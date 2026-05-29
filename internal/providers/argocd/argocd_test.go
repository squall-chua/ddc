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
		case strings.HasSuffix(r.URL.Path, "/managed-resources"):
			io.WriteString(w, `{"items":[]}`)
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
	if _, err := p.History(ctx, "web"); err != nil {
		t.Fatalf("History: %v", err)
	}
	if _, _, err := p.Diff(ctx, "web"); err != nil {
		t.Fatalf("Diff: %v", err)
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

func TestHistoryParsesRows(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"metadata":{"name":"web"},"status":{"history":[`+
			`{"id":1,"revision":"abc123","deployedAt":"2024-01-01T00:00:00Z","source":{"path":"app","targetRevision":"v1"}},`+
			`{"id":2,"revision":"def456","deployedAt":"2024-02-01T00:00:00Z","source":{"path":"app","targetRevision":"v2"}}]}}`)
	})
	res, err := p.History(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if len(res.Rows) != 2 {
		t.Fatalf("want 2 history rows, got %d", len(res.Rows))
	}
	if res.Rows[0][0] != "2" {
		t.Fatalf("expected most-recent (id 2) first, got id %q", res.Rows[0][0])
	}
	if !strings.Contains(strings.Join(res.Rows[0], " "), "v2") {
		t.Fatalf("history row missing source revision: %v", res.Rows[0])
	}
}

func TestDiffRendersUnifiedAndHidesSecrets(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"items":[`+
			`{"kind":"Deployment","namespace":"prod","name":"web","modified":true,"normalizedLiveState":"{\"spec\":{\"replicas\":1}}","targetState":"{\"spec\":{\"replicas\":3}}"},`+
			`{"kind":"Secret","namespace":"prod","name":"db","modified":true,"liveState":"{\"data\":{\"password\":\"c3VwZXJzZWNyZXQ=\"}}","targetState":"{\"data\":{\"password\":\"bmV3c2VjcmV0\"}}"}]}`)
	})
	text, obj, err := p.Diff(context.Background(), "web")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(text, "replicas") {
		t.Fatalf("expected deployment diff to mention replicas:\n%s", text)
	}
	if !strings.Contains(text, "+") || !strings.Contains(text, "-") {
		t.Fatalf("expected unified diff markers:\n%s", text)
	}
	if !strings.Contains(text, "body hidden") {
		t.Fatalf("expected secret-hidden note:\n%s", text)
	}
	for _, leak := range []string{"c3VwZXJzZWNyZXQ=", "bmV3c2VjcmV0"} {
		if strings.Contains(text, leak) {
			t.Fatalf("secret material leaked in diff text (%q):\n%s", leak, text)
		}
	}

	// The --json payload must also have Secret states blanked.
	items, ok := obj.([]resourceDiff)
	if !ok {
		t.Fatalf("Diff items have unexpected type %T", obj)
	}
	for _, it := range items {
		if strings.EqualFold(it.Kind, "Secret") &&
			(it.LiveState != "[hidden]" || it.TargetState != "[hidden]" || it.NormalizedLiveState != "[hidden]") {
			t.Fatalf("secret state not sanitized for JSON: %+v", it)
		}
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
