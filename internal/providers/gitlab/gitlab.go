// Package gitlab is the read-only GitLab CI provider. It calls the GitLab REST
// API (v4) using GET endpoints only, authenticating with a personal/project
// access token sent in the PRIVATE-TOKEN header. The token is resolved from the
// environment, the glab CLI's config, or the ddc keychain and never leaves this
// layer. Following the argocd/jenkins pattern, a thin hand-rolled client keeps
// mutation structurally impossible without dragging in a large SDK.
package gitlab

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"time"

	"sigs.k8s.io/yaml"

	"github.com/squall-chua/ddc/internal/credential"
	"github.com/squall-chua/ddc/internal/provider"
)

func init() { provider.Register("gitlab", New) }

// New returns an unconnected GitLab provider.
func New() provider.Provider { return &Provider{} }

// Provider queries GitLab using read-only REST calls. Host is set by the CLI
// from --host and falls back to the environment, defaulting to gitlab.com.
type Provider struct {
	Host string

	token  credential.Secret
	source credential.Source
	http   *http.Client
}

// Result is a renderable list: a table plus typed Items for --json.
type Result struct {
	Headers []string
	Rows    [][]string
	Items   any
}

func (p *Provider) Name() string { return "gitlab" }

// Connect resolves the host and token, then prepares an HTTP client.
func (p *Provider) Connect(ctx context.Context, env string) error {
	p.Host = firstNonEmpty(p.Host, os.Getenv("GITLAB_HOST"), os.Getenv("CI_SERVER_URL"), "https://gitlab.com")
	if !strings.Contains(p.Host, "://") {
		p.Host = "https://" + p.Host
	}
	hostname := p.Host
	if u, err := url.Parse(p.Host); err == nil && u.Host != "" {
		hostname = u.Host
	}

	res, err := credential.TokenSpec{
		Provider: "gitlab",
		Env:      env,
		EnvVars:  []string{"GITLAB_TOKEN"},
		Fallback: func() (credential.Secret, credential.Source, bool) {
			return glabConfigToken(hostname)
		},
	}.Resolve()
	if err != nil {
		return err
	}
	p.token = res.Secret
	p.source = res.Source
	p.http = &http.Client{Timeout: 30 * time.Second}
	return nil
}

// Status returns the authenticated username (safe identity).
func (p *Provider) Status(ctx context.Context) (provider.Identity, error) {
	var u struct {
		Username string `json:"username"`
	}
	if err := p.get(ctx, "/api/v4/user", &u); err != nil {
		return provider.Identity{}, err
	}
	return provider.Identity{
		Provider: "gitlab",
		Account:  u.Username,
		Endpoint: p.Host,
		Source:   string(p.source),
	}, nil
}

type pipeline struct {
	ID        int64  `json:"id"`
	Status    string `json:"status"`
	Ref       string `json:"ref"`
	SHA       string `json:"sha"`
	Source    string `json:"source"`
	WebURL    string `json:"web_url"`
	CreatedAt string `json:"created_at"`
}

// ListPipelines lists recent pipelines, optionally filtered by ref and status.
func (p *Provider) ListPipelines(ctx context.Context, project, ref, status string, limit int) (Result, error) {
	q := url.Values{}
	if limit > 0 {
		q.Set("per_page", strconv.Itoa(limit))
	}
	if ref != "" {
		q.Set("ref", ref)
	}
	if status != "" {
		q.Set("status", status)
	}
	path := p.projectBase(project) + "/pipelines"
	if len(q) > 0 {
		path += "?" + q.Encode()
	}
	var pipes []pipeline
	if err := p.get(ctx, path, &pipes); err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(pipes))
	for _, pl := range pipes {
		rows = append(rows, []string{
			strconv.FormatInt(pl.ID, 10), pl.Status, pl.Ref, shortSHA(pl.SHA), pl.Source, pl.CreatedAt,
		})
	}
	return Result{Headers: []string{"ID", "STATUS", "REF", "SHA", "SOURCE", "CREATED"}, Rows: rows, Items: pipes}, nil
}

type job struct {
	ID            int64  `json:"id"`
	Name          string `json:"name"`
	Stage         string `json:"stage"`
	Status        string `json:"status"`
	AllowFailure  bool   `json:"allow_failure"`
	FailureReason string `json:"failure_reason"`
	WebURL        string `json:"web_url"`
}

