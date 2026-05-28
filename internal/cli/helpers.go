package cli

import (
	"fmt"
	"os"

	"github.com/spf13/cobra"

	"github.com/squallchua/ddc/internal/output"
	"github.com/squallchua/ddc/internal/providers/gha"
	"github.com/squallchua/ddc/internal/providers/k8s"
)

// flagRepo is the GitHub owner/repo target, shared by gha subcommands.
var flagRepo string

func newPrinter(cmd *cobra.Command) *output.Printer {
	return output.NewPrinter(cmd.OutOrStdout(), flagJSON)
}

// renderList prints a table or JSON, honoring --json. Both paths are redacted.
func renderList(cmd *cobra.Command, headers []string, rows [][]string, items any) error {
	p := newPrinter(cmd)
	if p.AsJSON() {
		return p.JSON(items)
	}
	if len(rows) == 0 {
		return p.Text("No resources found.")
	}
	return p.Text(output.Table(headers, rows))
}

func connectK8s(cmd *cobra.Command) (*k8s.Provider, error) {
	p := k8s.New().(*k8s.Provider)
	if err := p.Connect(cmd.Context(), flagEnv); err != nil {
		return nil, err
	}
	return p, nil
}

func connectGHA(cmd *cobra.Command) (*gha.Provider, error) {
	p := gha.New().(*gha.Provider)
	if err := p.Connect(cmd.Context(), flagEnv); err != nil {
		return nil, err
	}
	return p, nil
}

// resolveRepo determines the target repository from --repo or $GH_REPO.
func resolveRepo() (owner, repo string, err error) {
	r := flagRepo
	if r == "" {
		r = os.Getenv("GH_REPO")
	}
	if r == "" {
		return "", "", fmt.Errorf("no repository selected: pass --repo owner/repo or set GH_REPO")
	}
	return gha.SplitRepo(r)
}

func envLabel() string {
	if flagEnv == "" {
		return "default"
	}
	return flagEnv
}
