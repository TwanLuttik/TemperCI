# One-command Linux install

**Date:** 2026-08-23
**Status:** Approved for implementation
**Product:** TemperCI — first-run installer for a single Ubuntu/KVM host

## 1. Problem

A new host today requires a long operator runbook: apt packages, a hand-fetched Firecracker binary, `make build` (Go + Node), copying systemd units, writing both TOML files, building a 12G guest image, then opening the dashboard. That blocks the existing setup wizard, which is already the right place for GitHub App + auth.

The operator should run **one command**. Everything the machine needs is installed. The only remaining work is the wizard.

## 2. Goals

1. One command installs every host dependency and launches TemperCI.
2. The user never installs Firecracker, KVM packages, binaries, guest image, or systemd by hand.
3. Control is reachable for the setup wizard **before** the guest image finishes.
4. The installer prints a numbered step/progress indicator.
5. After the wizard (GitHub App, auth), the host can run jobs once the guest image is ready.

## 3. Non-goals

- Multi-host / separate control plane install
- Distros other than Ubuntu 22.04/24.04 amd64
- Shipping a prebuilt 12G rootfs on GitHub Releases (v1 builds on the host)
- TLS, reverse proxy, or Funnel/Serve automation
- Uninstall / upgrade beyond “re-run is idempotent”
- Windows, macOS, or nested-virt workarounds when `/dev/kvm` is missing

## 4. Decisions (locked)

| Topic | Choice |
|---|---|
| Command | `curl -fsSL https://github.com/TwanLuttik/TemperCI/releases/latest/download/install.sh \| sudo bash` |
| Scope | Single-node: control + agent + hostctl on the same box |
| Distro | Ubuntu 22.04 or 24.04, x86_64, `/dev/kvm` required |
| Progress | `[n/8] name .... ok\|fail\|running` on stderr |
| Config | Installer writes minimal TOML; wizard owns GitHub + auth |
| Token | Installer generates `agent_token` and writes it to **both** TOML files |
| Guest image | On-host `build-guest-image.sh` in a systemd oneshot **after** control is up |
| Kernel | Existing `fetch-kernel.sh` (pinned Firecracker CI vmlinux) |
| Firecracker | Installer downloads a pinned official release into `/usr/local/bin/firecracker` |
| Binaries | Same GitHub Release as `install.sh` (`*-linux-amd64`) |
| Re-run | Idempotent. Existing TOML is not overwritten |
| Image miss | Agent starts anyway; pool create fails until the oneshot finishes, then agent is restarted |

## 5. User journey

```text
operator                          host
   |                                |
   |  curl .../install.sh | sudo bash
   |------------------------------->|
   |                                |  [1] check Linux/root/kvm/ubuntu
   |                                |  [2] apt packages
   |                                |  [3] Firecracker
   |                                |  [4] temperci-{control,agent,hostctl}
   |                                |  [5] dirs + TOML + units
   |                                |  [6] start control  → print URL
   |<----- http://<lan-ip>:8080/ ---|
   |  wizard: auth, GitHub App      |  [7] start agent (no image yet)
   |                                |  [8] temperci-guest-image.service
   |                                |      fetch kernel + build rootfs
   |                                |      restart agent
   |  Services step: image ready    |  warm VMs boot
```

Wizard steps stay as they are: Access, GitHub App, Agent, Services, Review. Installer pre-fills listen address, cache listen address, and agent token. The user only fills GitHub fields and auth.

## 6. Installer

**Path:** `deploy/ubuntu/install.sh` (also published as the `install.sh` release asset).

Must run as root. `set -euo pipefail`. No interactive prompts.

### 6.1 Progress UI

Eight steps. Each line:

```text
[3/8] Installing Firecracker ....................... ok
[8/8] Preparing guest image ........................ running
```

- `ok` / `fail` / `skip` (already present) / `running`
- Failures print the command’s last lines and exit non-zero
- Step 6 prints the wizard URL as soon as `/healthz` returns `ok`
- Step 8 starts the oneshot and, if stdout is a TTY, tails `journalctl -u temperci-guest-image -f` until the unit is `active`/`inactive` with success, or 45 minutes elapse. If not a TTY, print `journalctl -u temperci-guest-image -f` and exit 0 after starting the unit (control is already up)

### 6.2 Steps

