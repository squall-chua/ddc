// Package helm is the read-only Helm provider. It uses the Helm v3 SDK but only
// the read actions (list, status, history, get values) — never install, upgrade,
// rollback, or uninstall. It reuses the existing kubeconfig (Helm releases live
// in the cluster), so ddc handles no secret of its own.
package helm

import (
	"context"
	"fmt"
	"os"
	"strings"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/cli"
	"helm.sh/helm/v3/pkg/release"
	"sigs.k8s.io/yaml"

	"github.com/squallchua/ddc/internal/provider"
)

func init() { provider.Register("helm", New) }

// New returns an unconnected Helm provider.
func New() provider.Provider { return &Provider{} }

// Provider inspects Helm releases. Namespace and AllNamespaces are set by the CLI
// before Connect.
type Provider struct {
	Namespace     string
	AllNamespaces bool

	cfg     *action.Configuration
	context string
}

// Result is a renderable list: a table plus typed Items for --json.
type Result struct {
	Headers []string
	Rows    [][]string
	Items   any
}

func (p *Provider) Name() string { return "helm" }

// Connect initializes a Helm action configuration against the active kubeconfig.
func (p *Provider) Connect(ctx context.Context, env string) error {
	settings := cli.New()
	if env != "" {
		settings.KubeContext = env
	}
	ns := p.Namespace
	if ns == "" {
		ns = settings.Namespace()
	}
	if p.AllNamespaces {
		ns = ""
	}
	cfg := new(action.Configuration)
	if err := cfg.Init(settings.RESTClientGetter(), ns, os.Getenv("HELM_DRIVER"), func(string, ...interface{}) {}); err != nil {
		return err
	}
	p.cfg = cfg
	p.context = settings.KubeContext
	p.Namespace = ns
	return nil
}

// Status verifies the cluster is reachable and returns a safe identity.
func (p *Provider) Status(ctx context.Context) (provider.Identity, error) {
	if err := p.cfg.KubeClient.IsReachable(); err != nil {
		return provider.Identity{}, err
	}
	return provider.Identity{
		Provider: "helm",
		Account:  p.context,
		Endpoint: p.Namespace,
		Source:   "kubeconfig",
	}, nil
}

// List lists releases.
func (p *Provider) List(ctx context.Context) (Result, error) {
	client := action.NewList(p.cfg)
	client.All = true
	client.AllNamespaces = p.AllNamespaces
	client.SetStateMask()
	rels, err := client.Run()
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(rels))
	for _, r := range rels {
		rows = append(rows, []string{
			r.Name, r.Namespace, fmt.Sprintf("%d", r.Version),
			relStatus(r), chartLabel(r), appVersion(r), updated(r),
		})
	}
	return Result{Headers: []string{"NAME", "NAMESPACE", "REVISION", "STATUS", "CHART", "APP-VERSION", "UPDATED"}, Rows: rows, Items: rels}, nil
}

// ReleaseStatus returns details for one release (the `helm status` equivalent).
func (p *Provider) ReleaseStatus(ctx context.Context, name string) (string, any, error) {
	rel, err := action.NewStatus(p.cfg).Run(name)
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Name:       %s\n", rel.Name)
	fmt.Fprintf(&b, "Namespace:  %s\n", rel.Namespace)
	fmt.Fprintf(&b, "Revision:   %d\n", rel.Version)
	fmt.Fprintf(&b, "Status:     %s\n", relStatus(rel))
	fmt.Fprintf(&b, "Chart:      %s\n", chartLabel(rel))
	fmt.Fprintf(&b, "AppVersion: %s\n", appVersion(rel))
	fmt.Fprintf(&b, "Updated:    %s\n", updated(rel))
	if rel.Info != nil && rel.Info.Description != "" {
		fmt.Fprintf(&b, "Description:%s\n", rel.Info.Description)
	}
	return b.String(), rel, nil
}

// History returns the revision history of a release.
func (p *Provider) History(ctx context.Context, name string) (Result, error) {
	client := action.NewHistory(p.cfg)
	client.Max = 10
	rels, err := client.Run(name)
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(rels))
	for _, r := range rels {
		desc := ""
		if r.Info != nil {
			desc = r.Info.Description
		}
		rows = append(rows, []string{
			fmt.Sprintf("%d", r.Version), relStatus(r), chartLabel(r), appVersion(r), updated(r), desc,
		})
	}
	return Result{Headers: []string{"REVISION", "STATUS", "CHART", "APP-VERSION", "UPDATED", "DESCRIPTION"}, Rows: rows, Items: rels}, nil
}

// Values returns the user-supplied values for a release. The output is redacted
// downstream like everything else.
func (p *Provider) Values(ctx context.Context, name string) (string, any, error) {
	vals, err := action.NewGetValues(p.cfg).Run(name)
	if err != nil {
		return "", nil, err
	}
	if len(vals) == 0 {
		return "(no user-supplied values)\n", vals, nil
	}
	out, err := yaml.Marshal(vals)
	if err != nil {
		return "", nil, err
	}
	return string(out), vals, nil
}

func relStatus(r *release.Release) string {
	if r.Info != nil {
		return r.Info.Status.String()
	}
	return ""
}

func chartLabel(r *release.Release) string {
	if r.Chart != nil && r.Chart.Metadata != nil {
		return r.Chart.Metadata.Name + "-" + r.Chart.Metadata.Version
	}
	return ""
}

func appVersion(r *release.Release) string {
	if r.Chart != nil && r.Chart.Metadata != nil {
		return r.Chart.Metadata.AppVersion
	}
	return ""
}

func updated(r *release.Release) string {
	if r.Info != nil && !r.Info.LastDeployed.IsZero() {
		return r.Info.LastDeployed.Format("2006-01-02 15:04:05")
	}
	return ""
}
