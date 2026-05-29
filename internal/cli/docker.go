package cli

import (
	"strconv"

	"github.com/spf13/cobra"

	"github.com/squall-chua/ddc/internal/providers/docker"
)

func newDockerCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "docker",
		Short: "Read-only Docker (Engine API) inspection",
	}
	cmd.AddCommand(newDockerPSCmd(), newDockerInspectCmd(), newDockerLogsCmd(), newDockerImagesCmd())
	return cmd
}

func connectDocker(cmd *cobra.Command) (*docker.Provider, error) {
	p := docker.New().(*docker.Provider)
	if err := p.Connect(cmd.Context(), flagEnv); err != nil {
		return nil, err
	}
	return p, nil
}

func newDockerPSCmd() *cobra.Command {
	var all bool
	c := &cobra.Command{
		Use:   "ps",
		Short: "List containers",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := connectDocker(cmd)
			if err != nil {
				return err
			}
			res, err := p.PS(cmd.Context(), all)
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	c.Flags().BoolVarP(&all, "all", "a", false, "include stopped containers")
	return c
}

func newDockerInspectCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "inspect <container>",
		Short: "Inspect a container's state and exit details",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectDocker(cmd)
			if err != nil {
				return err
			}
			text, obj, err := p.Inspect(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(obj)
			}
			return pr.Text(text)
		},
	}
}

func newDockerLogsCmd() *cobra.Command {
	var tail int
	var limit int64
	c := &cobra.Command{
		Use:   "logs <container>",
		Short: "Print container logs (redacted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectDocker(cmd)
			if err != nil {
				return err
			}
			tailArg := ""
			if tail > 0 {
				tailArg = strconv.Itoa(tail)
			}
			out, err := p.Logs(cmd.Context(), args[0], tailArg, limit)
			if err != nil {
				return err
			}
			if limit > 0 && int64(len(out)) >= limit {
				noteTruncated(cmd, "log capped at --limit %d bytes; raise --limit (0 = no limit) or use --tail", limit)
			}
			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(map[string]string{"container": args[0], "logs": out})
			}
			return pr.Text(out)
		},
	}
	c.Flags().IntVar(&tail, "tail", 0, "lines from the end of the log (0 = all)")
	c.Flags().Int64Var(&limit, "limit", 1<<20, "max bytes to return (0 = no limit)")
	return c
}

func newDockerImagesCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "images",
		Short: "List local images",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := connectDocker(cmd)
			if err != nil {
				return err
			}
			res, err := p.Images(cmd.Context())
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
}