| # | Name | Does |
|---|---|---|
| 1 | Check host | `uname -s` Linux; `id -u` 0; `/dev/kvm` exists; `uname -m` is x86_64; `/etc/os-release` is Ubuntu 22.04 or 24.04 |
| 2 | Packages | Same set as `host-prereqs.sh` plus anything `build-guest-image.sh` needs (`debootstrap`, `e2fsprogs`). `apt-get update` + `DEBIAN_FRONTEND=noninteractive apt-get install -y` |
| 3 | Firecracker | If `firecracker` is already on PATH and `--version` works, skip. Else download pinned `v1.9.1` tarball from `github.com/firecracker-microvm/firecracker/releases` and `install` to `/usr/local/bin/firecracker` |
| 4 | TemperCI binaries | Download `temperci-control`, `temperci-agent`, `temperci-hostctl` for `linux-amd64` from the **same release** as this `install.sh`. Default URL base: `https://github.com/TwanLuttik/TemperCI/releases/latest/download`. Override with `TEMPERCI_VERSION` (tag) or `TEMPERCI_RELEASE_URL`. `install -m 0755` into `/usr/local/bin` |
| 5 | Config + units | `mkdir -p /etc/temperci /var/lib/temperci/{images,instances} /var/log/temperci`. Write TOML only if missing. Install `deploy/systemd/*.service` and new `temperci-guest-image.service`. `systemctl daemon-reload` |
| 6 | Control | `systemctl enable --now temperci-control`. Poll `http://127.0.0.1:8080/healthz` up to 20s. Print `http://<first-non-loopback-IPv4>:8080/` (fallback `http://<hostname>:8080/`) |
| 7 | Agent | `systemctl enable --now temperci-agent`. Agent may log create/boot errors until the image exists — that is expected |
| 8 | Guest image | `systemctl start temperci-guest-image` (oneshot, remains enabled so a reboot retries a failed/incomplete image). On success the unit `ExecStartPost` restarts `temperci-agent` |

### 6.3 Bootstrap TOML

Written only when the file does not exist.

**`/etc/temperci/control.toml`** (`0600`):

```toml
listen_addr = "0.0.0.0:8080"
github_app_private_key_path = "/etc/temperci/github-app.pem"
github_org = ""
label_prefix = "temperci-"
runner_group_id = 1
agent_token = "<openssl rand -hex 32>"
auth_mode = "open"
setup_completed = false
sqlite_path = "/var/lib/temperci/control.db"
hostctl_path = "/usr/local/bin/temperci-hostctl"
data_dir = "/var/lib/temperci"
```

`github_app_id` omitted (0). Webhook secret omitted. Control already treats this as setup mode (`NeedsSetup()`).

**`/etc/temperci/agent.toml`** (`0600`): same `agent_token`, `control_url = "http://127.0.0.1:8080"`, `vmm_backend = "firecracker"`, default image/kernel paths, `min_ready = 1`, `max_ready = 2`, `job_simulate_seconds = 0`, `cache_listen_addr = "127.0.0.1:8743"`, `memory_mib = 6144`, `vcpu = 4`.

Wizard apply must keep an existing token when the form field is blank (already the settings/setup behavior for secrets). Do not regenerate the token on apply if installer already set one.

### 6.4 Guest-image unit

New file `deploy/systemd/temperci-guest-image.service`:

- `Type=oneshot`, `RemainAfterExit=yes`
- `ConditionPathExists=!/var/lib/temperci/images/ubuntu-2404-runner.ext4` **or** an explicit stamp file `/var/lib/temperci/images/.ready` written at the end of a successful build (prefer the stamp: a partial ext4 must not count as ready)
- `ExecStart` a small wrapper `deploy/ubuntu/prepare-guest-image.sh` that:
  1. Runs `fetch-kernel.sh` if `vmlinux` is missing
  2. Runs `build-guest-image.sh` if the ext4 or stamp is missing
  3. Writes `.ready` only after both files exist and `gzip`/`file` checks are not required — existence + non-zero size of ext4 and vmlinux is enough
- `ExecStartPost=/usr/local/bin/temperci-hostctl restart agent` (ignore failure if agent is not enabled yet)
- Logs go to the journal (`StandardOutput=journal`)

`build-guest-image.sh` already prints phases (create ext4, debootstrap, runner tarball). The installer tails that.

Partial builds: `build-guest-image.sh` already uses a work file + trap cleanup. The stamp is only written after the atomic `mv` into place.

### 6.5 How the wizard and agent notice the image

No new wizard step.

- `setupSnapshot()` already reports `guest_image` / `guest_kernel` and marks Services `warn` when either file is missing.
- After the oneshot writes the ext4 + stamp and restarts the agent, the next `/api/v1/setup/status` poll shows “guest image ready”.
- Agent `New()` does not require the rootfs; `provision()` fails until the file exists. Restart after `.ready` starts a clean warm pool.

Optional small UI follow-up (same PR if cheap): Services step auto-refreshes status every 5s while `guest_image` is false.

## 7. Releases

CI today only `go test` + a host `go build`. Add a **release** workflow:

