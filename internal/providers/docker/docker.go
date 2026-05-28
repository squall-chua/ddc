// Package docker is the read-only Docker provider. It uses the official Docker
// Engine API client but calls only inspection endpoints (ps, inspect, logs,
// images) — no run/exec/stop/rm. It connects via the standard Docker environment
// (DOCKER_HOST etc.); there is no secret for ddc to handle.
package docker

import (
	"context"
	"fmt"
	"io"
	"strings"
	"time"

	"github.com/docker/docker/api/types/container"
	"github.com/docker/docker/api/types/image"
	"github.com/docker/docker/client"
	"github.com/docker/docker/pkg/stdcopy"

	"github.com/squallchua/ddc/internal/provider"
)

func init() { provider.Register("docker", New) }

// New returns an unconnected Docker provider.
func New() provider.Provider { return &Provider{} }

// Provider inspects a Docker daemon using read-only Engine API calls.
type Provider struct {
	cli  client.APIClient
	host string
}

// Result is a renderable list: a table plus typed Items for --json.
type Result struct {
	Headers []string
	Rows    [][]string
	Items   any
}

func (p *Provider) Name() string { return "docker" }

// Connect builds an Engine API client from the environment (DOCKER_HOST, TLS
// settings, etc.).
func (p *Provider) Connect(ctx context.Context, env string) error {
	c, err := client.NewClientWithOpts(client.FromEnv, client.WithAPIVersionNegotiation())
	if err != nil {
		return fmt.Errorf("docker client: %w", err)
	}
	p.cli = c
	p.host = c.DaemonHost()
	return nil
}

// Status verifies the daemon is reachable and returns its version (safe).
func (p *Provider) Status(ctx context.Context) (provider.Identity, error) {
	v, err := p.cli.ServerVersion(ctx)
	if err != nil {
		return provider.Identity{}, err
	}
	return provider.Identity{
		Provider: "docker",
		Account:  "engine " + v.Version,
		Endpoint: p.host,
		Source:   "docker-env",
	}, nil
}

// PS lists containers.
func (p *Provider) PS(ctx context.Context, all bool) (Result, error) {
	list, err := p.cli.ContainerList(ctx, container.ListOptions{All: all})
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(list))
	for _, c := range list {
		rows = append(rows, []string{
			shortID(c.ID), containerName(c.Names), c.Image,
			string(c.State), c.Status, humanizeAge(time.Unix(c.Created, 0)),
		})
	}
	return Result{Headers: []string{"ID", "NAME", "IMAGE", "STATE", "STATUS", "CREATED"}, Rows: rows, Items: list}, nil
}

// Inspect returns a detailed view of one container.
func (p *Provider) Inspect(ctx context.Context, id string) (string, any, error) {
	info, err := p.cli.ContainerInspect(ctx, id)
	if err != nil {
		return "", nil, err
	}
	var b strings.Builder
	fmt.Fprintf(&b, "ID:      %s\n", shortID(info.ID))
	fmt.Fprintf(&b, "Name:    %s\n", strings.TrimPrefix(info.Name, "/"))
	fmt.Fprintf(&b, "Image:   %s\n", info.Image)
	if info.State != nil {
		fmt.Fprintf(&b, "State:   %s (running=%t exitCode=%d)\n", info.State.Status, info.State.Running, info.State.ExitCode)
		if info.State.Error != "" {
			fmt.Fprintf(&b, "Error:   %s\n", info.State.Error)
		}
		if info.State.OOMKilled {
			b.WriteString("OOMKilled: true\n")
		}
		fmt.Fprintf(&b, "Started: %s\n", info.State.StartedAt)
		if info.State.FinishedAt != "" && info.State.FinishedAt != "0001-01-01T00:00:00Z" {
			fmt.Fprintf(&b, "Finished:%s\n", info.State.FinishedAt)
		}
	}
	fmt.Fprintf(&b, "Restarts:%d\n", info.RestartCount)
	return b.String(), info, nil
}

// Logs returns container logs as text. tail of "" means all lines.
func (p *Provider) Logs(ctx context.Context, id, tail string) (string, error) {
	if tail == "" {
		tail = "all"
	}
	rc, err := p.cli.ContainerLogs(ctx, id, container.LogsOptions{ShowStdout: true, ShowStderr: true, Tail: tail})
	if err != nil {
		return "", err
	}
	defer rc.Close()
	var buf strings.Builder
	// Engine logs are multiplexed for non-TTY containers; demultiplex to text.
	if _, err := stdcopy.StdCopy(&buf, &buf, rc); err != nil && err != io.EOF {
		return "", err
	}
	return buf.String(), nil
}

// Images lists local images.
func (p *Provider) Images(ctx context.Context) (Result, error) {
	imgs, err := p.cli.ImageList(ctx, image.ListOptions{})
	if err != nil {
		return Result{}, err
	}
	rows := make([][]string, 0, len(imgs))
	for _, img := range imgs {
		tag := "<none>"
		if len(img.RepoTags) > 0 {
			tag = img.RepoTags[0]
		}
		rows = append(rows, []string{shortID(img.ID), tag, humanizeBytes(img.Size), humanizeAge(time.Unix(img.Created, 0))})
	}
	return Result{Headers: []string{"ID", "REPO:TAG", "SIZE", "CREATED"}, Rows: rows, Items: imgs}, nil
}

func shortID(id string) string {
	id = strings.TrimPrefix(id, "sha256:")
	if len(id) > 12 {
		return id[:12]
	}
	return id
}

func containerName(names []string) string {
	if len(names) == 0 {
		return ""
	}
	return strings.TrimPrefix(names[0], "/")
}

func humanizeAge(t time.Time) string {
	if t.IsZero() || t.Unix() <= 0 {
		return "<unknown>"
	}
	d := time.Since(t)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}

func humanizeBytes(b int64) string {
	const unit = 1024
	if b < unit {
		return fmt.Sprintf("%dB", b)
	}
	div, exp := int64(unit), 0
	for n := b / unit; n >= unit; n /= unit {
		div *= unit
		exp++
	}
	return fmt.Sprintf("%.1f%cB", float64(b)/float64(div), "KMGTPE"[exp])
}
