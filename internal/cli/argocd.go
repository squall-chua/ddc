package cli

import (
	"github.com/spf13/cobra"

	"github.com/squallchua/ddc/internal/providers/argocd"
)

var (
	flagArgoServer   string
	flagArgoInsecure bool
)

func newArgoCDCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:     "argocd",
		Aliases: []string{"argo"},
		Short:   "Read-only Argo CD inspection",
	}
	cmd.PersistentFlags().StringVar(&flagArgoServer, "server", "", "Argo CD server address (or set ARGOCD_SERVER)")
	cmd.PersistentFlags().BoolVar(&flagArgoInsecure, "insecure", false, "skip TLS verification (self-signed servers)")
	cmd.AddCommand(newArgoAppsCmd(), newArgoAppCmd())
	return cmd
}

func connectArgoCD(cmd *cobra.Command) (*argocd.Provider, error) {
	p := argocd.New().(*argocd.Provider)
	p.Server = flagArgoServer
	p.Insecure = flagArgoInsecure
	if err := p.Connect(cmd.Context(), flagEnv); err != nil {
		return nil, err
	}
	return p, nil
}

func newArgoAppsCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "apps", Short: "Inspect applications"}
	list := &cobra.Command{
		Use:   "list",
		Short: "List applications with sync and health status",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			p, err := connectArgoCD(cmd)
			if err != nil {
				return err
			}
			res, err := p.ListApps(cmd.Context())
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}
	cmd.AddCommand(list)
	return cmd
}

func newArgoAppCmd() *cobra.Command {
	cmd := &cobra.Command{Use: "app", Short: "Inspect a single application"}

	get := &cobra.Command{
		Use:   "get <name>",
		Short: "Show application details (sync, health, source, destination)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectArgoCD(cmd)
			if err != nil {
				return err
			}
			text, obj, err := p.GetApp(cmd.Context(), args[0])
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

	resources := &cobra.Command{
		Use:   "resources <name>",
		Short: "List the application's managed resources and their health",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			p, err := connectArgoCD(cmd)
			if err != nil {
				return err
			}
			res, err := p.Resources(cmd.Context(), args[0])
			if err != nil {
				return err
			}
			return renderList(cmd, res.Headers, res.Rows, res.Items)
		},
	}

	cmd.AddCommand(get, resources)
	return cmd
}
