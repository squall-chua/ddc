package cli

import (
	"fmt"
	"strings"

	"github.com/spf13/cobra"
)

func newK8sCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "k8s",
		Aliases: []string{"k", "kubernetes"},
		Short:   "Read-only Kubernetes inspection",
	}
	cmd.AddCommand(newK8sGetCmd(), newK8sDescribeCmd(), newK8sLogsCmd(), newK8sEventsCmd())
	return cmd
}

func newK8sGetCmd() *cobra.Command {
	var namespace string
	var allNamespaces bool
	var limit int64
	c := &cobra.Command{
		Use:   "get <kind>",
		Short: "List resources: pods, deployments, services, nodes, events",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectK8s(cmd)
			if err != nil {
				return err
			}
			res, err := p.Get(cmd.Context(), args[0], namespace, allNamespaces, limit)
			if err != nil {
				return err
			}
			if res.Truncated {
				noteTruncated(cmd, "results capped at --limit %d; raise --limit (0 = no limit) or narrow the query", limit)
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	c.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace")
	c.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "query all namespaces")
	c.Flags().Int64Var(&limit, "limit", 500, "max items to list (0 = no limit)")
	return c
}

func newK8sDescribeCmd() *cobra.Command {
	var namespace string
	c := &cobra.Command{
		Use:   "describe pod <name>",
		Short: "Describe a pod: status, container states, restarts, and events",
		Args:  cobra.ExactArgs(2),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch strings.ToLower(args[0]) {
			case "pod", "po", "pods":
			default:
				return fmt.Errorf("describe supports only 'pod' in v1, got %q", args[0])
			}
			p, err := connectK8s(cmd)
			if err != nil {
				return err
			}
			text, obj, err := p.DescribePod(cmd.Context(), namespace, args[1])
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
	c.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace")
	return c
}

func newK8sLogsCmd() *cobra.Command {
	var namespace, container string
	var previous bool
	var tail, limit int64
	c := &cobra.Command{
		Use:   "logs <pod>",
		Short: "Print container logs (redacted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectK8s(cmd)
			if err != nil {
				return err
			}
			logs, err := p.Logs(cmd.Context(), namespace, args[0], container, previous, tail, limit)
			if err != nil {
				return err
			}
			if limit > 0 && int64(len(logs)) >= limit {
				noteTruncated(cmd, "log capped at --limit %d bytes; raise --limit (0 = no limit) or use --tail", limit)
			}
			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(map[string]string{"pod": args[0], "logs": logs})
			}
			return pr.Text(logs)
		},
	}
	c.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace")
	c.Flags().StringVarP(&container, "container", "c", "", "container name")
	c.Flags().BoolVar(&previous, "previous", false, "logs from the previous container instance (crash debugging)")
	c.Flags().Int64Var(&tail, "tail", 0, "lines from the end of the log (0 = all)")
	c.Flags().Int64Var(&limit, "limit", 1<<20, "max bytes to return (0 = no limit)")
	return c
}

func newK8sEventsCmd() *cobra.Command {
	var namespace string
	var allNamespaces bool
	var limit int64
	c := &cobra.Command{
		Use:   "events",
		Short: "List cluster events, oldest first",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := connectK8s(cmd)
			if err != nil {
				return err
			}
			res, err := p.Get(cmd.Context(), "events", namespace, allNamespaces, limit)
			if err != nil {
				return err
			}
			if res.Truncated {
				noteTruncated(cmd, "results capped at --limit %d; raise --limit (0 = no limit) or narrow the query", limit)
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	c.Flags().StringVarP(&namespace, "namespace", "n", "default", "namespace")
	c.Flags().BoolVarP(&allNamespaces, "all-namespaces", "A", false, "query all namespaces")
	c.Flags().Int64Var(&limit, "limit", 500, "max events to list (0 = no limit)")
	return c
}
