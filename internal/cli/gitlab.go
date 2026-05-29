package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/squall-chua/ddc/internal/providers/gitlab"
)

var (
	flagGitlabHost string
	flagProject    string
)

func newGitlabCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "gitlab",
		Aliases: []string{"gl"},
		Short:   "Read-only GitLab CI inspection",
	}
	cmd.PersistentFlags().StringVar(&flagGitlabHost, "host", "", "GitLab base URL (default https://gitlab.com, or set GITLAB_HOST)")
	cmd.PersistentFlags().StringVar(&flagProject, "project", "", "project id or path group/project (or set GITLAB_PROJECT)")
	cmd.AddCommand(newGitlabPipelinesCmd(), newGitlabPipelineCmd(), newGitlabJobCmd())
	return cmd
}

func connectGitlab(cmd *cobra.Command) (*gitlab.Provider, error) {
	p := gitlab.New().(*gitlab.Provider)
	p.Host = flagGitlabHost
	if err := p.Connect(cmd.Context(), flagEnv); err != nil {
		return nil, err
	}
	return p, nil
}

// resolveProject determines the target project from --project, $GITLAB_PROJECT,
// or the CI-injected $CI_PROJECT_PATH.
func resolveProject() (string, error) {
	pr := flagProject
	if pr == "" {
		pr = os.Getenv("GITLAB_PROJECT")
	}
	if pr == "" {
		pr = os.Getenv("CI_PROJECT_PATH")
	}
	if pr == "" {
		return "", fmt.Errorf("no project selected: pass --project <id|group/project> or set GITLAB_PROJECT")
	}
	return pr, nil
}

func newGitlabPipelinesCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pipelines", Short: "Inspect CI pipelines"}

	var ref, status string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List recent pipelines",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			project, err := resolveProject()
			if err != nil {
				return err
			}
			p, err := connectGitlab(cmd)
			if err != nil {
				return err
			}
			res, err := p.ListPipelines(cmd.Context(), project, ref, status, limit)
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	list.Flags().StringVar(&ref, "ref", "", "filter by branch/tag")
	list.Flags().StringVar(&status, "status", "", "filter by status (e.g. failed, success, running)")
	list.Flags().IntVar(&limit, "limit", 20, "max pipelines to fetch")

	cmd.AddCommand(list)
	return cmd
}

func newGitlabPipelineCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "pipeline", Short: "Inspect a single pipeline"}

	view := &cobra.Command{
		Use:   "view <pipeline-id>",
		Short: "Show a pipeline's jobs and which failed",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			project, err := resolveProject()
			if err != nil {
				return err
			}
			p, err := connectGitlab(cmd)
			if err != nil {
				return err
			}
			text, obj, err := p.ViewPipeline(cmd.Context(), project, id)
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

	cmd.AddCommand(view)
	return cmd
}

func newGitlabJobCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "job", Short: "Inspect a single job"}

	var limit int64
	logs := &cobra.Command{
		Use:   "logs <job-id>",
		Short: "Print a job's trace/console log (redacted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			id, err := parseID(args[0])
			if err != nil {
				return err
			}
			project, err := resolveProject()
			if err != nil {
				return err
			}
			p, err := connectGitlab(cmd)
			if err != nil {
				return err
			}
			out, err := p.JobLogs(cmd.Context(), project, id, limit)
			if err != nil {
				return err
			}
			if limit > 0 && int64(len(out)) >= limit {
				noteTruncated(cmd, "log capped at --limit %d bytes; raise --limit (0 = no limit)", limit)
			}
			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(map[string]any{"job_id": id, "logs": out})
			}
			return pr.Text(out)
		},
	}
	logs.Flags().Int64Var(&limit, "limit", 1<<20, "max bytes to return (0 = no limit)")

	cmd.AddCommand(logs)
	return cmd
}
