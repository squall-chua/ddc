package cli

import (
	"github.com/spf13/cobra"

	"github.com/squallchua/ddc/internal/providers/helm"
)

func newHelmCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "helm",
		Short: "Read-only Helm release inspection",
	}
	cmd.AddCommand(newHelmListCmd(), newHelmStatusCmd(), newHelmHistoryCmd(), newHelmValuesCmd())
	return cmd
}

func connectHelm(cmd *cobra.Command, namespace string, allNS bool) (*helm.Provider, error) {
	p := helm.New().(*helm.Provider)
	p.Namespace = namespace
	p.AllNamespaces = allNS
	if err := p.Connect(cmd.Context(), flagEnv); err != nil {
		return nil, err
	}
	return p, nil
}

func newHelmListCmd() *cobra.Command {
	var namespace string
	var allNS bool
	c := &cobra.Command{
		Use:   "list",
		Short: "List releases",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := connectHelm(cmd, namespace, allNS)
			if err != nil {
				return err
			}
			res, err := p.List(cmd.Context())
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	c.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace (default: current context namespace)")
	c.Flags().BoolVarP(&allNS, "all-namespaces", "A", false, "list releases across all namespaces")
	return c
}

func newHelmStatusCmd() *cobra.Command {
	var namespace string
	c := &cobra.Command{
		Use:   "status <release>",
		Short: "Show a release's status",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectHelm(cmd, namespace, false)
			if err != nil {
				return err
			}
			text, obj, err := p.ReleaseStatus(cmd.Context(), args[0])
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
	c.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace (default: current context namespace)")
	return c
}

func newHelmHistoryCmd() *cobra.Command {
	var namespace string
	c := &cobra.Command{
		Use:   "history <release>",
		Short: "Show a release's revision history",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectHelm(cmd, namespace, false)
			if err != nil {
				return err
			}
			res, err := p.History(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	c.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace (default: current context namespace)")
	return c
}

func newHelmValuesCmd() *cobra.Command {
	var namespace string
	c := &cobra.Command{
		Use:   "values <release>",
		Short: "Show a release's user-supplied values (redacted)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectHelm(cmd, namespace, false)
			if err != nil {
				return err
			}
			text, obj, err := p.Values(cmd.Context(), args[0])
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
	c.Flags().StringVarP(&namespace, "namespace", "n", "", "namespace (default: current context namespace)")
	return c
}
