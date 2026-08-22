# Proof runbook — one real GitHub job on Ubuntu+KVM

This is the operator checklist that **unit tests cannot replace**. It proves
webhook → mint JIT → warm bind → guest `actions/runner` → teardown on a
Linux+KVM host that runs `temperci-agent` (Firecracker).

In-repo automated proof (no GitHub):

```bash
go test ./internal/e2e/ ./internal/vmm/firecracker/ ./internal/agent/ -count=1
bash -n scripts/real-job-smoke.sh
# On the Linux+KVM host only:
sudo ./scripts/real-job-smoke.sh -fast     # artifacts + systemd + healthz
sudo ./scripts/real-job-smoke.sh           # also Firecracker create/destroy
```

---

## 0. Host is ready

SSH to the Ubuntu+KVM host as root.

```bash
ls -l /dev/kvm
command -v firecracker && firecracker --version
command -v mkfs.ext4
ls -lh /var/lib/temperci/images/ubuntu-2404-runner.ext4
ls -lh /var/lib/temperci/images/vmlinux
```

Install units if needed (`deploy/systemd/`), then:

```bash
systemctl is-active temperci-control temperci-agent
```

---

## 1. Control healthz

```bash
curl -sS http://127.0.0.1:8080/healthz
# expect: ok
```

---

## 2. Agent has a warm VM

```bash
journalctl -u temperci-agent -n 200 --no-pager | grep -E 'warm VM ready|temperci-agent started'
```

Expect a line containing `warm VM ready` (pool member booted from the base image,
**no** JIT on disk yet).

```bash
ls /var/lib/temperci/instances
# one (or min_ready) warm instance dir(s); guest/jitconfig must not exist yet
```

---

## 3. Dispatch a workflow

In a repo the GitHub App can see, add and dispatch:

```yaml
# .github/workflows/temperci-smoke.yml
name: temperci-smoke
on: workflow_dispatch
jobs:
  smoke:
    runs-on: temperci-4vcpu-ubuntu-2404
    steps:
      - uses: actions/checkout@v4
      - run: uname -a && echo hello-from-temperci
```

GitHub UI: **Actions → temperci-smoke → Run workflow**.

Webhook URL must be `https://<reachable-host>/webhooks/github` with the same
secret as `github_webhook_secret`.

---

## 4. Control: minted JIT config

```bash
journalctl -u temperci-control -n 200 --no-pager | grep -E 'minted JIT config|job claimed|job started|job finished'
```

Expect (fields may be structured JSON):

```text
minted JIT config    job_id=… run_id=… labels=[temperci-4vcpu-ubuntu-2404]
```

Do **not** expect the encoded JIT string in logs.

---

## 5. Agent: bind + guest runner

```bash
journalctl -u temperci-agent -n 300 --no-pager | grep -E 'job bound|starting guest runner|guest runner exited|job complete'
```

Expect, in order:

```text
job bound
starting guest runner
guest runner exited    exit_code=0
job complete           outcome=success
```

A first job may be a cold start; a second dispatch after the pool refills should
show `warm_bind=true`.

---

## 6. GitHub job green

On github.com the `smoke` job is **green** (`checkout` + `uname` / `echo`).
If it stays on “Waiting for a runner…”, the guest never connected — check
`/var/lib/temperci/job-logs/<vm-id>/{agent,runner}.log` if present, and
`journalctl -u temperci-agent`.

---

## 7. No leftover busy VM

After the job is green:

```bash
ls /var/lib/temperci/instances
# finished job VM id must be gone; only current warm (or busy) pool members
pgrep -a firecracker || true
sudo ./scripts/verify-cleanup.sh --data-dir /var/lib/temperci --expect-warm-max 2
```

There must be **no** `instances/<finished-vm-id>/` directory and no Firecracker
process for that id.

---

## Sign-off

| Check | Result |
|-------|--------|
| `GET /healthz` → `ok` | |
| Agent log `warm VM ready` | |
| Control log `minted JIT config` | |
| Agent `starting guest runner` / `guest runner exited` | |
| GitHub job green | |
| No leftover busy instance dir | |
