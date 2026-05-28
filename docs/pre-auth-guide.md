# Pre-authenticating `ddc` (for humans)

`ddc` is designed so an AI agent can debug your clusters and pipelines **without
ever seeing a secret or being able to change anything**. To make that true, *you*
authenticate the underlying tools once; the agent only ever runs `ddc`.

This is a human task. Do not ask the agent to do it — and never paste secrets into
a chat with the agent.

## How credentials are resolved

For each provider, `ddc` looks for credentials in this order and stops at the
first hit. It borrows your existing sessions before anything it stores itself, and
the raw secret never leaves `ddc` (it is never printed or logged):

1. **Environment variables**
2. **The tool's own config** (kubeconfig, the `gh` CLI's config)
3. **The OS keychain**, populated by `ddc auth login`

Check what is reachable at any time (safe identity only, no secrets):

```
ddc auth status
```

## Kubernetes

`ddc` reuses your kubeconfig — it stores nothing. Authenticate to your cluster the
usual way:

```
aws eks update-kubeconfig --name <cluster>
# or
gcloud container clusters get-credentials <cluster>
```

By default `ddc` reads `~/.kube/config`. To use a different file (or several), set
the standard `KUBECONFIG` environment variable — `ddc` honors it, including
colon-separated lists of paths:

```
export KUBECONFIG=/path/to/config
# or merge several:
export KUBECONFIG=/path/to/a.yaml:/path/to/b.yaml
```

Then target a specific context per command with `--env <context>`.

**Recommended (defense in depth):** point `ddc` at a **read-only** context — a
kubeconfig whose ServiceAccount/role only has `get`/`list`/`watch`. Then even a
bug could not mutate the cluster, and the cluster's own RBAC keeps Secrets out of
reach on top of `ddc`'s built-in Secret block.

## GitHub Actions

Pick one:

- **Environment variable:** export a token (prefer a fine-grained, read-only
  token with Actions: Read and Contents: Read):

  ```
  export GH_TOKEN=<your-read-only-token>
  ```

- **Reuse the `gh` CLI:** if you already use GitHub CLI, `gh auth login` is
  enough — `ddc` reads its stored token.

- **Store in the OS keychain** (interactive; token entry is hidden):

  ```
  ddc auth login gha
  ```

Select the repository per command with `--repo owner/repo`, or set `GH_REPO`.

To remove a keychain-stored credential:

```
ddc auth logout gha
```

## Keeping the agent inside the guardrails

`ddc`'s guarantees hold only if `ddc` is the agent's **single** capability for
this work. When configuring the agent:

- Allow it to run **only** `ddc`. Do not also grant it raw `kubectl`, `gh`,
  `docker`, `curl`, or unrestricted file reads — those bypass every guarantee
  `ddc` provides (an agent that can `cat ~/.kube/config` doesn't need `ddc`).
- Treat `ddc auth login/logout` as human-only.

## Building `ddc`

```
go build -o ddc ./cmd/ddc
```

Install the agent skill by making `skills/ddc-devops-debugging/` available to your
agent (e.g. copy it into your Claude Code skills directory).
