package gha

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"

	"github.com/google/go-github/v74/github"
)

// newTestProvider points a real go-github client at a local test server so we
// can observe the exact HTTP methods issued.
func newTestProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	base, err := url.Parse(srv.URL + "/")
	if err != nil {
		t.Fatal(err)
	}
	c := github.NewClient(nil)
	c.BaseURL = base
	return &Provider{client: c}
}

func TestActionsUseOnlyGET(t *testing.T) {
	var methods []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/jobs"):
			io.WriteString(w, `{"total_count":1,"jobs":[{"id":10,"name":"build","status":"completed","conclusion":"failure"}]}`)
		case strings.Contains(r.URL.Path, "/actions/runs/"):
			io.WriteString(w, `{"id":1,"name":"CI","head_branch":"main","event":"push","status":"completed","conclusion":"failure"}`)
		case strings.HasSuffix(r.URL.Path, "/actions/runs"):
			io.WriteString(w, `{"total_count":1,"workflow_runs":[{"id":1,"name":"CI","head_branch":"main","event":"push","status":"completed","conclusion":"failure"}]}`)
		case strings.HasSuffix(r.URL.Path, "/actions/workflows"):
			io.WriteString(w, `{"total_count":1,"workflows":[{"id":5,"name":"CI","state":"active","path":".github/workflows/ci.yml"}]}`)
		default:
			io.WriteString(w, `{}`)
		}
	})

	ctx := context.Background()
	if _, err := p.ListRuns(ctx, "o", "r", "", "", "", 10); err != nil {
		t.Fatalf("ListRuns: %v", err)
	}
	if _, _, err := p.ViewRun(ctx, "o", "r", 1); err != nil {
		t.Fatalf("ViewRun: %v", err)
	}
	if _, err := p.ListWorkflows(ctx, "o", "r"); err != nil {
		t.Fatalf("ListWorkflows: %v", err)
	}

	if len(methods) == 0 {
		t.Fatal("no HTTP calls were made")
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Fatalf("non-GET request issued: %s (all methods: %v)", m, methods)
		}
	}
}

func TestListRunsParsesRows(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"total_count":1,"workflow_runs":[{"id":42,"name":"Deploy","head_branch":"release","event":"push","status":"completed","conclusion":"failure"}]}`)
	})
	res, err := p.ListRuns(context.Background(), "o", "r", "", "", "", 10)
	if err != nil {
		t.Fatal(err)
	}
	row := strings.Join(res.Rows[0], " ")
	for _, want := range []string{"42", "Deploy", "release", "failure"} {
		if !strings.Contains(row, want) {
			t.Fatalf("row missing %q: %q", want, row)
		}
	}
}

func TestFetchTextCapsBytes(t *testing.T) {
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, strings.Repeat("L", 10000))
	}))
	defer srv.Close()

	capped, err := fetchText(context.Background(), srv.URL, 100)
	if err != nil {
		t.Fatal(err)
	}
	if len(capped) != 100 {
		t.Fatalf("limit not enforced: got %d bytes, want 100", len(capped))
	}

	full, err := fetchText(context.Background(), srv.URL, 0)
	if err != nil {
		t.Fatal(err)
	}
	if len(full) != 10000 {
		t.Fatalf("limit 0 should be unbounded: got %d bytes, want 10000", len(full))
	}
}

func TestSplitRepo(t *testing.T) {
	if _, _, err := SplitRepo("octocat/hello"); err != nil {
		t.Fatalf("valid repo rejected: %v", err)
	}
	for _, bad := range []string{"", "noslash", "/r", "o/"} {
		if _, _, err := SplitRepo(bad); err == nil {
			t.Fatalf("expected %q to be rejected", bad)
		}
	}
}
