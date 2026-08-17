# Production-gap todos — status

Written 2026-08-16 after the status review. Executed by subagents, then deployed to `10.0.0.50`.

| Area (from status table) | Todo | Subagent | Host deploy |
|--------------------------|------|----------|-------------|
| Real guest job execution | [01-real-guest-job-execution.md](01-real-guest-job-execution.md) | DONE | Warm Firecracker VM + guest-agent waiting for JIT |
| Guest image | [02-guest-image.md](02-guest-image.md) | DONE (script; not rebuilt from scratch on host) | Existing 8G image updated in place |
| Persistence / HA | [03-persistence-ha.md](03-persistence-ha.md) | DONE | Control logged `loaded assignments count=0`; SQLite `assignments` table present |
| Typical Actions workflows | [04-typical-actions-workflows.md](04-typical-actions-workflows.md) | DONE | docker, node, npm, git, jq, build-essential in guest; `python3-venv` still missing (apt pin) |
| Automated proof of a real job | [05-automated-proof.md](05-automated-proof.md) | DONE | `scripts/real-job-smoke.sh -fast` → `SMOKE_OK`; live GitHub dispatch not run |

Reports: `01-report.md` … `05-report.md`. Live GitHub proof: [proof-runbook.md](proof-runbook.md).
