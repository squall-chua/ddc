package gitlab

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
		Host:  srv.URL,
		token: credential.NewSecret("glpat-test"),
		http:  srv.Client(),
	}
}

func TestGitlabUsesOnlyGETWithAuth(t *testing.T) {
	var methods []string
	var sawAuth bool
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if r.Header.Get("PRIVATE-TOKEN") == "glpat-test" {
			sawAuth = true
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/user"):
			io.WriteString(w, `{"username":"ci-bot"}`)
		case strings.HasSuffix(r.URL.Path, "/trace"):
			io.WriteString(w, "job log line\n")
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			io.WriteString(w, `[{"id":50,"name":"build","stage":"build","status":"failed","failure_reason":"script_failure"}]`)
		case strings.HasSuffix(r.URL.Path, "/pipelines"):
			io.WriteString(w, `[{"id":1,"status":"failed","ref":"main","sha":"abcdef0123","source":"push"}]`)
		case strings.Contains(r.URL.Path, "/pipelines/"):
			io.WriteString(w, `{"id":1,"status":"failed","ref":"main","sha":"abcdef0123","source":"push"}`)
		default:
			io.WriteString(w, `{}`)
		}
	})

	ctx := context.Background()
	if _, err := p.Status(ctx); err != nil {
		t.Fatalf("Status: %v", err)
	}
	if _, err := p.ListPipelines(ctx, "group/project", "", "", 20); err != nil {
		t.Fatalf("ListPipelines: %v", err)
	}
	if _, _, err := p.ViewPipeline(ctx, "group/project", 1); err != nil {
		t.Fatalf("ViewPipeline: %v", err)
	}
	if _, err := p.JobLogs(ctx, "group/project", 50, 0); err != nil {
		t.Fatalf("JobLogs: %v", err)
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
		t.Fatal("expected PRIVATE-TOKEN header on requests")
	}
}

func TestListPipelinesParsesRows(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"id":42,"status":"failed","ref":"release","sha":"0123456789abcdef","source":"merge_request_event"}]`)
	})
	res, err := p.ListPipelines(context.Background(), "group/project", "", "", 20)
	if err != nil {
		t.Fatal(err)
	}
	row := strings.Join(res.Rows[0], " ")
	for _, want := range []string{"42", "failed", "release", "01234567", "merge_request_event"} {
		if !strings.Contains(row, want) {
			t.Fatalf("row missing %q: %q", want, row)
		}
	}
}

func TestProjectBaseEncodesPath(t *testing.T) {
	p := &Provider{}
	if got := p.projectBase("group/sub/project"); got != "/api/v4/projects/group%2Fsub%2Fproject" {
		t.Fatalf("projectBase path-encoding wrong: %q", got)
	}
	if got := p.projectBase("12345"); got != "/api/v4/projects/12345" {
		t.Fatalf("projectBase numeric id wrong: %q", got)
	}
}

func TestJobLogsCapsBytes(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, strings.Repeat("L", 10000))
	})
	capped, err := p.JobLogs(context.Background(), "group/project", 7, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 100 {
		t.Fatalf("limit not enforced: got %d bytes, want 100", len(capped))
	}
	full, err := p.JobLogs(context.Background(), "group/project", 7, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 10000 {
		t.Fatalf("limit 0 should be unbounded: got %d bytes, want 10000", len(full))
	}
}
