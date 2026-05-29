package jenkins

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
		URL:   srv.URL,
		user:  "ci",
		token: credential.NewSecret("api-token"),
		http:  srv.Client(),
	}
}

func TestJenkinsUsesOnlyGETWithAuth(t *testing.T) {
	var methods []string
	var sawAuth bool
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		if u, _, ok := r.BasicAuth(); ok && u == "ci" {
			sawAuth = true
		}
		switch {
		case strings.HasSuffix(r.URL.Path, "/progressiveText"):
			io.WriteString(w, "build log line\n")
		case strings.Contains(r.URL.Path, "/job/"):
			io.WriteString(w, `{"number":7,"result":"FAILURE","building":false,"duration":1234,"displayName":"#7"}`)
		default:
			io.WriteString(w, `{"jobs":[{"name":"deploy","color":"red","url":"http://j/job/deploy"}]}`)
		}
	})

	ctx := context.Background()
	if _, err := p.ListJobs(ctx); err != nil {
		t.Fatalf("ListJobs: %v", err)
	}
	if _, _, err := p.ViewBuild(ctx, "deploy", "7"); err != nil {
		t.Fatalf("ViewBuild: %v", err)
	}
	if _, _, _, err := p.BuildLogs(ctx, "deploy", "7", 0, 0); err != nil {
		t.Fatalf("BuildLogs: %v", err)
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
		t.Fatal("expected basic auth on requests")
	}
}

func TestListJobsParsesStatus(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `{"jobs":[{"name":"build","color":"blue"},{"name":"e2e","color":"red_anime"}]}`)
	})
	res, err := p.ListJobs(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	flat := strings.Join(res.Rows[0], " ") + " " + strings.Join(res.Rows[1], " ")
	for _, want := range []string{"build", "Success", "e2e", "Running"} {
		if !strings.Contains(flat, want) {
			t.Fatalf("jobs output missing %q: %q", want, flat)
		}
	}
}

func TestJobPathSupportsFolders(t *testing.T) {
	if got := jobPath("a/b"); got != "/job/a/job/b" {
		t.Fatalf("jobPath(a/b) = %q, want /job/a/job/b", got)
	}
	if got := jobPath("solo"); got != "/job/solo" {
		t.Fatalf("jobPath(solo) = %q, want /job/solo", got)
	}
}

func TestBuildLogsSkipAndLimit(t *testing.T) {
	var gotStart string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		gotStart = r.URL.Query().Get("start")
		w.Header().Set("X-Text-Size", "1000") // server holds far more than we read
		w.Header().Set("X-More-Data", "false")
		io.WriteString(w, strings.Repeat("x", 100))
	})

	text, next, more, err := p.BuildLogs(context.Background(), "deploy", "12", 10, 5)
	if err != nil {
		t.Fatal(err)
	}
	if gotStart != "10" {
		t.Fatalf("skip not sent as start offset: got start=%q, want 10", gotStart)
	}
	if int64(len(text)) != 5 {
		t.Fatalf("limit not enforced: returned %d bytes, want 5", len(text))
	}
	if next != 15 {
		t.Fatalf("next = %d, want 15 (skip+limit)", next)
	}
	if !more {
		t.Fatal("expected more=true when the log exceeds the returned window")
	}
}
