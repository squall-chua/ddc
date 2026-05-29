// Package argocd is the read-only Argo CD provider. It speaks directly to the
// Argo CD REST API using GET endpoints only — the official Argo CD Go client
// pulls in an enormous, replace-directive-heavy dependency tree, and a thin HTTP
// client keeps mutation structurally impossible. Credentials are reused from the
// argocd CLI config or the environment; the raw token never leaves this layer.
package argocd

import (
	"context"
	"crypto/tls"
	"encoding/json"
	"fmt"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/pmezard/go-difflib/difflib"
	"sigs.k8s.io/yaml"

	"github.com/squall-chua/ddc/internal/credential"
	"github.com/squall-chua/ddc/internal/provider"
)

func init() { provider.Register("argocd", New) }

// New returns an unconnected Argo CD provider.
func New() provider.Provider { return &Provider{} }

// Provider queries Argo CD using read-only REST calls. Server and Insecure are
// set by the CLI before Connect; both fall back to the environment/config.
type Provider struct {
	Server   string
	Insecure bool

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

func (p *Provider) Name() string { return "argocd" }

// Connect resolves the server address and token, then prepares an HTTP client.
func (p *Provider) Connect(ctx context.Context, env string) error {
	cfgServer, cfgToken, hasCfg := argocdConfig()

	p.Server = firstNonEmpty(p.Server, os.Getenv("ARGOCD_SERVER"), cfgServer)
	if p.Server == "" {
		return fmt.Errorf("no Argo CD server: pass --server, set ARGOCD_SERVER, or log in with the argocd CLI")
	}

	res, err := credential.TokenSpec{
		Provider: "argocd",
		Env:      env,
		EnvVars:  []string{"ARGOCD_AUTH_TOKEN"},
		Fallback: func() (credential.Secret, credential.Source, bool) {
			if hasCfg && !cfgToken.IsZero() {
				return cfgToken, credential.Source("argocd-config"), true
			}
			return credential.Secret{}, "", false
		},
	}.Resolve()
	if err != nil {
		return err
	}
	p.token = res.Secret
	p.source = res.Source

	if os.Getenv("ARGOCD_INSECURE") == "true" {
		p.Insecure = true
	}
	transport := http.DefaultTransport.(*http.Transport).Clone()
	if p.Insecure {
		transport.TLSClientConfig = &tls.Config{InsecureSkipVerify: true} //nolint:gosec // opt-in via --insecure/ARGOCD_INSECURE
	}
	p.http = &http.Client{Timeout: 30 * time.Second, Transport: transport}
	return nil
}

// Status returns the authenticated username (safe identity).
func (p *Provider) Status(ctx context.Context) (provider.Identity, error) {
	var info struct {
		LoggedIn bool   `json:"loggedIn"`
		Username string `json:"username"`
	}
	if err := p.get(ctx, "/api/v1/session/userinfo", &info); err != nil {
		return provider.Identity{}, err
	}
	if !info.LoggedIn {
		return provider.Identity{}, fmt.Errorf("not logged in to Argo CD")
	}
	return provider.Identity{
		Provider: "argocd",
		Account:  info.Username,
		Endpoint: p.Server,
		Source:   string(p.source),
	}, nil
}

type application struct {
	Metadata struct {
		Name      string `json:"name"`
		Namespace string `json:"namespace"`
	} `json:"metadata"`
	Spec struct {
		Project     string `json:"project"`
		Destination struct {
			Namespace string `json:"namespace"`
			Server    string `json:"server"`
		} `json:"destination"`
		Source struct {
			RepoURL        string `json:"repoURL"`
			Path           string `json:"path"`
			TargetRevision string `json:"targetRevision"`
		} `json:"source"`
	} `json:"spec"`
	Status struct {
		Sync   struct{ Status string } `json:"sync"`
		Health struct {
			Status  string `json:"status"`
			Message string `json:"message"`
		} `json:"health"`
		History []revisionHistory `json:"history"`
	} `json:"status"`
}

type revisionHistory struct {
	ID         int64  `json:"id"`
	Revision   string `json:"revision"`
	DeployedAt string `json:"deployedAt"`
	Source     struct {
		Path           string `json:"path"`
		TargetRevision string `json:"targetRevision"`
	} `json:"source"`
}

// ListApps lists Argo CD applications.
func (p *Provider) ListApps(ctx context.Context) (Result, error) {
	var resp struct {
		Items []application `json:"items"`
	}
	if err := p.get(ctx, "/api/v1/applications", &resp); err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(resp.Items))
	for _, a := range resp.Items {
		rows = append(rows, []string{
			a.Metadata.Name, a.Spec.Project, a.Status.Sync.Status,
			a.Status.Health.Status, a.Spec.Destination.Namespace, a.Spec.Source.TargetRevision,
		})
	}
	return Result{Headers: []string{"NAME", "PROJECT", "SYNC", "HEALTH", "DEST-NS", "REVISION"}, Rows: rows, Items: resp.Items}, nil
}

