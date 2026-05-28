package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/squallchua/ddc/internal/credential"
	"github.com/squallchua/ddc/internal/output"
	"github.com/squallchua/ddc/internal/provider"
)

const k8sLoginHelp = `Kubernetes uses your existing kubeconfig — ddc stores nothing.
Authenticate with your cluster provider, for example:
  aws eks update-kubeconfig --name <cluster>
  gcloud container clusters get-credentials <cluster>
Then select a context per command with --env <context>.`

func newAuthCmd() *cobra.Command {
	cmd := &cobra.Command{
		Use:   "auth",
		Short: "Inspect and pre-authenticate providers (never prints secrets)",
	}
	cmd.AddCommand(newAuthStatusCmd(), newAuthLoginCmd(), newAuthLogoutCmd())
	return cmd
}

type authStatus struct {
	Provider string `json:"provider"`
	Account  string `json:"account,omitempty"`
	Endpoint string `json:"endpoint,omitempty"`
	Source   string `json:"source,omitempty"`
	Status   string `json:"status"`
}

func newAuthStatusCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "status",
		Short: "Show which providers are reachable (safe identity only, no secrets)",
		Args:  cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			var items []authStatus
			for _, name := range provider.Names() {
				p, err := provider.New(name)
				if err != nil {
					continue
				}
				st := authStatus{Provider: name}
				if err := p.Connect(cmd.Context(), flagEnv); err != nil {
					st.Status = "not configured"
				} else if id, err := p.Status(cmd.Context()); err != nil {
					st.Status = "error: " + firstLine(err.Error())
				} else {
					st.Account, st.Endpoint, st.Source, st.Status = id.Account, id.Endpoint, id.Source, "ok"
				}
				items = append(items, st)
			}

			pr := newPrinter(cmd)
			if pr.AsJSON() {
				return pr.JSON(items)
			}
			rows := make([][]string, 0, len(items))
			for _, it := range items {
				rows = append(rows, []string{it.Provider, it.Account, it.Endpoint, it.Source, it.Status})
			}
			return pr.Text(output.Table([]string{"PROVIDER", "ACCOUNT", "ENDPOINT", "SOURCE", "STATUS"}, rows))
		},
	}
}

func newAuthLoginCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "login <provider>",
		Short: "Pre-authenticate a provider (interactive; intended for humans, not agents)",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			switch args[0] {
			case "gha":
				fmt.Fprint(cmd.ErrOrStderr(), "Paste a GitHub token (input hidden): ")
				raw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.ErrOrStderr())
				if err != nil {
					return fmt.Errorf("read token: %w", err)
				}
				tok := strings.TrimSpace(string(raw))
				if tok == "" {
					return fmt.Errorf("no token entered")
				}
				if err := credential.KeychainSet("gha", flagEnv, credential.NewSecret(tok)); err != nil {
					return fmt.Errorf("store token: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Stored GitHub token in OS keychain for env %q.\n", envLabel())
				return nil
			case "k8s":
				fmt.Fprintln(cmd.OutOrStdout(), k8sLoginHelp)
				return nil
			default:
				return fmt.Errorf("unknown provider %q (known: %s)", args[0], strings.Join(provider.Names(), ", "))
			}
		},
	}
}

func newAuthLogoutCmd() *cobra.Command {
	return &cobra.Command{
		Use:   "logout <provider>",
		Short: "Remove a stored keychain credential",
		Args:  cobra.ExactArgs(1),
		RunE: func(cmd *cobra.Command, args []string) error {
			if err := credential.KeychainDelete(args[0], flagEnv); err != nil {
				return fmt.Errorf("remove credential: %w", err)
			}
			fmt.Fprintf(cmd.OutOrStdout(), "Removed keychain credential for %q (env %q).\n", args[0], envLabel())
			return nil
		},
	}
}

func firstLine(s string) string {
	if i := strings.IndexByte(s, '\n'); i >= 0 {
		return s[:i]
	}
	return s
}
