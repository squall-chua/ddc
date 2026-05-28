// Command ddc is a read-only DevOps debugging CLI designed to be the only
// capability granted to an AI agent: it talks to each tool's API using read
// endpoints only, never handles raw secrets, and redacts sensitive output.
package main

import (
	"os"

	"github.com/squall-chua/ddc/internal/cli"
)

func main() {
	os.Exit(cli.Execute())
}