// GetApp returns a detailed view of one application.
func (p *Provider) GetApp(ctx context.Context, name string) (string, any, error) {
	var a application
	if err := p.get(ctx, "/api/v1/applications/"+name, &a); err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Name:       %s\n", a.Metadata.Name)
	fmt.Fprintf(&b, "Project:    %s\n", a.Spec.Project)
	fmt.Fprintf(&b, "Sync:       %s\n", a.Status.Sync.Status)
	fmt.Fprintf(&b, "Health:     %s\n", a.Status.Health.Status)
	if a.Status.Health.Message != "" {
		fmt.Fprintf(&b, "Message:    %s\n", a.Status.Health.Message)
	}
	fmt.Fprintf(&b, "Repo:       %s\n", a.Spec.Source.RepoURL)
	fmt.Fprintf(&b, "Path:       %s\n", a.Spec.Source.Path)
	fmt.Fprintf(&b, "Revision:   %s\n", a.Spec.Source.TargetRevision)
	fmt.Fprintf(&b, "Dest:       %s (ns %s)\n", a.Spec.Destination.Server, a.Spec.Destination.Namespace)
	return b.String(), a, nil
}

// Resources lists an application's managed resources and their health.
func (p *Provider) Resources(ctx context.Context, name string) (Result, error) {
	var tree struct {
		Nodes []struct {
			Kind      string `json:"kind"`
			Name      string `json:"name"`
			Namespace string `json:"namespace"`
			Health    struct {
				Status string `json:"status"`
			} `json:"health"`
		} `json:"nodes"`
	}
	if err := p.get(ctx, "/api/v1/applications/"+name+"/resource-tree", &tree); err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(tree.Nodes))
	for _, n := range tree.Nodes {
		rows = append(rows, []string{n.Kind, n.Namespace, n.Name, n.Health.Status})
	}
	return Result{Headers: []string{"KIND", "NAMESPACE", "NAME", "HEALTH"}, Rows: rows, Items: tree.Nodes}, nil
}

// History returns an application's deployment history, most recent first.
func (p *Provider) History(ctx context.Context, name string) (Result, error) {
	var a application
	if err := p.get(ctx, "/api/v1/applications/"+name, &a); err != nil {
		return Result{}, err
	}
	hist := a.Status.History
	rows := make([][]string, 0, len(hist))
	for i := len(hist) - 1; i >= 0; i-- {
		h := hist[i]
		src := h.Source.Path
		if h.Source.TargetRevision != "" {
			src += "@" + h.Source.TargetRevision
		}
		rows = append(rows, []string{fmt.Sprintf("%d", h.ID), shortRev(h.Revision), h.DeployedAt, src})
	}
	return Result{Headers: []string{"ID", "REVISION", "DEPLOYED-AT", "SOURCE"}, Rows: rows, Items: hist}, nil
}

type resourceDiff struct {
	Group               string `json:"group"`
	Kind                string `json:"kind"`
	Namespace           string `json:"namespace"`
	Name                string `json:"name"`
	TargetState         string `json:"targetState"`
	LiveState           string `json:"liveState"`
	NormalizedLiveState string `json:"normalizedLiveState"`
	Modified            bool   `json:"modified"`
}

