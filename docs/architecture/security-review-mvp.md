# Security review — TemperCI MVP (Phase 7)

**Date:** 2026-08-12  
**Scope:** Design §7 security properties vs implemented control plane + host agent.  
**Status:** MVP hardening pass (not a formal audit).

## Design §7 checklist

| Property | Status | Notes |
|----------|--------|--------|
| JIT tokens treated as secrets; never full-logged | **Holds** | Claim/finish logs omit `encoded_jit_config`. Assignment store clears JIT on finish/fail/stuck. Bind path clears payload after runner start. |
| Warm VMs have no GitHub credentials until bind | **Holds** | Pool boots with no JIT; bind injects then starts runner. Failed bind destroys VM (no re-warm of tainted instance). |
| One job per VM; destroy after job | **Holds** | Busy → destroy → replenish new warm VM. No guest disk reuse. |
| GitHub App least privilege (runner admin, no repo secrets) | **Operator** | Documented in dry-run / install docs; not enforced in code. Operators must configure App permissions correctly. |
| Host agent privileged (KVM/net); isolate binary/config | **Partial** | systemd units and config paths documented; no sandbox beyond OS permissions. Agent metrics/admin should bind loopback. |
| Orphan cleanup after failures | **Holds** | Start-time orphan sweep; mid-job kill test proves leftover instances destroyed on restart. Periodic destroy retry on failure. |

## Control ↔ agent authentication

| Mechanism | MVP |
|-----------|-----|
| Shared bearer token (`agent_token`) | **Required** on agent API routes; constant-time compare; missing/wrong token → 401 |
| HTTPS (server TLS) | **Optional** via `tls_cert_file` / `tls_key_file` |
| mTLS (client certs) | **Optional** via `tls_client_ca_file` on control + agent `tls_cert_file`/`tls_key_file` |

**Recommended production:** HTTPS + bearer token at minimum; mTLS when agents run on untrusted networks.

Plain HTTP + shared token is acceptable only on trusted single-host or private L3 networks (lab / single-node quickstart).

## What holds under adversarial assumptions (MVP)

1. Unauthenticated callers cannot claim jobs or receive JIT material.
2. Agents without free capacity cannot drain the job queue (capacity-aware claim).
3. Stuck assignments eventually clear JIT from control memory and attempt GitHub runner delete.
4. Agent crash mid-job does not leave unbounded host scratch (restart sweep).
5. Job deadline on the agent force-destroys guests that never complete.

## Residual risks

1. **Shared token compromise** — any holder can claim JIT configs for queued jobs. Mitigate with mTLS, short-lived tokens (future), network policy.
2. **In-memory control state** — process restart loses assignment queue; jobs may need re-queue from GitHub. No durable store in MVP.
3. **GitHub runner delete best-effort** — API failures leave offline JIT runners until manual cleanup or next reconcile success.
4. **Agent admin HTTP** — drain/reload uses the same `agent_token`; if `metrics_listen_addr` is public, attackers with the token can drain the pool. Bind `127.0.0.1` and protect with host firewall.
5. **No webhook→agent end-to-end encryption beyond TLS** — operators must terminate TLS correctly.
6. **Guest compromise** — one job can attack its VM only; destroy limits persistence, but host/agent bugs could escalate (agent is root-equivalent for KVM).
7. **No formal secret scrubbing of core dumps / pprof** — JIT may exist briefly in process memory during claim→bind.
8. **Fake VMM / macOS path** — not a production security boundary; production is Linux + Firecracker + KVM.

## mTLS operator path (summary)

1. Create a private CA; issue server cert for control; issue per-agent (or shared agent) client certs.
2. Control: set `tls_cert_file`, `tls_key_file`, `tls_client_ca_file`.
3. Agent: `control_url = "https://…"`, `tls_ca_file` (server CA), `tls_cert_file` + `tls_key_file` (client).
4. Keep `agent_token` as second factor until token rotation is automated.

## Ops signals

- Control: `GET /metrics` — agent registry + assignment status counts (no secrets).
- Agent: `GET /metrics` on `metrics_listen_addr` — pool counts and counters.
- Structured logs: claim/start/finish/timeout/stuck without JIT bodies.

## Conclusion

MVP meets design §7 for the single-tenant self-hosted threat model when operators enable TLS (and preferably mTLS), keep agent admin on loopback, and treat `agent_token` as a high-value secret. Residual risks are acceptable for an MVP with documented operator responsibility; multi-tenant SaaS isolation is out of scope.