// ViewPipeline returns a detailed view of one pipeline including its jobs and
// which of them failed.
func (p *Provider) ViewPipeline(ctx context.Context, project string, pipelineID int64) (string, any, error) {
	base := p.projectBase(project)
	var pl pipeline
	if err := p.get(ctx, fmt.Sprintf("%s/pipelines/%d", base, pipelineID), &pl); err != nil {
		return "", nil, err
	}
	var jobs []job
	if err := p.get(ctx, fmt.Sprintf("%s/pipelines/%d/jobs", base, pipelineID), &jobs); err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Pipeline:  %d\n", pl.ID)
	fmt.Fprintf(&b, "Status:    %s\n", pl.Status)
	fmt.Fprintf(&b, "Ref:       %s\n", pl.Ref)
	fmt.Fprintf(&b, "SHA:       %s\n", shortSHA(pl.SHA))
	fmt.Fprintf(&b, "Source:    %s\n", pl.Source)
	fmt.Fprintf(&b, "URL:       %s\n\n", pl.WebURL)
	b.WriteString("Jobs:\n")
	for _, j := range jobs {
		fmt.Fprintf(&b, "  [%d] %s (%s): %s", j.ID, j.Name, j.Stage, j.Status)
		if j.Status == "failed" && j.AllowFailure {
			b.WriteString(" (allowed to fail)")
		}
		if j.FailureReason != "" {
			fmt.Fprintf(&b, " — %s", j.FailureReason)
		}
		b.WriteString("\n")
	}
	return b.String(), map[string]any{"pipeline": pl, "jobs": jobs}, nil
}

// JobLogs returns a job's trace (console log), reading at most limit bytes
// (limit <= 0 means no cap) so a large trace cannot exhaust memory.
func (p *Provider) JobLogs(ctx context.Context, project string, jobID, limit int64) (string, error) {
	path := fmt.Sprintf("%s/jobs/%d/trace", p.projectBase(project), jobID)
	resp, err := p.do(ctx, path)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", fmt.Errorf("gitlab: HTTP %d for %s", resp.StatusCode, path)
	}
	var reader io.Reader = resp.Body
	if limit > 0 {
		reader = io.LimitReader(resp.Body, limit)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", err
	}
	return string(data), nil
}

// projectBase builds the REST base path for a project, URL-encoding a path-style
// id ("group/project" -> "group%2Fproject") so the GitLab API accepts it.
func (p *Provider) projectBase(project string) string {
	return "/api/v4/projects/" + url.QueryEscape(project)
}

func (p *Provider) do(ctx context.Context, path string) (*http.Response, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.Host, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	req.Header.Set("PRIVATE-TOKEN", p.token.Reveal())
	return p.http.Do(req)
}

func (p *Provider) get(ctx context.Context, path string, out any) error {
	resp, err := p.do(ctx, path)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("gitlab: HTTP %d for %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func shortSHA(sha string) string {
	if len(sha) >= 8 {
		return sha[:8]
	}
	return sha
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// glabConfigToken reads the glab CLI config (config.yml) and returns the token
// stored for the given host.
func glabConfigToken(host string) (credential.Secret, credential.Source, bool) {
	base := os.Getenv("GLAB_CONFIG_DIR")
	if base == "" {
		xdg := os.Getenv("XDG_CONFIG_HOME")
		if xdg == "" {
			home, err := os.UserHomeDir()
			if err != nil {
				return credential.Secret{}, "", false
			}
			xdg = filepath.Join(home, ".config")
		}
		base = filepath.Join(xdg, "glab-cli")
	}
	data, err := os.ReadFile(filepath.Join(base, "config.yml"))
	if err != nil {
		return credential.Secret{}, "", false
	}
	var cfg struct {
		Hosts map[string]struct {
			Token string `json:"token"`
		} `json:"hosts"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil {
		return credential.Secret{}, "", false
	}
	if h, ok := cfg.Hosts[host]; ok && h.Token != "" {
		return credential.NewSecret(h.Token), credential.Source("glab-config"), true
	}
	return credential.Secret{}, "", false
}