// Diff shows, per managed resource, the difference between live and desired
// state as a unified diff. Secret bodies are never rendered (only that they
// differ), consistent with ddc's no-secrets posture — in both text and JSON.
func (p *Provider) Diff(ctx context.Context, name string) (string, any, error) {
	var resp struct {
		Items []resourceDiff `json:"items"`
	}
	if err := p.get(ctx, "/api/v1/applications/"+name+"/managed-resources", &resp); err != nil {
		return "", nil, err
	}

	var b strings.Builder
	changed := 0
	sanitized := make([]resourceDiff, 0, len(resp.Items))
	for _, it := range resp.Items {
		isSecret := strings.EqualFold(it.Kind, "Secret")

		s := it
		if isSecret {
			s.TargetState, s.LiveState, s.NormalizedLiveState = "[hidden]", "[hidden]", "[hidden]"
		}
		sanitized = append(sanitized, s)

		if !it.Modified {
			continue
		}
		changed++
		fmt.Fprintf(&b, "===== %s %s/%s =====\n", resourceKind(it.Group, it.Kind), it.Namespace, it.Name)
		if isSecret {
			b.WriteString("(Secret differs — body hidden; ddc never exposes secret material)\n\n")
			continue
		}
		live := it.NormalizedLiveState
		if live == "" {
			live = it.LiveState
		}
		ud := difflib.UnifiedDiff{
			A:        difflib.SplitLines(jsonToYAML(live)),
			B:        difflib.SplitLines(jsonToYAML(it.TargetState)),
			FromFile: "live",
			ToFile:   "desired",
			Context:  3,
		}
		text, _ := difflib.GetUnifiedDiffString(ud)
		if text == "" {
			text = "(no textual diff)\n"
		}
		b.WriteString(text)
		b.WriteString("\n")
	}
	if changed == 0 {
		b.WriteString("No differences — application is in sync.\n")
	}
	return b.String(), sanitized, nil
}

func resourceKind(group, kind string) string {
	if group == "" {
		return kind
	}
	return group + "/" + kind
}

func jsonToYAML(j string) string {
	if strings.TrimSpace(j) == "" {
		return ""
	}
	y, err := yaml.JSONToYAML([]byte(j))
	if err != nil {
		return j
	}
	return string(y)
}

func shortRev(rev string) string {
	if len(rev) >= 40 {
		return rev[:7]
	}
	return rev
}

func (p *Provider) get(ctx context.Context, path string, out any) error {
	base := p.Server
	if !strings.HasPrefix(base, "http://") && !strings.HasPrefix(base, "https://") {
		base = "https://" + base
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(base, "/")+path, nil)
	if err != nil {
		return err
	}
	req.Header.Set("Authorization", "Bearer "+p.token.Reveal())
	resp, err := p.http.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("argocd: HTTP %d for %s", resp.StatusCode, path)
	}
	return json.NewDecoder(resp.Body).Decode(out)
}

func firstNonEmpty(vs ...string) string {
	for _, v := range vs {
		if v != "" {
			return v
		}
	}
	return ""
}

// argocdConfig reads the argocd CLI config (~/.config/argocd/config) and returns
// the current server and its auth token.
func argocdConfig() (server string, token credential.Secret, ok bool) {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return "", credential.Secret{}, false
		}
		base = filepath.Join(home, ".config")
	}
	data, err := os.ReadFile(filepath.Join(base, "argocd", "config"))
	if err != nil {
		return "", credential.Secret{}, false
	}
	var cfg struct {
		CurrentContext string `json:"current-context"`
		Users          []struct {
			Name      string `json:"name"`
			AuthToken string `json:"auth-token"`
		} `json:"users"`
	}
	if err := yaml.Unmarshal(data, &cfg); err != nil || cfg.CurrentContext == "" {
		return "", credential.Secret{}, false
	}
	for _, u := range cfg.Users {
		if u.Name == cfg.CurrentContext && u.AuthToken != "" {
			return cfg.CurrentContext, credential.NewSecret(u.AuthToken), true
		}
	}
	return cfg.CurrentContext, credential.Secret{}, false
}
