package cli

import (
	"fmt"
	"strconv"

	"github.com/spf13/cobra"
)

func newGHACmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "gha",
		Aliases: []string{"github-actions"},
		Short:   "Read-only GitHub Actions inspection",
	}
	cmd.PersistentFlags().StringVar(&flagRepo, "repo", "", "target repository as owner/repo (or set GH_REPO)")
	cmd.AddCommand(newGHARunsCmd(), newGHARunCmd(), newGHAWorkflowsCmd())
	return cmd
}

func newGHARunsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "runs", Short: "Inspect workflow runs"}

	var workflow, branch, status string
	var limit int
	list := &cobra.Command{
		Use:   "list",
		Short: "List recent workflow runs",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			owner, repo, err := resolveRepo()
			if err != nil {
				return err
			}
			p, err := connectGHA(cmd)
			if err != nil {
				return err
			}
			res, err := p.ListRuns(cmd.Context(), owner, repo, workflow, branch, status, limit)
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	list.Flags().StringVar(&workflow, "workflow", "", "filter by workflow name")
	list.Flags().StringVar(&branch, "branch", "", "filter by branch")
	list.Flags().StringVar(&status, "status", "", "filter by status/conclusion (e.g. failure, completed)")
	list.Flags().IntVar(&limit, "limit", 20, "max runs to fetch")

	cmd.AddCommand(list)
	return cmd
}

func newGHARunCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "run", Short: "Inspect a single workflow run"}

	view := &cobra.Command{
		Use:   "view <run-id>",
		Short: "Show a run's jobs and failed steps",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID, err := parseID(args[0])
			if err != nil {
				return err
			}
			owner, repo, err := resolveRepo()
			if err != nil {
				return err
			}
			p, err := connectGHA(cmd)
			if err != nil {
				return err
			}
			text, obj, err := p.ViewRun(cmd.Context(), owner, repo, runID)
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

	var job, limit int64
	logs := &cobra.Command{
		Use:   "logs <run-id>",
		Short: "Print logs for a job in the run (defaults to the first failed job)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			runID, err := parseID(args[0])
			if err != nil {
				return err
			}
			owner, repo, err := resolveRepo()
			if err != nil {
				return err
			}
			p, err := connectGHA(cmd)
			if err != nil {
				return err
			}
			out, err := p.RunLogs(cmd.Context(), owner, repo, runID, job, limit)
			if err != nil {
				return err
			}
			if limit > 0 && int64(len(out)) >= limit {
				noteTruncated(cmd, "log capped at --limit %d bytes; raise --limit (0 = no limit)", limit)
			}
			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(map[string]any{"run_id": runID, "logs": out})
			}
			return pr.Text(out)
		},
	}
	logs.Flags().Int64Var(&job, "job", 0, "job id (default: first failed job)")
	logs.Flags().Int64Var(&limit, "limit", 1<<20, "max bytes to return (0 = no limit)")

	cmd.AddCommand(view, logs)
	return cmd
}

func newGHAWorkflowsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "workflows", Short: "Inspect workflows"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List workflows defined in the repository",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			owner, repo, err := resolveRepo()
			if err != nil {
				return err
			}
			p, err := connectGHA(cmd)
			if err != nil {
				return err
			}
			res, err := p.ListWorkflows(cmd.Context(), owner, repo)
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	cmd.AddCommand(list)
	return cmd
}

func parseID(s string) (int64, error) {
	id, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, fmt.Errorf("invalid run id %q: must be a number", s)
	}
	return id, nil
}
