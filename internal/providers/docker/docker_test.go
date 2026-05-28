package docker

import (
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/docker/docker/client"
)

func newTestProvider(t *testing.T, handler http.HandlerFunc) *Provider {
	t.Helper()
	srv := httptest.NewServer(handler)
	t.Cleanup(srv.Close)
	host := "tcp://" + strings.TrimPrefix(srv.URL, "http://")
	c, err := client.NewClientWithOpts(
		client.WithHost(host),
		client.WithHTTPClient(srv.Client()),
		client.WithVersion("1.45"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &Provider{cli: c, host: host}
}

func TestDockerUsesOnlyGET(t *testing.T) {
	var methods []string
	p := newTestProvider(t, func(w http.ResponseWriter, r *http.Request) {
		methods = append(methods, r.Method)
		w.Header().Set("Content-Type", "application/json")
		switch {
		case strings.HasSuffix(r.URL.Path, "/containers/json"):
			io.WriteString(w, `[{"Id":"abcdef1234567890","Names":["/web"],"Image":"nginx","State":"running","Status":"Up 2 minutes","Created":1700000000}]`)
		case strings.Contains(r.URL.Path, "/containers/") && strings.HasSuffix(r.URL.Path, "/json"):
			io.WriteString(w, `{"Id":"abcdef1234567890","Name":"/web","Image":"nginx","RestartCount":0,"State":{"Status":"running","Running":true,"ExitCode":0}}`)
		case strings.HasSuffix(r.URL.Path, "/images/json"):
			io.WriteString(w, `[{"Id":"sha256:deadbeef00112233","RepoTags":["nginx:latest"],"Size":1048576,"Created":1700000000}]`)
		default:
			io.WriteString(w, `{}`)
		}
	})

	ctx := context.Background()
	if _, err := p.PS(ctx, true); err != nil {
		t.Fatalf("PS: %v", err)
	}
	if _, _, err := p.Inspect(ctx, "web"); err != nil {
		t.Fatalf("Inspect: %v", err)
	}
	if _, err := p.Images(ctx); err != nil {
		t.Fatalf("Images: %v", err)
	}

	if len(methods) == 0 {
		t.Fatal("no HTTP calls were made")
	}
	for _, m := range methods {
		if m != http.MethodGet {
			t.Fatalf("non-GET request issued: %s (all: %v)", m, methods)
		}
	}
}

func TestPSParsesRows(t *testing.T) {
	p := newTestProvider(t, func(w http.ResponseWriter, _ *http.Request) {
		io.WriteString(w, `[{"Id":"0123456789abcdef","Names":["/api"],"Image":"api:v1","State":"exited","Status":"Exited (1)","Created":1700000000}]`)
	})
	res, err := p.PS(context.Background(), true)
	if err != nil {
		t.Fatal(err)
	}
	row := strings.Join(res.Rows[0], " ")
	for _, want := range []string{"0123456789ab", "api", "api:v1", "exited"} {
		if !strings.Contains(row, want) {
			t.Fatalf("ps row missing %q: %q", want, row)
		}
	}
}
