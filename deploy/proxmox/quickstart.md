# Proxmox operator quickstart + smoke checklist

Run the **same** control plane + host agent path as Ubuntu, but on a **Proxmox VE host** with Firecracker on host KVM.

Prereqs detail: [README.md](README.md) · Nested limits: [nested-virt.md](nested-virt.md) · Storage: [storage.md](storage.md) · Guest image: [../ubuntu/guest-image.md](../ubuntu/guest-image.md)

## What cannot be automated from the TemperCI dev workspace

| Step | On this repo’s default machine (non-Proxmox) | On a real Proxmox host |
|------|-----------------------------------------------|-------------------------|
| Packages / `/dev/kvm` | not applicable | operator |
| Firecracker boot | no | operator |
| One real GitHub Actions job | no | operator |
| Teardown verification on PVE storage | documented + script only | operator |
| Control↔agent e2e with fake VMM | `go test ./internal/e2e` | optional |

**Phase 6 deliverable:** repeatable docs + scripts. Live green job is an **operator checklist** below, not CI on this machine.

---

## 1. Validate KVM on the Proxmox node

```bash
# SSH as root to the Proxmox host
ls -l /dev/kvm
lsmod | grep kvm
# Prefer host-level agent (no nested virt). If agent must live in a VM, see nested-virt.md.
```

## 2. Host packages + layout

```bash
# From a checkout of this repo on the host:
sudo ./deploy/proxmox/host-prereqs.sh
# Install Firecracker (see README.md)
# Build or copy guest rootfs + kernel into data_dir/images/ (guest-image.md)
```

Default layout:

```bash
ls -la /var/lib/temperci/images
ls -la /var/lib/temperci/instances
```

## 3. Build / install binaries

```bash
# On a build machine (Go 1.22+), then copy to Proxmox:
make build
sudo install -m 0755 bin/temperci-control bin/temperci-agent /usr/local/bin/
```

Same binaries as Ubuntu — no Proxmox-specific build tags required for MVP.

## 4. Configure

```bash
sudo mkdir -p /etc/temperci
sudo cp deploy/control.example.toml /etc/temperci/control.toml
sudo cp deploy/agent.example.toml /etc/temperci/agent.toml
# Or start from Proxmox-oriented comments:
# sudo cp deploy/proxmox/agent.example.toml /etc/temperci/agent.toml

TOKEN=$(openssl rand -hex 32)
sudo sed -i "s/replace-with-long-random-string/${TOKEN}/" \
  /etc/temperci/control.toml /etc/temperci/agent.toml
```

Agent essentials on Proxmox:

```toml
control_url = "http://127.0.0.1:8080"
agent_token = "<same as control>"
agent_id = "pve-node-1"          # optional; defaults to hostname
vmm_backend = "firecracker"
image_path = "/var/lib/temperci/images/ubuntu-2404-runner.ext4"
kernel_path = "/var/lib/temperci/images/vmlinux"
data_dir = "/var/lib/temperci"
min_ready = 1
max_ready = 2
job_simulate_seconds = 0
```

Fill GitHub App fields in `control.toml` ([control-plane-dry-run.md](../../docs/architecture/control-plane-dry-run.md)).

## 5. Start services

```bash
sudo cp deploy/systemd/temperci-*.service /etc/systemd/system/
sudo systemctl daemon-reload
sudo systemctl enable --now temperci-control temperci-agent

curl -sS http://127.0.0.1:8080/healthz
journalctl -u temperci-agent -f
# expect: agent worker registered, warm VM ready
```

Single-node lab: control + agent on the same Proxmox host. Multi-host: control on a small VM; agents on each PVE node with `control_url` pointing at control.

## 6. GitHub App + workflow

1. Install App on test org; webhook → `https://<reachable-host>/webhooks/github`
2. Push workflow:

```yaml
# .github/workflows/temperci-smoke.yml
name: temperci-smoke
on: [workflow_dispatch]
jobs:
  smoke:
    runs-on: temperci-4vcpu-ubuntu-2404
    steps:
      - uses: actions/checkout@v4
      - run: uname -a && echo hello-from-temperci-proxmox
```

3. Confirm control logs: mint → claim → started → finished  
4. Confirm agent logs: `job bound` (second job should show `warm_bind=true` after replenish)

## 7. Verify teardown (no stale disks)

Immediately after the job finishes:

```bash
# Automated checks (warm pool members may remain):
sudo ./deploy/proxmox/verify-cleanup.sh \
  --data-dir /var/lib/temperci \
  --expect-warm-max 2

# Manual:
ls -la /var/lib/temperci/instances
# Finished job VM id must not appear; only current warm/busy ids.

pgrep -a firecracker || true
# Count should match live pool VMs, not finished jobs.

# No stray TemperCI taps/netns from destroyed ids:
ip link show | grep tc-tap || true
ip netns list | grep tc-ns || true

df -h /var/lib/temperci
du -sh /var/lib/temperci/instances/*
```

Optional create/destroy loop (no GitHub):

```bash
./scripts/vmm-smoke.sh --root /var/lib/temperci --n 5 --backend firecracker
sudo ./deploy/proxmox/verify-cleanup.sh --data-dir /var/lib/temperci --expect-warm-max 0
```

---

## Operator smoke checklist (one real job)

Copy this into your change ticket / runbook:

### Host prep

- [ ] Proxmox node selected; topology is **host agent** (or nested documented in nested-virt.md)
- [ ] `/dev/kvm` present where agent runs
- [ ] `./deploy/proxmox/host-prereqs.sh` completed
- [ ] Firecracker binary installed (`firecracker --version`)
- [ ] Guest image + kernel under `data_dir/images/` ([guest-image.md](../ubuntu/guest-image.md))
- [ ] Disk headroom for images + pool ([storage.md](storage.md))
- [ ] CPU/RAM budget reserved so PVE guests are not starved ([README.md](README.md))

### Install

- [ ] `temperci-control` + `temperci-agent` installed (same binaries as Ubuntu)
- [ ] `/etc/temperci/{control,agent}.toml` with matching `agent_token`
- [ ] `vmm_backend = "firecracker"`, `data_dir` points at chosen local path
- [ ] systemd units enabled; `/healthz` OK; warm VM ready in logs

### Job

- [ ] GitHub App + webhook delivering `workflow_job` queued
- [ ] Workflow `runs-on: temperci-…` dispatched
- [ ] Job **green** on github.com (`checkout` + `uname` / `echo`)
- [ ] Agent logged bind; second job shows `warm_bind=true` if pool refilled

### Cleanup

- [ ] `verify-cleanup.sh` exits 0 (or manual checks pass)
- [ ] No `instances/<finished-id>/` left
- [ ] No orphan Firecracker processes for finished jobs
- [ ] No stale `tc-tap-*` / `tc-ns-*` for destroyed ids

### Sign-off

- [ ] Nested virt limitations reviewed if not host-level
- [ ] Ops notes record `data_dir`, node name, pool size

---

## Local non-Proxmox development

```bash
go test ./...
go test ./internal/e2e/ -count=1 -v
make build
```

These prove control↔agent semantics with the fake VMM. They **do not** replace the Proxmox operator checklist above.
