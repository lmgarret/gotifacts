import { useEffect, useMemo, useState } from "react";
import { api, type ConsentInfo, type Site } from "../api";
import { deriveTargets, newDraftGrant, toGrant, validateGrants, type DraftGrant } from "../grants";
import { GrantsEditor } from "./GrantsEditor";
import { Logo } from "./Logo";

// ConsentView is the MCP OAuth consent screen, served at /mcp/oauth/authorize.
// It is standalone (no portal navigation): the OAuth request parameters come
// from the query string, the screen's data from GET /api/mcp/consent, and the
// decision goes to POST /api/mcp/consent, whose answer is the URL to send the
// browser to. The Go handler has already validated the request before serving
// this page, so a failure here is rare (e.g. an expired session or CSRF token).
export function ConsentView() {
  const search = window.location.search;
  const [info, setInfo] = useState<ConsentInfo | null>(null);
  const [error, setError] = useState<string | null>(null);
  const [sites, setSites] = useState<Site[]>([]);

  useEffect(() => {
    api
      .getConsent(search)
      .then(setInfo)
      .catch((e: Error) => setError(e.message));
    // Existing groups and sites feed the target suggestions; they are optional.
    api
      .listSites()
      .then((r) => setSites(r.sites))
      .catch(() => {});
  }, [search]);

  let body;
  if (error) {
    body = (
      <>
        <h2>This request can’t be authorized</h2>
        <p className="error">{error}</p>
        <p className="muted">Go back to the application and start the connection again.</p>
      </>
    );
  } else if (!info) {
    body = <p className="muted">Loading…</p>;
  } else {
    body = <ConsentForm info={info} sites={sites} params={new URLSearchParams(search)} />;
  }

  return (
    <div className="consent">
      <div className="consent-card">
        <div className="brand-logo">
          <Logo />
        </div>
        {body}
      </div>
    </div>
  );
}

interface FormProps {
  info: ConsentInfo;
  sites: Site[];
  params: URLSearchParams;
}

function ConsentForm({ info, sites, params }: FormProps) {
  const [grants, setGrants] = useState<DraftGrant[]>(() => [
    newDraftGrant({ target: info.default_target, permissions: ["publish"] }),
  ]);
  const [ack, setAck] = useState(false);
  const [error, setError] = useState<string | null>(null);
  const [busy, setBusy] = useState(false);
  const { groups, sites: siteDirs } = useMemo(() => deriveTargets(sites), [sites]);

  const anyGlobal = grants.some((g) => g.type === "global");

  const decide = async (action: "approve" | "deny") => {
    if (action === "approve") {
      const v = validateGrants(grants, ack, "Add at least one grant.");
      if (v) {
        setError(v);
        return;
      }
    }
    setError(null);
    setBusy(true);
    try {
      const { redirect_url } = await api.submitConsent({
        client_id: params.get("client_id") ?? "",
        redirect_uri: params.get("redirect_uri") ?? "",
        code_challenge: params.get("code_challenge") ?? "",
        code_challenge_method: params.get("code_challenge_method") ?? "",
        state: params.get("state") ?? "",
        csrf: info.csrf,
        action,
        grants:
          action === "approve" ? grants.map((g) => toGrant(g.type, g.target, g.permissions)) : [],
        acknowledge_global: action === "approve" && anyGlobal && ack,
      });
      // Leave busy set: the browser is navigating away.
      window.location.assign(redirect_url);
    } catch (err) {
      setError((err as Error).message);
      setBusy(false);
    }
  };

  return (
    // Deliberately not a <form>: pressing Enter in a target field must never
    // approve the connector. Approval is an explicit click.
    <div>
      <div className="consent-user">
        <span className="consent-user-dot" />
        {info.user}
      </div>
      <h2>Authorize MCP connector</h2>
      <p className="muted consent-lead">
        <strong>{info.client_name}</strong> wants to access sites on <code>{info.base_domain}</code>.
        Choose what it may do — it can never act outside the grants you approve.
      </p>

      <GrantsEditor
        grants={grants}
        onGrantsChange={setGrants}
        groups={groups}
        sites={siteDirs}
        base={info.base_domain}
        ack={ack}
        onAckChange={setAck}
        subject="connector"
        capabilities={info.capabilities}
        hint="Each grant gives the connector a set of permissions on a group, a single site, or all sites."
      />

      {error && <p className="error">{error}</p>}

      <div className="actions consent-actions">
        <button
          type="button"
          className="consent-approve"
          disabled={busy || (anyGlobal && !ack)}
          onClick={() => void decide("approve")}
        >
          Approve
        </button>
        <button type="button" className="ghost" disabled={busy} onClick={() => void decide("deny")}>
          Deny
        </button>
      </div>
    </div>
  );
}
