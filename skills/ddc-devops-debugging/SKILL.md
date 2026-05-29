---
name: ddc-devops-debugging
description: Use when debugging deployments, clusters, or CI/CD pipelines end-to-end — crashlooping pods, failed rollouts, broken GitHub Actions/Jenkins builds, OutOfSync Argo CD apps, bad Helm releases, exited Docker containers. Drives the read-only `ddc` CLI to trace a failure from the user's entry point across Kubernetes, Helm, Argo CD, GitHub Actions, Jenkins, and Docker, then reports root cause and fix steps. `ddc` is the ONLY tool to use for this — never raw kubectl/gh/helm/docker/argocd, never reading credential files.
---

# DevOps Debugging with `ddc`

`ddc` is a read-only DevOps debugging CLI. It is the **only** command you should
use to inspect Kubernetes clusters and GitHub Actions pipelines.

## Safety contract — follow exactly

- **Use only `ddc`.** Do NOT run `kubectl`, `helm`, `gh`, `docker`, `argocd`,
  `curl` against these APIs, or `cat`/`read` any credential file (`~/.kube/config`,
  `~/.config/gh/hosts.yml`, `.env`, etc.). `ddc` is read-only by construction;
  raw tools are not.
- **Never handle secrets.** `ddc` never prints secrets and redacts secret-looking
  values from all output. Do not ask the user to paste tokens, passwords, or
  kubeconfigs into the chat. Do not try to work around redaction — there is no
  flag to disable it, and that is intentional.
- **You cannot authenticate.** Pre-authentication is a human task (see
  "If a provider is not configured" below). Never attempt `ddc auth login` —
  it is interactive and for the user, not you.
- **`ddc` cannot mutate anything.** If a task needs a change (scale, rollback,
  re-run a pipeline, edit a resource), STOP and tell the user what to do — never
  reach for a raw tool to do it yourself.

## First step, always

Check what you can reach. This prints safe identity only (never secrets):

```
ddc auth status
```

Use `--env <context-or-name>` on any command to target a specific environment.
Add `--json` to any command for machine-readable output.

## How to debug end-to-end

Work the problem as a chain, from the user's entry point down to the failing
component. Use the per-provider playbooks below as the steps within this loop.

1. **Anchor on the entry point.** Start from exactly what the user reported — a
   symptom ("checkout is 500ing"), a service/app/repo name, a failed pipeline, an
   alert, a URL. Don't broaden the investigation beyond it until you have reason to.

