# Single-node Ubuntu quickstart (TemperCI MVP)

Run **control plane + host agent** on one Ubuntu 22.04/24.04 host with KVM, then execute a real GitHub Actions job via JIT self-hosted runners.

For guest image build details see [guest-image.md](guest-image.md). Host packages and Firecracker: [README.md](README.md).

## Prerequisites

- Ubuntu with `/dev/kvm`
- Root or a user in group `kvm` with write access to `/var/lib/temperci`
- GitHub org where you can install a GitHub App
- Go 1.22+ to build binaries (or copy prebuilt `temperci-control` / `temperci-agent`)

## 1. Install host deps + layout

```bash
sudo ./deploy/ubuntu/host-prereqs.sh
# Install Firecracker binary (see README.md)
# Build/place guest rootfs + kernel (see guest-image.md)
```

## 2. Build TemperCI

```bash
make build
sudo install -m 0755 bin/temperci-control bin/temperci-agent /usr/local/bin/
```

## 3. Configure

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

## 4. Start services

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

## 5. GitHub App + webhook

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
```

## Success demo checklist

Use this as an operator runbook:

- [ ] Install deps + KVM (`host-prereqs.sh`, Firecracker binary, `/dev/kvm`)
- [ ] Build/place Ubuntu base image + `actions/runner` + kernel (`guest-image.md`)
- [ ] `make build` and install both binaries
- [ ] Configure `/etc/temperci/{control,agent}.toml` with matching `agent_token`
- [ ] Start `temperci-control` and `temperci-agent`
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
