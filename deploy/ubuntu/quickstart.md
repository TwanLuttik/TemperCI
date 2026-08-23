# Single-node Ubuntu quickstart (TemperCI MVP)

Run **control plane + host agent** on one Ubuntu 22.04/24.04 host with KVM, then execute a real GitHub Actions job via JIT self-hosted runners.

## Recommended: one command

Requires Ubuntu 22.04/24.04 or Debian 12/13 (Proxmox VE 8/9), x86_64, `/dev/kvm`, and root. The script installs packages, Firecracker, TemperCI, systemd units, and starts the dashboard. The guest image builds in the background. On Proxmox it skips Debian `qemu-kvm` so it does not fight `pve-qemu-kvm`.

```bash
curl -fsSL https://github.com/TwanLuttik/TemperCI/releases/latest/download/install.sh | bash
```

Open the printed URL (port `8080`) and finish the setup wizard (auth + GitHub App). The host is reachable on the LAN with `auth_mode=open` until you choose password mode.

Re-run is safe: existing `/etc/temperci/*.toml` is not overwritten. If the guest image unit fails:

```bash
sudo journalctl -u temperci-guest-image -e
sudo systemctl start temperci-guest-image
```

From a git checkout (uses local binaries):

```bash
make build-ui build-linux   # or: make build  on the Linux host
TEMPERCI_BIN_DIR=./bin ./deploy/ubuntu/install.sh
```

For guest image internals see [guest-image.md](guest-image.md). Host packages and Firecracker: [README.md](README.md).

## Manual install (from a git checkout)

### Prerequisites

- Ubuntu with `/dev/kvm`
- Root or a user in group `kvm` with write access to `/var/lib/temperci`
- GitHub org where you can install a GitHub App
- Go 1.22+ to build binaries (or copy prebuilt `temperci-control` / `temperci-agent`)

### 1. Install host deps + layout

```bash
sudo ./deploy/ubuntu/host-prereqs.sh
# Install Firecracker binary (see README.md)
sudo ./deploy/ubuntu/build-guest-image.sh   # rootfs + kernel; see guest-image.md
```

### 2. Build TemperCI

```bash
make build
sudo install -m 0755 bin/temperci-control bin/temperci-agent bin/temperci-hostctl /usr/local/bin/
```

### 3. Configure

```bash
sudo mkdir -p /etc/temperci
sudo cp deploy/control.example.toml /etc/temperci/control.toml
sudo cp deploy/agent.example.toml /etc/temperci/agent.toml
# Generate a shared agent token:
TOKEN=$(openssl rand -hex 32)
sudo sed -i "s/replace-with-long-random-string/${TOKEN}/" /etc/temperci/control.toml /etc/temperci/agent.toml
```

Fill in GitHub App fields in `control.toml` (see [control-plane-dry-run.md](../../docs/architecture/control-plane-dry-run.md)):

- `github_app_id`
- `github_app_private_key_path`
- `github_webhook_secret`
- `github_org`

Agent (`agent.toml`) on the same host:

```toml
control_url = "http://127.0.0.1:8080"
agent_token = "<same as control>"
vmm_backend = "firecracker"
image_path = "/var/lib/temperci/images/ubuntu-2404-runner.ext4"
kernel_path = "/var/lib/temperci/images/vmlinux"
data_dir = "/var/lib/temperci"
min_ready = 1
max_ready = 2
job_simulate_seconds = 0
```

### 4. Start services

Manual (debug):

```bash
sudo temperci-control -config /etc/temperci/control.toml
# other terminal:
sudo temperci-agent -config /etc/temperci/agent.toml
```

Or install systemd units from `deploy/systemd/` and `systemctl enable --now temperci-control temperci-agent`.

Health:

```bash
curl -sS http://127.0.0.1:8080/healthz
```

Agent logs should show `agent worker registered` and `warm VM ready`.

## 5. GitHub App + webhook (wizard or manual)

1. Create/install the GitHub App on your test org (permissions: org self-hosted runners).
2. Webhook URL: `https://<public-or-tunnel-host>/webhooks/github` (or SSH tunnel to :8080).
3. Secret must match `github_webhook_secret`.

## 6. Workflow

```yaml
# .github/workflows/temperci-smoke.yml
name: temperci-smoke
on: [push, workflow_dispatch]
jobs:
  smoke:
    runs-on: temperci-4vcpu-ubuntu-2404
    steps:
      - uses: actions/checkout@v4
      - run: uname -a && echo hello-from-temperci
```

Push the workflow. Control plane should log `minted JIT config` then `job claimed` / `job started` / `job finished`. Agent should log `job bound` with `warm_bind=true` on a second job after the pool refills.

## 7. Verify cleanup

After the job finishes:

```bash
ls /var/lib/temperci/instances   # only warm pool members, not the finished job VM
pgrep -a firecracker || true
sudo ./scripts/verify-cleanup.sh --data-dir /var/lib/temperci --expect-warm-max 2
```

## Success demo checklist

Use this as an operator runbook:

- [ ] `curl …/install.sh | bash` as root (or the manual steps below)
- [ ] Open the printed wizard URL; finish GitHub App + auth
- [ ] Guest image unit reached `/var/lib/temperci/images/.ready`
- [ ] Install GitHub App; webhook delivers `workflow_job` queued events
- [ ] Push workflow with `runs-on: temperci-…`
- [ ] Job green on GitHub (`checkout` + `uname` / `echo`)
- [ ] Second job shows `warm_bind=true` in agent logs
- [ ] Host clean after job (no leftover instance dir for finished VM)

## Local macOS / no-KVM development

You cannot run a real Firecracker guest on macOS. Use:

```bash
go test ./...
# Integration path (fake VMM):
go test ./internal/e2e/ -count=1 -v
```

Agent with `vmm_backend = "fake"` exercises pool + inject files without KVM. Real GitHub job green requires Ubuntu+KVM + guest image.
