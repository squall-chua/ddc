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
usual way for your platform; each command writes/updates a context in your
kubeconfig:

```
# Amazon EKS
aws eks update-kubeconfig --name <cluster> --region <region>

# Google GKE
gcloud container clusters get-credentials <cluster> --region <region>

# Azure AKS
az aks get-credentials --resource-group <rg> --name <cluster>

# kind / minikube write a context automatically when the cluster is created.
# On-prem / generic: obtain a kubeconfig file from your cluster administrator.
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

**Recommended (defense in depth):** point `ddc` at a **read-only** context. To
mint a dedicated read-only credential, create a ServiceAccount bound to the
built-in `view` ClusterRole (which already excludes Secrets) and issue it a token:

```bash
kubectl create serviceaccount ddc-readonly -n kube-system
kubectl create clusterrolebinding ddc-readonly \
  --clusterrole=view --serviceaccount=kube-system:ddc-readonly

# Kubernetes 1.24+: print a short-lived token for that ServiceAccount
kubectl create token ddc-readonly -n kube-system --duration=24h
```

Wire that token into a kubeconfig context (`kubectl config set-credentials
ddc-readonly --token=<token>`, then `set-context`/`use-context`) and point `ddc`
at it. Even a bug then cannot mutate the cluster, and RBAC keeps Secrets out of
reach on top of `ddc`'s built-in Secret block.

## GitHub Actions

### Create a token (recommended: fine-grained PAT)

On github.com: **avatar → Settings → Developer settings** (bottom of the left
sidebar) **→ Personal access tokens → Fine-grained tokens → Generate new token**.

1. Set **Resource owner** to the account/org that owns the repo, and an expiry.
2. **Repository access →** select only the repositories you want `ddc` to read.
3. **Repository permissions →** grant read-only:
   - **Actions: Read-only** (workflow runs, jobs, logs)
   - **Contents: Read-only** (list workflows)
   - **Metadata: Read-only** (auto-selected)
4. **Generate token** and copy it (shown once).

Classic token alternative: **Developer settings → Tokens (classic) → Generate new
token**. Classic tokens are coarse — for private repos the only read scope is
`repo` (which also grants write), so prefer fine-grained. For public repos no
scope is needed beyond `public_repo`.

### Give the token to ddc — pick one

- **Environment variable:**

  ```
  export GH_TOKEN=<your-read-only-token>
  ```

- **Reuse the `gh` CLI:** `gh auth login` → choose **GitHub.com → HTTPS →**
  authenticate via browser or paste a token. `ddc` then reads the stored token
  from `~/.config/gh/hosts.yml`.

- **Store in the OS keychain** (interactive; token entry is hidden):

  ```
  ddc auth login gha
  ```

Select the repository per command with `--repo owner/repo`, or set `GH_REPO`.

To remove a keychain-stored credential:

```
ddc auth logout gha
```

## Helm

Helm reads release data from the cluster, so it uses the **same kubeconfig as
Kubernetes** — nothing extra to authenticate. Set `HELM_DRIVER` if your releases
use a non-default storage backend.

## Argo CD

### Log in / create a token

Interactive login (stores the server + a session token in
`~/.config/argocd/config`, which `ddc` reads):

```
argocd login <ARGOCD_SERVER>          # username/password, or:
argocd login <ARGOCD_SERVER> --sso    # browser SSO
```

For a stable, read-only API token, create a dedicated local account rather than
using `admin`:

1. In the `argocd-cm` ConfigMap, enable an account with API-key capability:

   ```yaml
   data:
     accounts.ddc-readonly: apiKey
   ```

2. In the `argocd-rbac-cm` ConfigMap, bind it to the built-in read-only role:

   ```yaml
   data:
     policy.csv: |
       g, ddc-readonly, role:readonly
   ```

3. Generate the token (copy the printed JWT):

   ```
   argocd account generate-token --account ddc-readonly
   ```

### Give it to ddc — pick one

- **Environment variables:**

  ```
  export ARGOCD_SERVER=argocd.example.com
  export ARGOCD_AUTH_TOKEN=<the-generated-token>
  export ARGOCD_INSECURE=true   # only for self-signed servers
  ```

- **Reuse the `argocd` CLI:** after `argocd login`, `ddc` reads the current
  server and token from `~/.config/argocd/config`.

- **Store a token in the keychain:** `ddc auth login argocd`.

Pass `--server`/`--insecure` per command to override.

## Docker

Docker has no token — `ddc` connects to a daemon using your standard Docker
environment. Choose how to reach the daemon:

- **Local daemon (default):** nothing to do — `ddc` uses the local socket
  (`unix:///var/run/docker.sock`). Just ensure the daemon is running and your user
  can access the socket.

- **Remote daemon over TLS:** point at it and supply the client certs your daemon
  admin issued:

  ```
  export DOCKER_HOST=tcp://dockerhost.example.com:2376
  export DOCKER_TLS_VERIFY=1
  export DOCKER_CERT_PATH=/path/to/certs   # contains ca.pem, cert.pem, key.pem
  ```

- **Remote daemon over SSH** (uses your existing SSH auth):

  ```
  export DOCKER_HOST=ssh://user@dockerhost.example.com
  ```

- **Docker contexts:** if you manage connections with `docker context`, select one
  via `export DOCKER_CONTEXT=<name>`.

## Jenkins

### Create an API token

In the Jenkins web UI:

1. Sign in, then click your **username** (top-right) → **Security** (on older
   Jenkins, **Configure**). Direct URL: `<JENKINS_URL>/me/security/`.
2. Under **API Token**, click **Add new Token**, give it a name, and **Generate**.
3. Copy the token immediately — it is shown only once.

Authenticate with your login name plus this token (Jenkins API auth is HTTP basic
`username:apiToken`; never your account password).

### Give it to ddc

```
export JENKINS_URL=https://jenkins.example.com
export JENKINS_USER=<your-login-name>
export JENKINS_TOKEN=<api-token>
```

Or store the token in the keychain (still set `JENKINS_URL`/`JENKINS_USER`):

```
ddc auth login jenkins
```

**Recommended:** generate the token under a Jenkins user with read-only
permissions (Overall/Read + Job/Read).

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
