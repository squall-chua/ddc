// Package gha is the read-only GitHub Actions provider. It authenticates with a
// token resolved from the environment, the gh CLI's config, or the ddc keychain
// (the raw token never leaves the credential package except to set the auth
// header), and calls only read endpoints of the GitHub REST API.
package gha

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/google/go-github/v74/github"

	"github.com/squall-chua/ddc/internal/credential"
	"github.com/squall-chua/ddc/internal/provider"
)

func init() { provider.Register("gha", New) }

// New returns an unconnected GitHub Actions provider.
func New() provider.Provider { return &Provider{} }

// Provider queries GitHub Actions using read-only REST calls.
type Provider struct {
	client *github.Client
	source credential.Source
}

// Result is a renderable list: a table plus typed Items for --json.
type Result struct {
	Headers []string
	Rows    [][]string
	Items   any
}

func (p *Provider) Name() string { return "gha" }

// Connect resolves a token and builds an authenticated client.
func (p *Provider) Connect(ctx context.Context, env string) error {
	res, err := credential.TokenSpec{
		Provider: "gha",
		Env:      env,
		EnvVars:  []string{"GH_TOKEN", "GITHUB_TOKEN"},
		Fallback: ghConfigToken,
	}.Resolve()
	if err != nil {
		return err
	}
	p.client = github.NewClient(nil).WithAuthToken(res.Secret.Reveal())
	p.source = res.Source
	return nil
}

// Status returns the authenticated login (safe identity).
func (p *Provider) Status(ctx context.Context) (provider.Identity, error) {
	u, _, err := p.client.Users.Get(ctx, "")
	if err != nil {
		return provider.Identity{}, err
	}
	return provider.Identity{
		Provider: "gha",
		Account:  u.GetLogin(),
		Endpoint: "github.com",
		Source:   string(p.source),
	}, nil
}

// ListRuns lists recent workflow runs, optionally filtered by workflow name,
// branch, and status.
func (p *Provider) ListRuns(ctx context.Context, owner, repo, workflow, branch, status string, limit int) (Result, error) {
	opts := &github.ListWorkflowRunsOptions{
		Branch:      branch,
		Status:      status,
		ListOptions: github.ListOptions{PerPage: limit},
	}
	runs, _, err := p.client.Actions.ListRepositoryWorkflowRuns(ctx, owner, repo, opts)
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(runs.WorkflowRuns))
	items := make([]*github.WorkflowRun, 0, len(runs.WorkflowRuns))
	for _, r := range runs.WorkflowRuns {
		if workflow != "" && !strings.EqualFold(r.GetName(), workflow) {
			continue
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", r.GetID()), r.GetName(), r.GetHeadBranch(),
			r.GetEvent(), r.GetStatus(), r.GetConclusion(),
			r.GetCreatedAt().Format(time.RFC3339),
		})
		items = append(items, r)
	}
	return Result{Headers: []string{"ID", "WORKFLOW", "BRANCH", "EVENT", "STATUS", "CONCLUSION", "CREATED"}, Rows: rows, Items: items}, nil
}

// ViewRun returns a detailed view of one run including its jobs and failed steps.
func (p *Provider) ViewRun(ctx context.Context, owner, repo string, runID int64) (string, any, error) {
	run, _, err := p.client.Actions.GetWorkflowRunByID(ctx, owner, repo, runID)
	if err != nil {
		return "", nil, err
	}
	jobs, _, err := p.client.Actions.ListWorkflowJobs(ctx, owner, repo, runID, &github.ListWorkflowJobsOptions{})
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Run:        %d\n", run.GetID())
	fmt.Fprintf(&b, "Workflow:   %s\n", run.GetName())
	fmt.Fprintf(&b, "Branch:     %s\n", run.GetHeadBranch())
	fmt.Fprintf(&b, "Event:      %s\n", run.GetEvent())
	fmt.Fprintf(&b, "Status:     %s\n", run.GetStatus())
	fmt.Fprintf(&b, "Conclusion: %s\n", run.GetConclusion())
	fmt.Fprintf(&b, "URL:        %s\n\n", run.GetHTMLURL())
	b.WriteString("Jobs:\n")
	for _, j := range jobs.Jobs {
		fmt.Fprintf(&b, "  [%d] %s: %s/%s\n", j.GetID(), j.GetName(), j.GetStatus(), j.GetConclusion())
		for _, step := range j.Steps {
			if step.GetConclusion() == "failure" {
				fmt.Fprintf(&b, "      failed step %d: %q\n", step.GetNumber(), step.GetName())
			}
		}
	}
	return b.String(), map[string]any{"run": run, "jobs": jobs.Jobs}, nil
}

