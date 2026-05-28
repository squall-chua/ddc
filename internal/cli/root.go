// Package cli wires the ddc command tree. All commands map to specific,
// typed read operations on a provider — there is deliberately no passthrough
// or "execute" command, so destructive actions cannot be expressed.
package cli

import (
	"context"
	"fmt"
	"os"
	"os/signal"
	"syscall"

	"github.com/spf13/cobra"
)

// Build-time values, injected via -ldflags. See docs/build for details.
var (
	version = "dev"
	commit  = "none"
	date    = "unknown"
)

// Shared global flags. All ddc commands live in this package, so package-level
// flag state is the simplest way to share them between subcommands.
var (
	flagEnv  string
	flagJSON bool
)

func newRootCmd() *cobra.Command {
	root := &cobra.Command{
		Use:   "ddc",
		Short: "Read-only DevOps debugging CLI, safe for AI agents",
		Long: `ddc is a read-only DevOps debugging CLI.

It talks to each tool's API using read endpoints only, so destructive actions
are absent from the binary, never handles raw secrets (it borrows your existing
local sessions or an OS keychain entry), and redacts sensitive values from all
output. It is designed to be the ONLY capability you grant an AI agent for
inspecting clusters and pipelines.`,
		SilenceUsage:  true,
		SilenceErrors: true,
		Version:       version,
	}

	root.PersistentFlags().StringVar(&flagEnv, "env", "", "named environment/context to target (provider-specific)")
	root.PersistentFlags().BoolVar(&flagJSON, "json", false, "emit machine-readable JSON instead of text")

	root.AddCommand(newVersionCmd())
	root.AddCommand(newAuthCmd())
	root.AddCommand(newK8sCmd())
	root.AddCommand(newGHACmd())

	return root
}

// Execute runs the root command and returns a process exit code.
func Execute() int {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()
	if err := newRootCmd().ExecuteContext(ctx); err != nil {
		fmt.Fprintln(os.Stderr, "ddc: "+err.Error())
		return 1
	}
	return 0
}
