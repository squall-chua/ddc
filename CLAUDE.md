# CLAUDE.md

This file provides guidance to Claude Code (claude.ai/code) when working with code in this repository.

## What this is

`ddc` is a read-only DevOps debugging CLI meant to be the **only** capability granted to an AI agent for inspecting clusters and pipelines. The security posture is the product — preserve it in every change.

## Commands

```bash
go build ./...                       # build everything
go build -o /tmp/ddc ./cmd/ddc       # build the binary
go test ./...                        # all tests
go test ./internal/redact/ -run TestScrubRedactsSecrets   # single package / test
go vet ./...
gofmt -l .                           # must print nothing (CI-equivalent format check)
```

The `go.mod` `go` directive is `1.26.0`; the Go toolchain auto-downloads 1.26.x if the local `go` is older. No Makefile or external lint config — `go vet` + `gofmt` are the gates.

## The security invariant (do not break)

These guarantees span multiple files; understand them before touching providers or output:

1. **No passthrough.** There is no `exec`/arbitrary-command relay. Every command maps to a specific typed read method. Never add one that shells out or forwards arbitrary input.
2. **Read endpoints only.** Providers call only read operations (k8s `list/get/watch`, REST `GET`, helm `NewList/Status/History/GetValues`, docker inspection calls). No create/update/delete/patch/install/upgrade/exec anywhere in `internal/providers/`. Audit: `grep -rnE "\.(Create|Update|Delete|Patch|Apply)\(" internal/providers/ | grep -v _test.go` should be empty.
3. **Secret confinement.** Raw credentials live only in `internal/credential`. `credential.Secret` returns `[REDACTED]` from `String`/`GoString`/`MarshalJSON`; `Reveal()` is the only accessor and its call sites must stay few and auditable. Never log or print a revealed secret.
4. **Non-bypassable redaction.** All command output flows through `output.Printer`, which runs `redact.Scrub` on both text and JSON. There is intentionally no `--no-redact` flag. Commands must render via the `Printer`, never write to stdout directly.
5. **Secret-bearing reads are blocked outright** (e.g. k8s `Secret` kind is refused before any API call), not merely redacted.

`redact` is tuned to catch known secret *shapes* (tokens, keys, JWTs, PEM, basic-auth) while leaving debugging signal intact (image digests, pod hashes). Don't add a blanket high-entropy redactor — it would scrub useful output.

## Architecture

- `cmd/ddc/main.go` → `internal/cli.Execute()` (signal-aware context, exit code).
- `internal/provider` — the `Provider` interface (`Name`/`Connect`/`Status` only) plus a name→constructor **registry**. Providers call `provider.Register(name, New)` in their package `init()`. Registration only happens because `internal/cli` imports each provider package (via the per-tool command files) — `auth status` enumerates the registry.
- `internal/providers/<tool>` — one package per tool (`k8s`, `gha`, `argocd`, `jenkins`, `docker`, `helm`). Each concrete `Provider` implements the interface AND exposes **typed read methods** (these are not on the interface — the CLI uses the concrete type). List-style methods return a local `Result{Headers, Rows, Items}`; detail-style methods return `(text string, obj any, err error)`.
- `internal/cli/<tool>.go` — cobra subcommands. They build the concrete provider with `<tool>.New().(*<tool>.Provider)`, set any connection inputs (e.g. `Server`, `Namespace`) from flags, call `Connect`, then call read methods. Shared helpers (`newPrinter`, `renderList`, `connect*`, repo/env resolution) live in `internal/cli/helpers.go`; global `--env`/`--json` and command registration in `root.go`.
- `internal/credential` — `Secret`, keychain (zalando/go-keyring), and `TokenSpec.Resolve()` (env vars → tool's own config fallback → keychain).
- `internal/output` — `Printer` (text/JSON, both redacted) and `Table`.

### Credential model

Reuse the user's existing sessions first, ddc-managed storage last. k8s/helm use the kubeconfig (`KUBECONFIG` honored via client-go default loading rules); gha/argocd/jenkins use `TokenSpec` (env → tool config → keychain). `ddc auth login` writes tokens to the keychain for token providers (`tokenProviders` map in `auth.go`) and prints guidance for kubeconfig/socket providers.

## Adding a provider

Follow the existing pattern exactly: new `internal/providers/<tool>` package with `init()` registration, the three interface methods, typed read-only methods returning `Result`/text, plus `internal/cli/<tool>.go` wired into `root.go`. Prefer a thin GET-only REST client over heavy official SDKs when the SDK drags in a large/fragile dependency tree (that's why `argocd` and `jenkins` are hand-rolled REST).

### Read-only testing

Tests assert read-only at the boundary, not by trust:
- REST providers (`gha`, `argocd`, `jenkins`): point the client at `httptest.NewServer` and assert every request method is `GET` (plus auth header present).
- `docker`: real client pointed at `httptest` via `WithHost("tcp://…")` + `WithHTTPClient`.
- `k8s`: `client-go` fake clientset; assert only `list/get/watch` verbs and that `Secret` is refused with zero API calls.
- `helm`: in-memory storage driver (`storage.Init(driver.NewMemory())`) + `kubefake.PrintingKubeClient`.

## Dependency gotcha

The `k8s.io/*` modules are pinned to **v0.35.1**, not latest. helm v3.21.0 requires v0.35.1, and k8s.io v0.36 removed `k8s.io/api/scheduling/v1alpha1`, which helm's transitive `k8s.io/kubectl` still imports. Bumping `client-go`/`apimachinery`/`api` above what helm pins breaks the build. Keep them aligned with helm's pin.