- Trigger: GitHub Release published (and optionally tags `v*`)
- `GOOS=linux GOARCH=amd64 CGO_ENABLED=0` build of control, agent, hostctl
- Attach: `temperci-control-linux-amd64`, `temperci-agent-linux-amd64`, `temperci-hostctl-linux-amd64`, `install.sh` (copy of `deploy/ubuntu/install.sh`)
- `install.sh` in the tarball/repo may substitute `TEMPERCI_DEFAULT_VERSION` at release time, or resolve `latest`

Local/dev: `TEMPERCI_RELEASE_URL=file:///path/to/bin` or `TEMPERCI_BIN_DIR=./bin` so a developer can run the script against locally built linux binaries.

## 8. Failure handling

| Failure | Behavior |
|---|---|
| Not Linux / not root / no KVM / wrong arch / not Ubuntu | Exit 1 at step 1 with a one-line reason |
| apt fails | Exit 1; leave packages as apt left them |
| Firecracker download fails | Exit 1 before binaries |
| Binary download fails | Exit 1; do not start units |
| `/healthz` never ok | Exit 1; print `journalctl -u temperci-control -n 50` |
| Guest image oneshot fails | Control/agent stay up. Installer (TTY) exits 1 after showing the unit log. Wizard Services stays “image missing”. Re-run `sudo systemctl start temperci-guest-image` or re-run `install.sh` (idempotent; skips 1–7) |
| Re-run with existing TOML | Skip rewrite. Still repair missing binaries/units/packages/image |
| Port 8080 busy | Control fails; installer surfaces the journal |

`curl | bash` must not lose the image build if the SSH session drops: the oneshot is a real systemd unit, not a background `&` from the script.

## 9. Security

- `install.sh` is fetched over HTTPS from GitHub Releases.
- Generated `agent_token` is 32 hex bytes; files are `0600`.
- Default `auth_mode = open` until the wizard; listen `0.0.0.0:8080`. Document that the host is open on the LAN until the operator picks password mode.
- Script never prints the token. Wizard already shows it if it generates one; installer-generated token is visible in the Agent step if the status API exposes “set” but not the value (keep it that way). Operator can read `/etc/temperci/control.toml` on the box.

## 10. Testing

- `bash -n deploy/ubuntu/install.sh deploy/ubuntu/prepare-guest-image.sh`
- `deploy/ubuntu/install_test.sh`: dry-run helpers — progress printer, Ubuntu/os-release parse, skip-if-exists, TOML write-once (use a temp root via `TEMPERCI_ROOT` / `DESTDIR` for unit tests; production default `/`)
- Do not run debootstrap or Firecracker download in unit tests
- CI: `bash -n` + `install_test.sh` on `ubuntu-latest`
- Manual proof: one Ubuntu/KVM host, curl the release script, open wizard, confirm image unit reaches `.ready` and agent logs `warm VM ready`

## 11. Docs

- README: replace the multi-step build/install with the one-liner; keep `make build` as the developer path
- `deploy/ubuntu/quickstart.md`: one-liner first; keep the manual path as “from a git checkout”
- `deploy/dashboard.md`: “after install.sh, open the printed URL”

## 12. Key decisions

1. **Hybrid timing** — wizard before image so first-run feels instant; image is a systemd oneshot so it survives a dropped SSH session.
2. **Build image on the host** — avoids a multi-gigabyte release asset; `fetch-kernel.sh` + `build-guest-image.sh` already exist.
3. **Installer writes both TOMLs + one token** — agent can register during setup; wizard does not invent a second token.
4. **Stamp file `.ready`** — a partial ext4 must not look like a guest image.
5. **Restart agent after image** — cheaper than teaching the pool to hot-wait on a missing file (it already retries, but restart is explicit and clears failed create leftovers).

## 13. PR Plan

| PR | Title | Files | Depends | Description |
|---|---|---|---|---|
| I1 | Installer script + progress + bootstrap TOML | `deploy/ubuntu/install.sh`, `install_test.sh`, `prepare-guest-image.sh`, `deploy/systemd/temperci-guest-image.service` | — | Steps 1–8 against `TEMPERCI_ROOT`; no release yet. Firecracker/binary download can be stubbed in tests |
| I2 | Release linux-amd64 artifacts + install.sh | `.github/workflows/release.yml`, Makefile `build-linux` | I1 | Publish the four assets on tag/release |
| I3 | Docs + wizard status refresh | `README.md`, `deploy/ubuntu/quickstart.md`, `deploy/dashboard.md`, optional SetupPage poll | I1 | One-liner is the documented path. Services step polls until image ready |

I1 is enough to run from a git checkout:

```bash
sudo TEMPERCI_BIN_DIR=./bin ./deploy/ubuntu/install.sh
```

I2 is required for `curl …/releases/latest/download/install.sh`.
