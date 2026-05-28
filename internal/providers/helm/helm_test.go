package helm

import (
	"context"
	"io"
	"strings"
	"testing"

	"helm.sh/helm/v3/pkg/action"
	"helm.sh/helm/v3/pkg/chart"
	"helm.sh/helm/v3/pkg/chartutil"
	kubefake "helm.sh/helm/v3/pkg/kube/fake"
	"helm.sh/helm/v3/pkg/release"
	"helm.sh/helm/v3/pkg/storage"
	"helm.sh/helm/v3/pkg/storage/driver"
	helmtime "helm.sh/helm/v3/pkg/time"
)

func newTestProvider(t *testing.T, rels ...*release.Release) *Provider {
	t.Helper()
	store := storage.Init(driver.NewMemory())
	for _, r := range rels {
		if err := store.Create(r); err != nil {
			t.Fatal(err)
		}
	}
	cfg := &action.Configuration{
		Releases:     store,
		KubeClient:   &kubefake.PrintingKubeClient{Out: io.Discard},
		Capabilities: chartutil.DefaultCapabilities,
		Log:          func(string, ...interface{}) {},
	}
	return &Provider{cfg: cfg, Namespace: "default"}
}

func seed(name, ns string, version int, status release.Status) *release.Release {
	return &release.Release{
		Name:      name,
		Namespace: ns,
		Version:   version,
		Info:      &release.Info{Status: status, LastDeployed: helmtime.Now(), Description: "Install complete"},
		Chart:     &chart.Chart{Metadata: &chart.Metadata{Name: "mychart", Version: "1.0.0", AppVersion: "2.0"}},
	}
}

func TestListReturnsReleases(t *testing.T) {
	p := newTestProvider(t, seed("web", "default", 1, release.StatusDeployed))
	res, err := p.List(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 1 {
		t.Fatalf("expected 1 release, got %d", len(res.Rows))
	}
	row := strings.Join(res.Rows[0], " ")
	for _, want := range []string{"web", "default", "mychart-1.0.0", "deployed", "2.0"} {
		if !strings.Contains(row, want) {
			t.Fatalf("list row missing %q: %q", want, row)
		}
	}
}

func TestReleaseStatusAndHistory(t *testing.T) {
	p := newTestProvider(t, seed("web", "default", 1, release.StatusDeployed))

	text, _, err := p.ReleaseStatus(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"web", "deployed", "mychart-1.0.0"} {
		if !strings.Contains(text, want) {
			t.Fatalf("status missing %q: %q", want, text)
		}
	}

	hist, err := p.History(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(hist.Rows) == 0 {
		t.Fatal("expected at least one history row")
	}
}
