type Props = {
  /** Org login slug if known (e.g. coatcheckapp); used in deep links */
  orgSlug?: string;
  compact?: boolean;
};

/**
 * Operator guide: create an org-owned GitHub App (required for org repos).
 * User-owned apps often only install on the personal account and never receive
 * workflow_job webhooks for organization repositories.
 */
export function GitHubAppGuide({ orgSlug, compact }: Props) {
  const slug = (orgSlug || "").trim().replace(/^@/, "");
  const newAppUrl = slug
    ? `https://github.com/organizations/${encodeURIComponent(slug)}/settings/apps/new`
    : "https://github.com/organizations/<ORG_LOGIN>/settings/apps/new";
  const appsListUrl = slug
    ? `https://github.com/organizations/${encodeURIComponent(slug)}/settings/apps`
    : "https://github.com/organizations/<ORG_LOGIN>/settings/apps";
  const installedUrl = slug
    ? `https://github.com/organizations/${encodeURIComponent(slug)}/settings/installations`
    : "https://github.com/organizations/<ORG_LOGIN>/settings/installations";

  return (
    <div className={compact ? "" : "mb-4"}>
      <div className="mb-2 text-sm font-semibold tracking-tight">Create the GitHub App on the organization</div>
      <p className="mb-3 max-w-[70ch] text-[13px] text-muted-foreground">
        TemperCI is <strong>not</strong> a public Marketplace app. You create a private App{" "}
        <strong>owned by your GitHub organization</strong>, then install it on that org. Creating the
        App only under your personal account often installs only on your user and never sends{" "}
        <code>workflow_job</code> events for org repos.
      </p>

      <ol className="mb-3 list-decimal pl-5 text-[13px]">
        <li>
          <strong>Open org Developer settings</strong> (you must be an org owner). Prefer:
          <div className="my-1.5 flex flex-wrap items-center gap-x-3 gap-y-2">
            <code className="break-all font-mono text-[11px]">{newAppUrl}</code>
            {slug ? (
              <a href={newAppUrl} target="_blank" rel="noreferrer">
                Open new App
              </a>
            ) : null}
          </div>
          Or list existing apps:{" "}
          <code className="break-all font-mono text-[11px]">{appsListUrl}</code>
          {slug ? (
            <>
              {" "}
              <a href={appsListUrl} target="_blank" rel="noreferrer">
                Open apps
              </a>
            </>
          ) : null}
          <div className="mt-1.5 text-xs text-muted-foreground">
            Use the org <strong>login</strong> from the URL <code>github.com/orgs/LOGIN</code> — not
            the company display name. Replace <code>&lt;ORG_LOGIN&gt;</code> if the links above still
            show a placeholder.
          </div>
        </li>
        <li>
          <strong>New GitHub App</strong> with:
          <ul className="mt-1.5 list-disc pl-4 text-muted-foreground">
            <li>
              <strong>Webhook URL:</strong> <code>https://&lt;public-or-funnel-host&gt;/webhooks/github</code>
            </li>
            <li>
              <strong>Webhook secret:</strong> long random string (paste into TemperCI as webhook secret)
            </li>
            <li>
              <strong>Repository permissions → Actions:</strong> Read-only
            </li>
            <li>
              <strong>Organization permissions → Self-hosted runners:</strong> Read and write
            </li>
            <li>
              <strong>Subscribe to events:</strong> <strong>Workflow job</strong> only (required)
            </li>
          </ul>
        </li>
        <li>
          Create the App → note the numeric <strong>App ID</strong> →{" "}
          <strong>Generate a private key</strong> and download the <code>.pem</code>.
        </li>
        <li>
          <strong>Install App</strong> on <em>this organization</em> (not only your personal user).
          Repository access: <strong>All repositories</strong>, or include every repo that will use{" "}
          <code>runs-on: temperci-…</code>.
          <div className="mt-1.5 text-xs text-muted-foreground">
            Confirm under Org → Settings → GitHub Apps (Installed). You should see TemperCI listed for
            the org: <code className="break-all font-mono text-[11px]">{installedUrl}</code>
            {slug ? (
              <>
                {" "}
                <a href={installedUrl} target="_blank" rel="noreferrer">
                  Open installations
                </a>
              </>
            ) : null}
          </div>
        </li>
        <li>
          In TemperCI, set <strong>github_org</strong> to the org login, paste App ID, webhook secret,
          and PEM. After a workflow runs, App → <strong>Recent Deliveries</strong> should show{" "}
          <code>workflow_job</code> (not only <code>ping</code>).
        </li>
      </ol>

      <div className="rounded-lg bg-primary/15 px-3 py-2.5 text-xs leading-snug text-orange-100">
        <strong>Personal-only install fails for org repos:</strong> If Install App only shows your
        user and the org is missing, do not rely on that install. Create the App under{" "}
        <code>/organizations/&lt;ORG_LOGIN&gt;/settings/apps</code> (this guide) so the org appears by
        default. Org Settings → Installed GitHub Apps has no “Add app” button — install from the App’s
        own <strong>Install App</strong> page after the App exists on the org.
      </div>
    </div>
  );
}
