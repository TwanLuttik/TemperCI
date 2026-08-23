import { useState } from "react";

import { formatAge, type WebhookEndpoint, type WebhookStatus as WebhookInfo } from "../api";
import { StatusBadge } from "./status-badge";
import { Button } from "@/components/ui/button";

function webhookReceivedLabel(event?: string): string {
  if (event === "workflow_job") return "received job";
  if (event === "ping") return "received ping";
  return "received webhook";
}

function CopyURL({ url }: { url: string }) {
  const [copied, setCopied] = useState(false);
  return (
    <Button
      type="button"
      variant="outline"
      size="sm"
      className="h-7 shrink-0 px-2 font-mono text-[11px]"
      onClick={() => {
        void navigator.clipboard.writeText(url).then(() => {
          setCopied(true);
          window.setTimeout(() => setCopied(false), 1500);
        });
      }}
    >
      {copied ? "Copied" : "Copy"}
    </Button>
  );
}

function EndpointRow({ ep, primary }: { ep: WebhookEndpoint; primary?: boolean }) {
  return (
    <div className={primary ? "space-y-1.5" : "space-y-1"}>
      <div className="flex flex-wrap items-center gap-2">
        <span className="text-[13px] font-medium">{ep.label || ep.kind}</span>
        <StatusBadge tone={ep.public ? "ok" : "warn"}>{ep.public ? "GitHub-reachable" : "private"}</StatusBadge>
      </div>
      <div className="flex items-start gap-2">
        <code className="min-w-0 flex-1 break-all font-mono text-[11px]">{ep.url}</code>
        <CopyURL url={ep.url} />
      </div>
      {ep.detail ? <p className="text-xs text-muted-foreground">{ep.detail}</p> : null}
    </div>
  );
}

export function WebhookStatus({
  webhook,
  compact,
}: {
  webhook?: WebhookInfo | null;
  compact?: boolean;
}) {
  const received = Boolean(webhook?.received);
  const event = webhook?.last_event || "";
  const suggested = webhook?.endpoints?.find((e) => e.url === webhook.suggested_url) || webhook?.endpoints?.[0];
  const extras = (webhook?.endpoints || []).filter((e) => e.url !== suggested?.url);

  return (
    <div className={compact ? "space-y-2" : "space-y-3"}>
      <div className="flex flex-wrap items-center gap-2">
        <StatusBadge tone={received ? "ok" : "warn"}>
          {received ? webhookReceivedLabel(event) : "waiting for a job"}
        </StatusBadge>
        {received && webhook?.last_at ? (
          <span className="font-mono text-[11px] text-muted-foreground">{formatAge(webhook.last_at)}</span>
        ) : null}
      </div>
      {received ? (
        <p className="text-[13px] text-muted-foreground">
          GitHub is delivering webhooks to this host
          {event === "workflow_job" ? " — a job reached TemperCI." : event === "ping" ? " (App ping)." : "."}
        </p>
      ) : (
        <p className="text-[13px] text-muted-foreground">
          Paste the URL below into the GitHub App webhook field, then dispatch a workflow with{" "}
          <code className="font-mono text-[11px]">runs-on: temperci-…</code>. This flips to received when
          that job arrives — no ping redelivery needed.
        </p>
      )}
      {suggested ? <EndpointRow ep={suggested} primary /> : (
        <p className="text-xs text-muted-foreground">
          No Tailscale Funnel or Cloudflare Tunnel detected. GitHub needs a public HTTPS URL such as{" "}
          <code className="font-mono text-[11px]">https://&lt;host&gt;/webhooks/github</code>.
        </p>
      )}
      {extras.length > 0 ? (
        <div className="space-y-2 border-t border-border pt-2">
          {extras.map((ep) => (
            <EndpointRow key={`${ep.kind}-${ep.url}`} ep={ep} />
          ))}
        </div>
      ) : null}
    </div>
  );
}
