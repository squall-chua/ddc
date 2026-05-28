package cli

import (
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

	logs := &cobra.Command{
		Use:   "logs <job> [number]",
		Short: "Print build console output (redacted; defaults to last build)",
		Args:  cobra.RangeArgs(1, 2),
		RunE: func(cmd *cobra.Command, args []string) error {
			number := ""
			if len(args) == 2 {
				number = args[1]
			}
			p, err := connectJenkins(cmd)
			if err != nil {
				return err
			}
			out, err := p.BuildLogs(cmd.Context(), args[0], number)
			if err != nil {
				return err
			}
			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(map[string]string{"job": args[0], "logs": out})
			}
			return pr.Text(out)
		},
	}

	cmd.AddCommand(view, logs)
	return cmd
}
