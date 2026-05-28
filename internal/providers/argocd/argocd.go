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

	"sigs.k8s.io/yaml"

	"github.com/squallchua/ddc/internal/credential"
	"github.com/squallchua/ddc/internal/provider"
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
	} `json:"status"`
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