// RunLogs returns logs for a single job. When jobID is 0 it picks the first
// failed job of the run.
func (p *Provider) RunLogs(ctx context.Context, owner, repo string, runID, jobID int64) (string, error) {
	if jobID == 0 {
		jobs, _, err := p.client.Actions.ListWorkflowJobs(ctx, owner, repo, runID, &github.ListWorkflowJobsOptions{})
		if err != nil {
			return "", err
		}
		for _, j := range jobs.Jobs {
			if j.GetConclusion() == "failure" {
				jobID = j.GetID()
				break
			}
		}
		if jobID == 0 {
			return "", fmt.Errorf("no failed job in run %d; pass --job <id> (see `ddc gha run view %d`)", runID, runID)
		}
	}
	u, _, err := p.client.Actions.GetWorkflowJobLogs(ctx, owner, repo, jobID, 3)
	if err != nil {
		return "", err
	}
	return fetchText(ctx, u.String())
}

// ListWorkflows lists the workflows defined in a repository.
func (p *Provider) ListWorkflows(ctx context.Context, owner, repo string) (Result, error) {
	wfs, _, err := p.client.Actions.ListWorkflows(ctx, owner, repo, &github.ListOptions{PerPage: 100})
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(wfs.Workflows))
	for _, w := range wfs.Workflows {
		rows = append(rows, []string{fmt.Sprintf("%d", w.GetID()), w.GetName(), w.GetState(), w.GetPath()})
	}
	return Result{Headers: []string{"ID", "NAME", "STATE", "PATH"}, Rows: rows, Items: wfs.Workflows}, nil
}

// SplitRepo parses an "owner/repo" string.
func SplitRepo(s string) (owner, repo string, err error) {
	parts := strings.SplitN(strings.TrimSpace(s), "/", 2)
	if len(parts) != 2 || parts[0] == "" || parts[1] == "" {
		return "", "", fmt.Errorf("invalid repository %q, expected owner/repo", s)
	}
	return parts[0], parts[1], nil
}

// fetchText downloads a pre-signed log URL (no auth needed) as text.
func fetchText(ctx context.Context, url string) (string, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return "", err
	}
	resp, err := http.DefaultClient.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("fetch logs: HTTP %d", resp.StatusCode)
	}
	data, err := io.ReadAll(resp.Body)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// ghConfigToken reads the gh CLI's stored token from hosts.yml (github.com).
func ghConfigToken() (credential.Secret, credential.Source, bool) {
	path := filepath.Join(os.Getenv("GH_CONFIG_DIR"), "hosts.yml")
	if os.Getenv("GH_CONFIG_DIR") == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return credential.Secret{}, "", false
		}
		path = filepath.Join(home, ".config", "gh", "hosts.yml")
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return credential.Secret{}, "", false
	}
	for _, line := range strings.Split(string(data), "\n") {
		line = strings.TrimSpace(line)
		if after, ok := strings.CutPrefix(line, "oauth_token:"); ok {
			if tok := strings.TrimSpace(after); tok != "" {
				return credential.NewSecret(tok), credential.SourceGHConfig, true
			}
		}
	}
	return credential.Secret{}, "", false
}
