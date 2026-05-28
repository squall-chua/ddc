package argocd

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/squall-chua/ddc/internal/credential"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	return &Provider{
		Server: srv.URL,
		token:  credential.NewSecret("test-token"),
		http:   srv.Client(),
	}
}

func TestArgoCDUsesOnlyGET(t *testing.T) {
	var methods []string
	var sawAuth bool
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Header.Get("Authorization") == "Bearer test-token" {
			sawAuth = true
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/resource-tree"):
			io.WriteString(w, `{"nodes":[{"kind":"Deployment","name":"web","namespace":"prod","health":{"status":"Degraded"}}]}`)
		case strings.Contains(r.URL.Path, "/applications/"):
			io.WriteString(w, `{"metadata":{"name":"web"},"spec":{"project":"default","source":{"repoURL":"git","targetRevision":"main"}},"status":{"sync":{"status":"OutOfSync"},"health":{"status":"Degraded"}}}`)
		case strings.HasSuffix(r.URL.Path, "/applications"):
			io.WriteString(w, `{"items":[{"metadata":{"name":"web"},"spec":{"project":"default"},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}]}`)
		default:
			io.WriteString(w, `{}`)
		}
	})

	ctx := context.Background()
	if _, err := p.ListApps(ctx); err != nil {
		t.Fatalf("ListApps: %v", err)
	}
	if _, _, err := p.GetApp(ctx, "web"); err != nil {
		t.Fatalf("GetApp: %v", err)
	}
	if _, err := p.Resources(ctx, "web"); err != nil {
		t.Fatalf("Resources: %v", err)
	}

	if len(methods) == 0 {
		t.Fatal("no HTTP calls were made")
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Fatalf("non-GET request issued: %s (all: %v)", m, methods)
		}
	}
	if !sawAuth {
		t.Fatal("expected bearer Authorization header on requests")
	}
}

func TestListAppsParsesRows(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"items":[{"metadata":{"name":"api"},"spec":{"project":"team-a","destination":{"namespace":"prod"},"source":{"targetRevision":"v1.2"}},"status":{"sync":{"status":"Synced"},"health":{"status":"Healthy"}}}]}`)
	})
	res, err := p.ListApps(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	row := strings.Join(res.Rows[0], " ")
	for _, want := range []string{"api", "team-a", "Synced", "Healthy", "prod", "v1.2"} {
		if !strings.Contains(row, want) {
			t.Fatalf("row missing %q: %q", want, row)
		}
	}
}
