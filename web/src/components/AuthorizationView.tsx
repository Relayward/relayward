import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from "react";
import { KeyRound, Pencil, Plus, Trash2 } from "lucide-react";

import {
  APIError,
  createAuthorization,
  deleteAuthorization,
  listAuthorizations,
  listNodes,
  listUsers,
  rotateSubscriptionToken,
  updateAuthorization,
  type Authorization,
  type AuthorizationInput,
  type Node,
  type ResetKind,
  type User,
} from "../api";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";

const gibibyte = 1024 ** 3;

export function AuthorizationsView() {
  const [items, setItems] = useState<Authorization[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<Authorization | "new">();
  const [deleting, setDeleting] = useState<Authorization>();
  const [rotating, setRotating] = useState<Authorization>();
  const [shownToken, setShownToken] = useState<{ title: string; value: string }>();

  useEffect(() => {
    let active = true;
    Promise.all([listAuthorizations(), listNodes(), listUsers()]).then(([authorizations, nodeItems, userItems]) => {
      if (!active) return;
      setItems(authorizations);
      setNodes(nodeItems);
      setUsers(userItems);
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, []);

  const nodeNames = useMemo(() => new Map(nodes.map((node) => [node.id, node.name])), [nodes]);
  const userNames = useMemo(() => new Map(users.map((user) => [user.id, user.display_name])), [users]);
  const canAdd = nodes.length > 0 && users.length > 0;

  return (
    <section aria-labelledby="authorizations-title">
      <div className="section-heading">
        <div><p className="eyebrow">Access</p><h1 id="authorizations-title">Authorizations</h1></div>
        <button
          className="primary-button compact button-with-icon"
          disabled={!canAdd}
          onClick={() => setEditing("new")}
          title={canAdd ? "Add authorization" : "A node and user are required"}
          type="button"
        ><Plus size={17} />Add</button>
      </div>
      <FormError message={error} />
      <div className="table-frame">
        <table className="resource-table authorization-table">
          <thead><tr><th>User</th><th>Node</th><th>Traffic</th><th>Reset</th><th>Expiry</th><th>Enforcement</th><th>IP slots</th><th>Actions</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={8} className="empty-cell">Loading...</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={8} className="empty-cell">No authorizations have been created.</td></tr> : null}
            {items.map((authorization) => (
              <tr key={authorization.id}>
                <td><strong>{userNames.get(authorization.user_id) ?? "Unknown user"}</strong></td>
                <td className="secondary-cell">{nodeNames.get(authorization.node_id) ?? "Unknown node"}</td>
                <td><TrafficUsage value={authorization} /></td>
                <td className="secondary-cell">{formatReset(authorization)}</td>
                <td className="secondary-cell">{authorization.expires_at ? new Date(authorization.expires_at).toLocaleDateString() : "Never"}</td>
                <td><AuthorizationStatus value={authorization} /></td>
                <td><IPStatus value={authorization} /></td>
                <td className="table-actions">
                  <IconAction label="Rotate subscription token" onClick={() => setRotating(authorization)}><KeyRound size={17} /></IconAction>
                  <IconAction label="Edit authorization" onClick={() => setEditing(authorization)}><Pencil size={17} /></IconAction>
                  <IconAction label="Delete authorization" danger onClick={() => setDeleting(authorization)}><Trash2 size={17} /></IconAction>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {editing ? (
        <AuthorizationDialog
          value={editing === "new" ? undefined : editing}
          nodes={nodes}
          users={users}
          onClose={() => setEditing(undefined)}
          onSaved={(authorization, token) => {
            setItems((current) => editing === "new" ? [authorization, ...current] : current.map((item) => item.id === authorization.id ? authorization : item));
            setEditing(undefined);
            if (token) setShownToken({ title: "Subscription link", value: token });
          }}
        />
      ) : null}
      {deleting ? (
        <ConfirmationDialog
          title="Delete authorization"
          subject={`${userNames.get(deleting.user_id) ?? "Unknown user"} / ${nodeNames.get(deleting.node_id) ?? "Unknown node"}`}
          action="Delete"
          danger
          onClose={() => setDeleting(undefined)}
          onConfirm={async () => {
            await deleteAuthorization(deleting.id);
            setItems((current) => current.filter((item) => item.id !== deleting.id));
            setDeleting(undefined);
          }}
        />
      ) : null}
      {rotating ? (
        <ConfirmationDialog
          title="Rotate subscription token"
          subject={`${userNames.get(rotating.user_id) ?? "Unknown user"} / ${nodeNames.get(rotating.node_id) ?? "Unknown node"}`}
          action="Rotate"
          onClose={() => setRotating(undefined)}
          onConfirm={async () => {
            const result = await rotateSubscriptionToken(rotating.id);
            setRotating(undefined);
            setShownToken({ title: "New subscription link", value: result.subscription_token });
          }}
        />
      ) : null}
      {shownToken ? <TokenDialog title={shownToken.title} token={shownToken.value} onClose={() => setShownToken(undefined)} /> : null}
    </section>
  );
}

function AuthorizationDialog({ value, nodes, users, onClose, onSaved }: {
  value?: Authorization;
  nodes: Node[];
  users: User[];
  onClose: () => void;
  onSaved: (authorization: Authorization, token?: string) => void;
}) {
  const [userID, setUserID] = useState(value?.user_id ?? users[0]?.id ?? "");
  const [nodeID, setNodeID] = useState(value?.node_id ?? nodes[0]?.id ?? "");
  const [enabled, setEnabled] = useState(value?.enabled ?? true);
  const [quotaGiB, setQuotaGiB] = useState(value?.traffic_limit_bytes === null || value === undefined ? "" : String(value.traffic_limit_bytes / gibibyte));
  const [resetKind, setResetKind] = useState<ResetKind>(value?.reset.kind ?? "never");
  const [resetValue, setResetValue] = useState(value?.reset.value === null || value === undefined ? "" : String(value.reset.value));
  const [timezone, setTimezone] = useState(value?.reset.timezone ?? "UTC");
  const [periodAnchor, setPeriodAnchor] = useState(toLocalInput(value?.reset.period_anchor));
  const [expiresAt, setExpiresAt] = useState(toLocalInput(value?.expires_at));
  const [softIPLimit, setSoftIPLimit] = useState(value?.soft_ip_limit === null || value === undefined ? "" : String(value.soft_ip_limit));
  const [activityMinutes, setActivityMinutes] = useState(String((value?.activity_window_seconds ?? 600) / 60));
  const [blockMinutes, setBlockMinutes] = useState(String((value?.block_duration_seconds ?? 1800) / 60));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(undefined);
    const quota = optionalNumber(quotaGiB);
    const resetNumber = optionalInteger(resetValue);
    const softLimit = optionalInteger(softIPLimit);
    const activity = Number(activityMinutes);
    const block = Number(blockMinutes);
    if (quota === undefined || resetNumber === undefined || softLimit === undefined || !Number.isFinite(activity) || !Number.isFinite(block)) {
      setError("Numeric values are invalid.");
      return;
    }
    const quotaBytes = quota === null ? null : Math.round(quota * gibibyte);
    if (quotaBytes !== null && !Number.isSafeInteger(quotaBytes)) {
      setError("Traffic quota is too large.");
      return;
    }
    const input: AuthorizationInput = {
      user_id: userID,
      node_id: nodeID,
      enabled,
      traffic_limit_bytes: quotaBytes,
      reset: {
        kind: resetKind,
        value: resetKind === "weekly" || resetKind === "monthly" || resetKind === "interval_days" ? resetNumber ?? 1 : null,
        timezone,
        period_anchor: resetKind === "interval_days" ? fromLocalInput(periodAnchor) : null,
      },
      expires_at: fromLocalInput(expiresAt),
      soft_ip_limit: softLimit,
      activity_window_seconds: Math.round(activity * 60),
      block_duration_seconds: Math.round(block * 60),
    };
    setBusy(true);
    try {
      if (value) {
        onSaved(await updateAuthorization(value.id, input));
      } else {
        const created = await createAuthorization(input);
        onSaved(created.authorization, created.subscription_token);
      }
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={value ? "Edit authorization" : "Add authorization"} onClose={onClose} width="wide">
      <form onSubmit={submit}>
        <div className="form-grid">
          <SelectField label="User" value={userID} onChange={setUserID} disabled={value !== undefined} options={users.map((user) => ({ value: user.id, label: user.display_name }))} />
          <SelectField label="Node" value={nodeID} onChange={setNodeID} disabled={value !== undefined} options={nodes.map((node) => ({ value: node.id, label: node.name }))} />
          <NumberField label="Traffic quota (GiB)" value={quotaGiB} onChange={setQuotaGiB} min="0" step="0.01" required={false} />
          <SelectField label="Reset" value={resetKind} onChange={(next) => {
            const kind = next as ResetKind;
            setResetKind(kind);
            if ((kind === "weekly" || kind === "monthly" || kind === "interval_days") && resetValue === "") setResetValue(kind === "interval_days" ? "30" : "1");
          }} options={[
            { value: "never", label: "Never" }, { value: "daily", label: "Daily" }, { value: "weekly", label: "Weekly" },
            { value: "monthly", label: "Monthly" }, { value: "interval_days", label: "Every N days" },
          ]} />
          {resetKind === "weekly" ? <SelectField label="Weekday" value={resetValue || "1"} onChange={setResetValue} options={weekdays} /> : null}
          {resetKind === "monthly" ? <NumberField label="Day of month" value={resetValue} onChange={setResetValue} min="1" max="31" step="1" /> : null}
          {resetKind === "interval_days" ? <NumberField label="Interval (days)" value={resetValue} onChange={setResetValue} min="1" max="3650" step="1" /> : null}
          <label className="field"><span>Timezone</span><input value={timezone} onChange={(event) => setTimezone(event.target.value)} list="relayward-timezones" required /></label>
          {resetKind === "interval_days" ? <DateTimeField label="Period anchor" value={periodAnchor} onChange={setPeriodAnchor} required /> : null}
          <DateTimeField label="Expires" value={expiresAt} onChange={setExpiresAt} />
          <NumberField label="Soft IP limit" value={softIPLimit} onChange={setSoftIPLimit} min="1" max="1024" step="1" required={false} />
          <NumberField label="Activity window (minutes)" value={activityMinutes} onChange={setActivityMinutes} min="1" max="1440" step="1" />
          <NumberField label="Block duration (minutes)" value={blockMinutes} onChange={setBlockMinutes} min="1" max="10080" step="1" />
        </div>
        <datalist id="relayward-timezones"><option value="UTC" /><option value="Asia/Shanghai" /><option value="Asia/Singapore" /><option value="Europe/London" /><option value="America/New_York" /></datalist>
        <label className="toggle-field form-toggle"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span>Enabled</span></label>
        <FormError message={error} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">Cancel</button>
          <button className="primary-button compact" disabled={busy} type="submit">{busy ? "Saving..." : value ? "Save" : "Add authorization"}</button>
        </div>
      </form>
    </Modal>
  );
}

function SelectField({ label, value, onChange, options, disabled = false }: {
  label: string; value: string; onChange: (value: string) => void; options: { value: string; label: string }[]; disabled?: boolean;
}) {
  return <label className="field"><span>{label}</span><select value={value} onChange={(event) => onChange(event.target.value)} disabled={disabled} required>{options.map((option) => <option key={option.value} value={option.value}>{option.label}</option>)}</select></label>;
}

function NumberField({ label, value, onChange, min, max, step, required = true }: {
  label: string; value: string; onChange: (value: string) => void; min: string; max?: string; step: string; required?: boolean;
}) {
  return <label className="field"><span>{label}</span><input type="number" value={value} onChange={(event) => onChange(event.target.value)} min={min} max={max} step={step} required={required} /></label>;
}

function DateTimeField({ label, value, onChange, required = false }: { label: string; value: string; onChange: (value: string) => void; required?: boolean }) {
  return <label className="field"><span>{label}</span><input type="datetime-local" value={value} onChange={(event) => onChange(event.target.value)} required={required} /></label>;
}

function ConfirmationDialog({ title, subject, action, danger = false, onClose, onConfirm }: {
  title: string; subject: string; action: string; danger?: boolean; onClose: () => void; onConfirm: () => Promise<void>;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  async function confirm() {
    setBusy(true);
    setError(undefined);
    try {
      await onConfirm();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }
  return (
    <Modal title={title} onClose={onClose}>
      <p className="confirmation-name">{subject}</p>
      <FormError message={error} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">Cancel</button>
        <button className={danger ? "danger-button" : "primary-button compact"} disabled={busy} onClick={confirm} type="button">{busy ? "Saving..." : action}</button>
      </div>
    </Modal>
  );
}

function TokenDialog({ title, token, onClose }: { title: string; token: string; onClose: () => void }) {
  const [copied, setCopied] = useState(false);
  const link = new URL(`/s/${encodeURIComponent(token)}`, window.location.origin).toString();
  async function copy() {
    try {
      await navigator.clipboard.writeText(link);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }
  return (
    <Modal title={title} onClose={onClose} dismissible={false}>
      <code className="one-time-token">{link}</code>
      <div className="dialog-actions">
        <button className="secondary-button" onClick={copy} type="button">{copied ? "Copied" : "Copy"}</button>
        <button className="primary-button compact" onClick={onClose} type="button">Done</button>
      </div>
    </Modal>
  );
}

function AuthorizationStatus({ value }: { value: Authorization }) {
  const status = value.enforcement;
  if (!status) return <span className="inline-status"><span className="status-dot status-dot--warning" />Not reported</span>;
  const labels = {
    active: "Active",
    administrator_disabled: "Disabled",
    expired: "Expired",
    quota_exceeded: "Quota reached",
  } as const;
  const tone = status.reason === "active" && status.services_enabled ? "ok"
    : status.reason === "administrator_disabled" ? "muted" : "warning";
  return (
    <span className="runtime-value" title={`Observed ${new Date(status.observed_at).toLocaleString()}`}>
      <span className="inline-status"><span className={`status-dot status-dot--${tone}`} />{labels[status.reason]}</span>
      <small>Generation {status.generation}</small>
    </span>
  );
}

function TrafficUsage({ value }: { value: Authorization }) {
  const traffic = value.current_traffic;
  if (!traffic) return <span className="secondary-cell">No data / {formatQuota(value.traffic_limit_bytes)}</span>;
  const total = traffic.upload_bytes + traffic.download_bytes;
  const periodEnd = traffic.period.ends_at ? new Date(traffic.period.ends_at).toLocaleString() : "No end";
  return (
    <span className="runtime-value" title={`${new Date(traffic.period.starts_at).toLocaleString()} - ${periodEnd}; observed ${new Date(traffic.observed_at).toLocaleString()}`}>
      <strong>{formatBytes(total)}</strong>
      <small>of {formatQuota(value.traffic_limit_bytes)}</small>
    </span>
  );
}

function IPStatus({ value }: { value: Authorization }) {
  if (value.soft_ip_limit === null) return <span className="secondary-cell">Not limited</span>;
  if (!value.enforcement) return <span className="secondary-cell">Not reported / {value.soft_ip_limit}</span>;
  return (
    <span className="runtime-value">
      <strong>{value.enforcement.active_ip_count} / {value.soft_ip_limit}</strong>
      <small>{value.enforcement.blocked_ip_count} blocked</small>
    </span>
  );
}

function IconAction({ label, danger = false, onClick, children }: { label: string; danger?: boolean; onClick: () => void; children: ReactNode }) {
  return <button className={`icon-button${danger ? " icon-button--danger" : ""}`} aria-label={label} title={label} onClick={onClick} type="button">{children}</button>;
}

const weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"].map((label, index) => ({ value: String(index + 1), label }));

function formatQuota(value: number | null): string {
  if (value === null) return "Unlimited";
  const gib = value / gibibyte;
  return `${Number.isInteger(gib) ? gib : gib.toFixed(2)} GiB`;
}

function formatBytes(value: number): string {
  if (value < 1024) return `${value} B`;
  const units = ["KiB", "MiB", "GiB", "TiB", "PiB"];
  let current = value;
  let unit = -1;
  do {
    current /= 1024;
    unit++;
  } while (current >= 1024 && unit < units.length - 1);
  return `${current >= 10 ? current.toFixed(1) : current.toFixed(2)} ${units[unit]}`;
}

function formatReset(value: Authorization): string {
  switch (value.reset.kind) {
    case "never": return "Never";
    case "daily": return `Daily / ${value.reset.timezone}`;
    case "weekly": return `${weekdays[(value.reset.value ?? 1) - 1]?.label ?? "Weekly"} / ${value.reset.timezone}`;
    case "monthly": return `Day ${value.reset.value} / ${value.reset.timezone}`;
    case "interval_days": return `Every ${value.reset.value} days / ${value.reset.timezone}`;
  }
}

function optionalNumber(value: string): number | null | undefined {
  if (value.trim() === "") return null;
  const parsed = Number(value);
  return Number.isFinite(parsed) && parsed >= 0 ? parsed : undefined;
}

function optionalInteger(value: string): number | null | undefined {
  if (value.trim() === "") return null;
  const parsed = Number(value);
  return Number.isSafeInteger(parsed) ? parsed : undefined;
}

function toLocalInput(value?: string | null): string {
  if (!value) return "";
  const date = new Date(value);
  const offset = date.getTimezoneOffset() * 60_000;
  return new Date(date.getTime() - offset).toISOString().slice(0, 16);
}

function fromLocalInput(value: string): string | null {
  return value ? new Date(value).toISOString() : null;
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
