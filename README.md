# ddc — DevOps Debug CLI

A **read-only** DevOps debugging CLI designed to be the *only* capability you hand
to an AI agent. `ddc` lets an agent investigate why a pod is crashlooping or a
pipeline failed — without being able to perform destructive actions, trigger
deploys, or read your secrets.

It talks to each tool's API using **read endpoints only** (mutation isn't merely
blocked — it's absent from the binary), **never handles your raw secrets** (it
borrows your existing local sessions or an OS keychain entry), and **redacts
secret-looking values** from everything it prints.

## Why

Handing an AI agent raw `kubectl`, `gh`, `helm`, or `docker` means it can mutate
clusters, re-run pipelines, and `cat` credential files. `ddc` gives the agent a
narrow, safe surface for *diagnosis* so it can be genuinely useful during
incidents and debugging while staying inside hard guardrails.

## Target use cases

- Let an AI agent triage live issues: crashlooping pods, failed rollouts, broken
  CI runs, OutOfSync/Degraded GitOps apps, failing Jenkins builds, exited
  containers.
- Give humans a single read-only entry point across mixed tooling.
- Run an agent against production for **read-only** investigation with confidence
  that it cannot change state or exfiltrate secrets through the tool.

## Supported tools

| Area | Provider | Commands |
| --- | --- | --- |
| Kubernetes | `k8s` | `get <pods\|deployments\|services\|nodes\|events>`, `describe pod <name>`, `logs <pod>`, `events` |
| GitHub Actions | `gha` | `runs list`, `run view <id>`, `run logs <id>`, `workflows list` |
| Argo CD | `argocd` | `apps list`, `app get <name>`, `app resources <name>` |
| Jenkins | `jenkins` | `jobs list`, `build view <job> <n>`, `build logs <job> [n]` |
| Docker | `docker` | `ps`, `inspect <container>`, `logs <container>`, `images` |
| Helm | `helm` | `list`, `status <release>`, `history <release>`, `values <release>` |

Kubernetes `Secret` objects are refused outright — `ddc k8s get secret` is blocked
by design, never just redacted.

## Installation

Requires Go 1.26+ (the toolchain auto-downloads if your local Go is older).

```bash
git clone <repo-url> ddc
cd ddc
go build -o ddc ./cmd/ddc
# move ./ddc onto your PATH, e.g.
install ddc ~/.local/bin/ddc
```

Optionally stamp version info at build time:

```bash
go build -ldflags "-X github.com/squall-chua/ddc/internal/cli.version=$(git describe --tags --always)" -o ddc ./cmd/ddc
```

## Quick start

Pre-authenticate the underlying tools yourself (this is a human step — see
[docs/pre-auth-guide.md](docs/pre-auth-guide.md)), then check what `ddc` can reach
(prints safe identity only, never secrets):

```bash
ddc auth status
```

Then debug:

```bash
# Kubernetes: why is this pod unhealthy?
ddc k8s get pods -n prod
ddc k8s describe pod web-7c5ddbdf54-abcde -n prod
ddc k8s logs web-7c5ddbdf54-abcde -n prod --previous

# GitHub Actions: what broke the pipeline?
ddc gha runs list --repo myorg/myrepo --status failure
ddc gha run view 123456 --repo myorg/myrepo
ddc gha run logs 123456 --repo myorg/myrepo
```

Global flags: `--env <context/name>` targets a specific environment (e.g. a kube
context), and `--json` emits machine-readable output. Both still pass through
redaction.

## Using ddc with your own AI agent

The whole point is that `ddc` becomes the agent's **single sanctioned capability**.

1. **Pre-authenticate (human, once).** You authenticate the underlying tools; the
   agent never sees a secret. See [docs/pre-auth-guide.md](docs/pre-auth-guide.md).
   Prefer read-only upstream credentials (a read-only kube context, a fine-grained
   read GitHub token, a read-only Argo CD/Jenkins account) for defense in depth.

2. **Restrict the agent to `ddc`.** Allow it to run only the `ddc` binary. Do **not**
   also grant it raw `kubectl`/`gh`/`docker`/`curl` or unrestricted file reads — an
   agent that can `cat ~/.kube/config` doesn't need `ddc`, and those paths bypass
   every guarantee `ddc` provides. Treat `ddc auth login/logout` as human-only.

3. **Give the agent the usage instructions.** This repo ships a ready-made
   **Claude Code skill** at
   [skills/ddc-devops-debugging/SKILL.md](skills/ddc-devops-debugging/SKILL.md) with
   the command reference, debugging playbooks, and the safety contract. Install it
   by making that directory available to Claude Code (e.g. copy it into your skills
   directory). For other agents, feed the same `SKILL.md` content as system/tool
   instructions.

With that in place, the agent runs commands like `ddc k8s describe pod …` and
`ddc gha run logs …`, reasons over redacted read-only output, and — when a fix
requires a change — tells *you* what to do rather than doing it.

## Authentication summary

`ddc` resolves credentials per provider in this order, stopping at the first hit,
and the raw secret never leaves the tool:

1. Environment variables (`GH_TOKEN`, `ARGOCD_AUTH_TOKEN`, `JENKINS_TOKEN`, …)
2. The tool's own config (kubeconfig/`KUBECONFIG`, the `gh` CLI config, the
   `argocd` CLI config, `DOCKER_HOST`)
3. The OS keychain, populated by `ddc auth login <provider>`

Helm and Kubernetes share the kubeconfig; Docker uses your Docker environment.
Full details and per-provider setup are in
[docs/pre-auth-guide.md](docs/pre-auth-guide.md).

## What ddc does *not* do

- It cannot create, update, delete, scale, roll back, re-run, or exec anything.
- It will not authenticate for you or accept secrets pasted at runtime.
- It is not a replacement for the real tools when you actually need to *change*
  something — it is a safe lens for *reading* state.

## Safety guarantees (how it holds)

- **No passthrough.** No `exec` or arbitrary-command relay; every command maps to a
  specific read call.
- **Read endpoints only.** Kubernetes `list/get/watch`, REST `GET`, Helm read
  actions, Docker inspection calls — no write paths exist in the codebase.
- **Secret confinement.** Raw credentials live in one package and are never logged
  or printed.
- **Non-bypassable redaction.** Every byte of output is scrubbed; there is no flag
  to disable it.
