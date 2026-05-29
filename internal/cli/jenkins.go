package cli

import (
	"fmt"

	"github.com/spf13/cobra"

	"github.com/squall-chua/ddc/internal/providers/jenkins"
)

var flagJenkinsURL string

func newJenkinsCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "jenkins",
		Short: "Read-only Jenkins inspection",
	}
	cmd.PersistentFlags().StringVar(&flagJenkinsURL, "url", "", "Jenkins base URL (or set JENKINS_URL)")
	cmd.AddCommand(newJenkinsJobsCmd(), newJenkinsBuildCmd())
	return cmd
}

func connectJenkins(cmd *cobra.Command) (*jenkins.Provider, error) {
	p := jenkins.New().(*jenkins.Provider)
	p.URL = flagJenkinsURL
	if err := p.Connect(cmd.Context(), flagEnv); err != nil {
		return nil, err
	}
	return p, nil
}

func newJenkinsJobsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "jobs", Short: "Inspect jobs"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List jobs and their last-build status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := connectJenkins(cmd)
			if err != nil {
				return err
			}
			res, err := p.ListJobs(cmd.Context())
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	cmd.AddCommand(list)
	return cmd
}

func newJenkinsBuildCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "build", Short: "Inspect a build"}

	view := &cobra.Command{
		Use:   "view <job> <number>",
		Short: "Show build result, duration, and timing",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectJenkins(cmd)
			if err != nil {
				return err
			}
			text, obj, err := p.ViewBuild(cmd.Context(), args[0], args[1])
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

	var skip, limit int64
	logs := &cobra.Command{
		Use:   "logs <job> [number]",
		Short: "Print build console output (redacted; defaults to last build)",
		Long: `Print a build's console output.

Logs can be huge, so output is windowed: --skip bytes are skipped from the start
and at most --limit bytes are returned (use --limit 0 for the whole log). When
more output remains, the next --skip offset is reported so you can page through.`,
		Args: cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			number := ""
			if len(args) == 2 {
				number = args[1]
			}
			p, err := connectJenkins(cmd)
			if err != nil {
				return err
			}
			out, next, more, err := p.BuildLogs(cmd.Context(), args[0], number, skip, limit)
			if err != nil {
				return err
			}
			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(map[string]any{
					"job": args[0], "logs": out, "next_offset": next, "more": more,
				})
			}
			if more {
				out += fmt.Sprintf("\n[truncated — more output available; continue with --skip %d]", next)
			}
			return pr.Text(out)
		},
	}
	logs.Flags().Int64Var(&skip, "skip", 0, "byte offset to start from (for paging)")
	logs.Flags().Int64Var(&limit, "limit", 1<<20, "max bytes to return (0 = no limit)")

	cmd.AddCommand(view, logs)
	return cmd
}
