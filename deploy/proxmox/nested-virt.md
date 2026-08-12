# Nested virtualization and policy limitations (Proxmox)

TemperCI job isolation is **KVM microVMs (Firecracker)**. That requires a working `/dev/kvm` on the machine that runs `temperci-agent`. On Proxmox this interacts with nested virt and site policy.

## Preferred topology (no nested virt)

```text
Physical host
└── Proxmox VE (host OS)
    ├── PVE-managed VMs/CTs (your workloads)
    └── temperci-agent (systemd) → Firecracker microVMs for CI jobs
```

Here Firecracker is a **sibling** of Proxmox QEMU VMs, not nested inside them. Nested virtualization is **not** required.

## Nested topology (agent inside a Proxmox VM)

```text
Physical host
└── Proxmox VE
    └── Ubuntu/Debian VM (nested virt enabled)
        └── temperci-agent → Firecracker
```

Use only when policy forbids installing services on the Proxmox host OS.

### Requirements

1. **CPU type** on the guest must expose virtualization flags (e.g. host CPU type, or a type with `+vmx` / `+svm`).
2. **Nested KVM** enabled on the Proxmox host modules when needed:

   ```bash
   # On Proxmox host — check current nested setting
   cat /sys/module/kvm_intel/parameters/nested   # Intel: expect Y or 1
   cat /sys/module/kvm_amd/parameters/nested     # AMD: expect 1
   ```

   Persist if required (example Intel):

   ```bash
   echo "options kvm_intel nested=1" > /etc/modprobe.d/kvm-nested.conf
   # reboot or reload modules carefully on a maintenance window
   ```

3. Inside the guest: `/dev/kvm` must exist and be usable by the agent.
4. Expect **higher latency and lower density** than host-level Firecracker. Size pools conservatively.

### What we do **not** do

| Anti-pattern | Why |
|--------------|-----|
| Silently fall back to long-lived `qm` VMs when nested KVM fails | Violates hard teardown / no-stale-disk product rules |
| One full Proxmox VM per job as the default path | Slow boot, harder cleanup, different lifecycle |
| Run Firecracker without `/dev/kvm` | Unsupported; agent must fail closed |

If nested virt is blocked by hypervisor policy, cloud provider, or security baseline: **install the agent on a bare metal / Proxmox host that exposes KVM**, or use a separate Ubuntu bare-metal node. Document the block in your ops notes rather than weakening cleanup.

## Policy and multi-tenancy notes

- Host-level agent is powerful (KVM + net admin). Restrict SSH/API access to the Proxmox host accordingly.
- Job microVMs should not reach Proxmox management interfaces (`8006`, cluster network) without intentional allow rules.
- Site policies that ban “extra hypervisors on the host” need an explicit exception for Firecracker, or you must use a dedicated CI host outside the PVE cluster nodes.
- SELinux/AppArmor profiles on customized Proxmox installs may block Firecracker sockets or taps — test on a lab node first.

## Validation checklist

- [ ] Topology chosen: **host agent** (preferred) or **nested guest agent**
- [ ] `ls -l /dev/kvm` succeeds where the agent runs
- [ ] `firecracker --version` works
- [ ] If nested: nested module param confirmed; guest CPU type exposes VMX/SVM
- [ ] If nested blocked: **stop** — do not invent a full-VM-per-job workaround for MVP

## Cannot validate from this repo’s default dev machine

The TemperCI development workspace is **not** a Proxmox host. Nested-virt and host-KVM checks above must be run by an operator on real Proxmox hardware (or a nested lab). Unit/e2e tests on macOS use the fake VMM only.
