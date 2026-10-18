import type { Dispatch, ReactNode, SetStateAction } from "react";
import type { Capability } from "../api";
import { newDraftGrant, type DraftGrant } from "../grants";
import { GrantCard } from "./GrantCard";

interface Props {
  grants: DraftGrant[];
  onGrantsChange: Dispatch<SetStateAction<DraftGrant[]>>;
  // Existing groups / sites, offered as suggestions in the target field.
  groups: string[];
  sites: string[];
  base: string;
  // Whether the user ticked "I understand the risk" for a global grant.
  ack: boolean;
  onAckChange: (ack: boolean) => void;
  // What the grants are for, used in the global-grant warning.
  subject: "key" | "connector";
  // Permission checkboxes offered on every card; defaults to all capabilities.
  capabilities?: Capability[];
  // Explanatory text under the "Access grants" label.
  hint?: ReactNode;
}

// GrantsEditor is the list of grant cards shared by the API key form and the
// MCP consent screen: add / remove grants, pick a scope (group, site, or all
// sites) and permissions for each, and acknowledge the risk of a global grant.
// Validation lives in grants.ts (validateGrants) so each screen can word its
// own empty-list message.
export function GrantsEditor({
  grants,
  onGrantsChange,
  groups,
  sites,
  base,
  ack,
  onAckChange,
  subject,
  capabilities,
  hint = "Each grant gives a set of permissions on a group, a single site, or all sites.",
}: Props) {
  const anyGlobal = grants.some((g) => g.type === "global");

  const patchGrant = (id: number, patch: Partial<DraftGrant>) =>
    onGrantsChange((gs) => gs.map((g) => (g.id === id ? { ...g, ...patch } : g)));
  const addGrant = () => onGrantsChange((gs) => [...gs, newDraftGrant()]);
  const removeGrant = (id: number) => onGrantsChange((gs) => gs.filter((g) => g.id !== id));

  return (
    <div className="grants-section">
      <div className="grants-head">
        <span className="field-label">Access grants</span>
        <span className="field-hint muted">{hint}</span>
      </div>

      {grants.map((g, i) => (
        <GrantCard
          key={g.id}
          grant={g}
          index={i}
          groups={groups}
          sites={sites}
          base={base}
          capabilities={capabilities}
          removable={grants.length > 1}
          onChange={(patch) => patchGrant(g.id, patch)}
          onRemove={() => removeGrant(g.id)}
        />
      ))}

      <button type="button" className="small add-grant" onClick={addGrant}>
        + Add grant
      </button>

      {anyGlobal && (
        <div className="global-warning">
          <p>
            ⚠️ <strong>This {subject} has a global grant.</strong> It can act on{" "}
            <strong>every site</strong> on this instance — not just one group or site.
          </p>
          <label className="checkbox">
            <input type="checkbox" checked={ack} onChange={(e) => onAckChange(e.target.checked)} />
            I understand the risk of granting access to all sites.
          </label>
        </div>
      )}
    </div>
  );
}
