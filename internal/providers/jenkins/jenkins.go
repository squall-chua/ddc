// Package jenkins is the read-only Jenkins provider. It calls the Jenkins REST
// API using GET endpoints only, authenticating with HTTP basic auth (username +
// API token). The token is resolved from the environment or the ddc keychain and
// never leaves this layer.
package jenkins

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"time"

	"github.com/squall-chua/ddc/internal/credential"
	"github.com/squall-chua/ddc/internal/provider"
)

func init() { provider.Register("jenkins", New) }

// New returns an unconnected Jenkins provider.
func New() provider.Provider { return &Provider{} }

// Provider queries Jenkins using read-only REST calls. URL is set by the CLI
// from --url and falls back to JENKINS_URL.
type Provider struct {
	URL string

	user   string
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

func (p *Provider) Name() string { return "jenkins" }

// Connect resolves the server URL, username, and API token.
func (p *Provider) Connect(ctx context.Context, env string) error {
	p.URL = firstNonEmpty(p.URL, os.Getenv("JENKINS_URL"))
	if p.URL == "" {
		return fmt.Errorf("no Jenkins URL: pass --url or set JENKINS_URL: %w", credential.ErrNotConfigured)
	}
	p.user = firstNonEmpty(os.Getenv("JENKINS_USER"), os.Getenv("JENKINS_USERNAME"))

	res, err := credential.TokenSpec{
		Provider: "jenkins",
		Env:      env,
		EnvVars:  []string{"JENKINS_TOKEN", "JENKINS_API_TOKEN"},
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
	var who struct {
		Name          string `json:"name"`
		Authenticated bool   `json:"authenticated"`
	}
	if err := p.getJSON(ctx, "/whoAmI/api/json", &who); err != nil {
		return provider.Identity{}, err
	}
	return provider.Identity{
		Provider: "jenkins",
		Account:  who.Name,
		Endpoint: p.URL,
		Source:   string(p.source),
	}, nil
}

var colorStatus = map[string]string{
	"blue": "Success", "red": "Failed", "yellow": "Unstable",
	"aborted": "Aborted", "disabled": "Disabled", "notbuilt": "NotBuilt",
}

// ListJobs lists jobs and their last-build status.
func (p *Provider) ListJobs(ctx context.Context) (Result, error) {
	var resp struct {
		Jobs []struct {
			Name  string `json:"name"`
			Color string `json:"color"`
			URL   string `json:"url"`
		} `json:"jobs"`
	}
	if err := p.getJSON(ctx, "/api/json?tree=jobs[name,color,url]", &resp); err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(resp.Jobs))
	for _, j := range resp.Jobs {
		rows = append(rows, []string{j.Name, jobStatus(j.Color), j.URL})
	}
	return Result{Headers: []string{"NAME", "STATUS", "URL"}, Rows: rows, Items: resp.Jobs}, nil
}

type build struct {
	Number      int64  `json:"number"`
	Result      string `json:"result"`
	Building    bool   `json:"building"`
	Duration    int64  `json:"duration"`
	Timestamp   int64  `json:"timestamp"`
	DisplayName string `json:"displayName"`
	URL         string `json:"url"`
}

// ViewBuild returns details of a single build.
func (p *Provider) ViewBuild(ctx context.Context, job, number string) (string, any, error) {
	var b build
	path := jobPath(job) + "/" + number + "/api/json?tree=number,result,building,duration,timestamp,displayName,url"
	if err := p.getJSON(ctx, path, &b); err != nil {
		return "", nil, err
	}
	result := b.Result
	if b.Building {
		result = "BUILDING"
	}
	var sb strings.Builder
	fmt.Fprintf(&sb, "Job:      %s\n", job)
	fmt.Fprintf(&sb, "Build:    #%d (%s)\n", b.Number, b.DisplayName)
	fmt.Fprintf(&sb, "Result:   %s\n", result)
	fmt.Fprintf(&sb, "Duration: %s\n", time.Duration(b.Duration)*time.Millisecond)
	if b.Timestamp > 0 {
		fmt.Fprintf(&sb, "Started:  %s\n", time.UnixMilli(b.Timestamp).Format(time.RFC3339))
	}
	fmt.Fprintf(&sb, "URL:      %s\n", b.URL)
	return sb.String(), b, nil
}

// BuildLogs returns a window of a build's console log. An empty number means the
// last build. It fetches from byte offset skip via Jenkins' progressiveText
// endpoint and reads at most limit bytes (limit <= 0 means no cap), so the CLI
// never buffers an unbounded log in memory. It also returns the next byte offset
// to resume from and whether more output remains beyond the returned window.
func (p *Provider) BuildLogs(ctx context.Context, job, number string, skip, limit int64) (text string, next int64, more bool, err error) {
	if number == "" {
		number = "lastBuild"
	}
	if skip < 0 {
		skip = 0
	}
	path := fmt.Sprintf("%s/%s/logText/progressiveText?start=%d", jobPath(job), number, skip)
	req, err := p.newRequest(ctx, path)
	if err != nil {
		return "", 0, false, err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return "", 0, false, err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return "", 0, false, fmt.Errorf("jenkins: HTTP %d for %s", resp.StatusCode, path)
	}

	var reader io.Reader = resp.Body
	if limit > 0 {
		reader = io.LimitReader(resp.Body, limit)
	}
	data, err := io.ReadAll(reader)
	if err != nil {
		return "", 0, false, err
	}

	next = skip + int64(len(data))
	// X-Text-Size is the total bytes Jenkins holds for this log; X-More-Data is set
	// while the build is still producing output. If we capped the read below the
	// total, there is more to fetch even after the build finishes.
	end := next
	if v := resp.Header.Get("X-Text-Size"); v != "" {
		if n, perr := strconv.ParseInt(v, 10, 64); perr == nil {
			end = n
		}
	}
	more = resp.Header.Get("X-More-Data") == "true" || next < end
	return string(data), next, more, nil
}

func (p *Provider) newRequest(ctx context.Context, path string) (*http.Request, error) {
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(p.URL, "/")+path, nil)
	if err != nil {
		return nil, err
	}
	if p.user != "" {
		req.SetBasicAuth(p.user, p.token.Reveal())
	}
	return req, nil
}

func (p *Provider) getJSON(ctx context.Context, path string, out any) error {
	req, err := p.newRequest(ctx, path)
	if err != nil {
		return err
	}
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("jenkins: HTTP %d for %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func jobStatus(color string) string {
	if strings.HasSuffix(color, "_anime") {
		return "Running"
	}
	if s, ok := colorStatus[color]; ok {
		return s
	}
	return color
}

// jobPath builds the REST path for a job, supporting folders ("a/b" -> /job/a/job/b).
func jobPath(name string) string {
	return "/job/" + strings.Join(strings.Split(strings.Trim(name, "/"), "/"), "/job/")
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}
