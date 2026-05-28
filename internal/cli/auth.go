package cli

import (
	"fmt"
	"os"
	"strings"

	"github.com/spf13/cobra"
	"golang.org/x/term"

	"github.com/squall-chua/ddc/internal/credential"
	"github.com/squall-chua/ddc/internal/output"
	"github.com/squall-chua/ddc/internal/provider"
)

const kubeLoginHelp = `This provider uses your existing kubeconfig — ddc stores nothing.
Authenticate with your cluster provider, for example:
  aws eks update-kubeconfig --name <cluster>
  gcloud container clusters get-credentials <cluster>
Then select a context per command with --env <context>.`

const dockerLoginHelp = `Docker uses your local Docker environment — ddc stores nothing.
Ensure the daemon is running and DOCKER_HOST points at it.`

// tokenProviders can have a token stored in the OS keychain via `ddc auth login`.
var tokenProviders = map[string]string{
	"gha":     "GitHub",
	"jenkins": "Jenkins API",
	"argocd":  "Argo CD",
}

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
			name := args[0]
			if label, ok := tokenProviders[name]; ok {
				fmt.Fprintf(cmd.ErrOrStderr(), "Paste a %s token (input hidden): ", label)
				raw, err := term.ReadPassword(int(os.Stdin.Fd()))
				fmt.Fprintln(cmd.ErrOrStderr())
				if err != nil {
					return fmt.Errorf("read token: %w", err)
				}
				tok := strings.TrimSpace(string(raw))
				if tok == "" {
					return fmt.Errorf("no token entered")
				}
				if err := credential.KeychainSet(name, flagEnv, credential.NewSecret(tok)); err != nil {
					return fmt.Errorf("store token: %w", err)
				}
				fmt.Fprintf(cmd.OutOrStdout(), "Stored %s token in OS keychain for env %q.\n", label, envLabel())
				return nil
			}
			switch name {
			case "k8s", "helm":
				fmt.Fprintln(cmd.OutOrStdout(), kubeLoginHelp)
				return nil
			case "docker":
				fmt.Fprintln(cmd.OutOrStdout(), dockerLoginHelp)
				return nil
			default:
				return fmt.Errorf("unknown provider %q (known: %s)", name, strings.Join(provider.Names(), ", "))
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