2. **Establish scope, ask when ambiguous.** Run `ddc auth status` to see which
   environments are reachable (the user pre-authenticated these — only these are
   available to you). If the target environment or resource is unclear — multiple
   reachable clusters/contexts, an unspecified namespace, several matching repos or
   pods — **ask the user one concise question** naming the candidates rather than
   guessing or scanning everything. Examples: "Which cluster context — `--env
   staging` or `--env prod`?", "Which namespace is the checkout service in?".

3. **Follow the dependency chain across tools.** Failures propagate across layers,
   so cross provider boundaries to trace cause from effect. Common chains:
   - CI/CD → runtime: GitHub Actions / Jenkins build → Argo CD app sync → Helm
     release → Kubernetes Deployment → Pod → container logs.
   - Runtime symptom → cause: unhealthy Service → Pods behind it → `describe` →
     `events` → `logs --previous`.
   Hop to the next layer using the evidence from the last (a failed deploy shows up
   as a Degraded app, which shows up as a CrashLoopBackOff pod, whose previous-logs
   hold the real error).

4. **Gather evidence at each hop.** Prefer status/describe/events first, then logs.
   Follow the concrete signal — restart counts, exit codes, OOMKilled, event
   reasons, the first error line in logs. Stop pulling data once the cause is clear;
   don't dump everything.

5. **Stop and report.** When you can name the root cause (or have ruled the
   reachable evidence dry), present the diagnosis summary below — do not keep
   digging indefinitely, and never attempt a fix yourself.

### Diagnosis summary (how to report)

Present a tight summary, not a transcript. Structure it as:

- **Problem** — one line: what is broken and where (environment + resource).
- **Root cause** — the underlying reason, concise, grounded in specific evidence
  you saw (name the command/field, e.g. "previous-instance logs show
  `OOMKilled`, exit 137; memory limit 128Mi"). Explain *why* in plain terms.
- **Impact** — what's affected (optional, when it adds clarity).
- **Suggested fix** — concrete, numbered steps **for the user to run** (ddc is
  read-only; you cannot apply them). Be specific (commands, files, values).
- **If unconfirmed** — if evidence was insufficient, say what's still uncertain and
  what additional access or input would confirm it.

Keep explanations concise; lead with the conclusion, then the supporting evidence.

## Kubernetes (`ddc k8s`)

```
ddc k8s get pods -n <ns>            # or -A for all namespaces
ddc k8s get deployments|services|nodes|events -n <ns>
ddc k8s describe pod <name> -n <ns>     # status, container states, restarts, events
ddc k8s logs <pod> -n <ns> [-c <container>] [--previous] [--tail 200]
ddc k8s events -n <ns>
```

`ddc k8s get secret` is **blocked by design** — do not try to read Secrets.

### Playbook: crashlooping / not-ready pod

1. `ddc k8s get pods -n <ns>` — find the pod; note STATUS (e.g. CrashLoopBackOff)
   and RESTARTS.
2. `ddc k8s describe pod <name> -n <ns>` — read container states, the last
   termination reason/exit code, and recent events.
3. `ddc k8s logs <name> -n <ns> --previous` — logs from the crashed instance are
   usually where the real error is. Drop `--previous` for the current instance.
4. `ddc k8s events -n <ns>` — cluster-level causes (FailedScheduling, image pull
   errors, OOMKilled, failed mounts).
5. Diagnose from the evidence. If a fix requires a change, describe it to the
   user; do not apply it.

### Playbook: deployment not progressing

1. `ddc k8s get deployments -n <ns>` — compare READY vs AVAILABLE.
2. `ddc k8s get pods -n <ns>` — find unhealthy pods behind it.
3. Continue with the crashloop playbook on those pods.

## GitHub Actions (`ddc gha`)

Target a repo with `--repo owner/repo` (or set `GH_REPO`).

```
ddc gha runs list --repo o/r [--workflow <name>] [--branch <b>] [--status failure] [--limit 20]
ddc gha run view <run-id> --repo o/r        # jobs + which steps failed
ddc gha run logs <run-id> --repo o/r [--job <id>]   # defaults to first failed job
ddc gha workflows list --repo o/r
```

### Playbook: failed pipeline run

1. `ddc gha runs list --repo o/r --status failure` — find the failing run id.
2. `ddc gha run view <run-id> --repo o/r` — see which job/steps failed.
3. `ddc gha run logs <run-id> --repo o/r` — prints the first failed job's logs
   (or pass `--job <id>` for a specific one). Redacted automatically.
4. Diagnose. To re-run or change the workflow, tell the user — do not do it.

## Argo CD (`ddc argocd`)

Target the server with `--server <host>` (or `ARGOCD_SERVER`); add `--insecure`
for self-signed certs.

```
ddc argocd apps list                  # name, project, sync, health
ddc argocd app get <name>             # source, destination, sync/health detail
ddc argocd app resources <name>       # managed resources and their health
ddc argocd app diff <name>            # live vs desired (Secret bodies hidden)
ddc argocd app history <name>         # deployment history, most recent first
```

### Playbook: app OutOfSync / Degraded

1. `ddc argocd apps list` — find the app; note SYNC and HEALTH.
2. `ddc argocd app get <name>` — read the health message, target revision, dest.
3. `ddc argocd app resources <name>` — find the Degraded/Missing resource.
4. If OutOfSync, `ddc argocd app diff <name>` — see exactly what drifted; and
   `ddc argocd app history <name>` — check whether a recent sync caused it.
5. Cross-check that workload in the cluster with `ddc k8s describe pod …`.

## Helm (`ddc helm`)

```
ddc helm list [-n <ns>] [-A]
ddc helm status <release> -n <ns>
ddc helm history <release> -n <ns>
ddc helm values <release> -n <ns>     # redacted
```

### Playbook: release in a bad state

1. `ddc helm list -A` — find the release; note STATUS (failed/pending-*).
2. `ddc helm status <release> -n <ns>` — read the description.
3. `ddc helm history <release> -n <ns>` — did a recent revision regress it?
4. `ddc helm values <release> -n <ns>` — check supplied values.
5. Inspect the rendered workloads with `ddc k8s …`.

## Docker (`ddc docker`)

Uses your Docker environment (`DOCKER_HOST`, socket).

```
ddc docker ps [-a]
ddc docker inspect <container>
ddc docker logs <container> [--tail 200]
ddc docker images
```

### Playbook: container exiting / restarting

1. `ddc docker ps -a` — find the container; note STATE/STATUS.
2. `ddc docker inspect <container>` — exit code, OOMKilled, error.
3. `ddc docker logs <container> --tail 200` — the failure output (redacted).

## Jenkins (`ddc jenkins`)

Target the controller with `--url <base-url>` (or `JENKINS_URL`). Folder jobs:
pass `folder/job`.

```
ddc jenkins jobs list
ddc jenkins build view <job> <number>
ddc jenkins build logs <job> [number]   # defaults to last build; redacted
```

### Playbook: failed Jenkins build

1. `ddc jenkins jobs list` — find the failing job (STATUS Failed).
2. `ddc jenkins build view <job> <number>` — result, duration, timing.
3. `ddc jenkins build logs <job> <number>` — console output.

## If a provider shows "not configured"

Tell the user to pre-authenticate themselves; do not do it for them:

- **Kubernetes:** authenticate to the cluster (`aws eks update-kubeconfig …`,
  `gcloud container clusters get-credentials …`) so a kubeconfig context exists,
  then re-run with `--env <context>` if needed.
- **GitHub Actions:** set `GH_TOKEN` (a read-only / fine-grained token), or
  authenticate the `gh` CLI, or run `ddc auth login gha` (interactive).
- **Helm:** same as Kubernetes — it uses the kubeconfig.
- **Argo CD:** set `ARGOCD_SERVER` and `ARGOCD_AUTH_TOKEN` (a read-only account),
  log in with the `argocd` CLI, or run `ddc auth login argocd`. If status instead
  reads `error: … session token expired`, the user's SSO session lapsed — `ddc`
  does not refresh SSO tokens. Tell the user to run `argocd relogin` to refresh it,
  or (to avoid repeat expiry, recommended for agent use) set a long-lived read-only
  `ARGOCD_AUTH_TOKEN`.
- **Docker:** ensure the daemon is running and `DOCKER_HOST` points at it.
- **Jenkins:** set `JENKINS_URL`, `JENKINS_USER`, and `JENKINS_TOKEN` (an API
  token), or run `ddc auth login jenkins`.

Point the user to `docs/pre-auth-guide.md` for details. They hold the secret; you
never see it.
