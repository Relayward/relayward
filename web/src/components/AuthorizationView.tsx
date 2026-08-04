import { type FormEvent, type ReactNode, useEffect, useMemo, useState } from "react";
import { KeyRound, ListChecks, Pencil, Plus, Trash2 } from "lucide-react";

import {
  APIError,
  createAuthorization,
  createServiceBinding,
  deleteAuthorization,
  listAuthorizations,
  listNodes,
  listPluginServices,
  listServiceBindings,
  listUsers,
  rotateSubscriptionToken,
  updateServiceBinding,
  updateAuthorization,
  type Authorization,
  type AuthorizationInput,
  type Node,
  type PluginService,
  type ResetKind,
  type User,
  type ServiceBinding,
} from "../api";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";

const gibibyte = 1024 ** 3;
type Translate = ReturnType<typeof useI18n>["t"];

export function AuthorizationsView() {
  const { t, formatDate } = useI18n();
  const [items, setItems] = useState<Authorization[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<Authorization | "new">();
  const [deleting, setDeleting] = useState<Authorization>();
  const [rotating, setRotating] = useState<Authorization>();
  const [servicesFor, setServicesFor] = useState<Authorization>();
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
        <div><p className="eyebrow">{t("Access")}</p><h1 id="authorizations-title">{t("Authorizations")}</h1></div>
        <button
          className="primary-button compact button-with-icon"
          disabled={!canAdd}
          onClick={() => setEditing("new")}
          title={canAdd ? t("Add authorization") : t("A node and user are required")}
          type="button"
        ><Plus size={17} />{t("Add")}</button>
      </div>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="table-frame">
        <table className="resource-table authorization-table">
          <thead><tr><th>{t("User")}</th><th>{t("Node")}</th><th>{t("Traffic")}</th><th>{t("Reset")}</th><th>{t("Expiry")}</th><th>{t("Enforcement")}</th><th>{t("IP slots")}</th><th>{t("Actions")}</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={8} className="empty-cell">{t("Loading...")}</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={8} className="empty-cell">{t("No authorizations have been created.")}</td></tr> : null}
            {items.map((authorization) => (
              <tr key={authorization.id}>
                <td><strong>{userNames.get(authorization.user_id) ?? t("Unknown user")}</strong></td>
                <td className="secondary-cell">{nodeNames.get(authorization.node_id) ?? t("Unknown node")}</td>
                <td><TrafficUsage value={authorization} /></td>
                <td className="secondary-cell">{formatReset(authorization, t)}</td>
                <td className="secondary-cell">{authorization.expires_at ? formatDate(authorization.expires_at) : t("Never")}</td>
                <td><AuthorizationStatus value={authorization} /></td>
                <td><IPStatus value={authorization} /></td>
                <td className="table-actions">
                  <IconAction label={t("Manage services")} onClick={() => setServicesFor(authorization)}><ListChecks size={17} /></IconAction>
                  <IconAction label={t("Rotate subscription token")} onClick={() => setRotating(authorization)}><KeyRound size={17} /></IconAction>
                  <IconAction label={t("Edit authorization")} onClick={() => setEditing(authorization)}><Pencil size={17} /></IconAction>
                  <IconAction label={t("Delete authorization")} danger onClick={() => setDeleting(authorization)}><Trash2 size={17} /></IconAction>
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
          title={t("Delete authorization")}
          subject={`${userNames.get(deleting.user_id) ?? t("Unknown user")} / ${nodeNames.get(deleting.node_id) ?? t("Unknown node")}`}
          action={t("Delete")}
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
          title={t("Rotate subscription token")}
          subject={`${userNames.get(rotating.user_id) ?? t("Unknown user")} / ${nodeNames.get(rotating.node_id) ?? t("Unknown node")}`}
          action={t("Rotate")}
          onClose={() => setRotating(undefined)}
          onConfirm={async () => {
            const result = await rotateSubscriptionToken(rotating.id);
            setRotating(undefined);
            setShownToken({ title: "New subscription link", value: result.subscription_token });
          }}
        />
      ) : null}
      {servicesFor ? (
        <ServicesDialog
          authorization={servicesFor}
          onClose={() => setServicesFor(undefined)}
        />
      ) : null}
      {shownToken ? <TokenDialog title={shownToken.title} token={shownToken.value} onClose={() => setShownToken(undefined)} /> : null}
    </section>
  );
}

function ServicesDialog({ authorization, onClose }: { authorization: Authorization; onClose: () => void }) {
  const { t } = useI18n();
  const [services, setServices] = useState<PluginService[]>([]);
  const [bindings, setBindings] = useState<ServiceBinding[]>([]);
  const [selected, setSelected] = useState<Set<string>>(new Set());
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    Promise.all([listPluginServices(authorization.node_id), listServiceBindings(authorization.id)]).then(([catalog, current]) => {
      if (!active) return;
      setServices(catalog.filter((service) => service.enabled));
      setBindings(current);
      setSelected(new Set(current.filter((binding) => binding.enabled).map(bindingKey)));
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [authorization.id, authorization.node_id]);

  function toggle(key: string) {
    setSelected((current) => {
      const next = new Set(current);
      if (next.has(key)) next.delete(key); else next.add(key);
      return next;
    });
  }

  async function save() {
    setBusy(true);
    setError(undefined);
    try {
      const existing = new Map(bindings.map((binding) => [bindingKey(binding), binding]));
      for (const service of services) {
        const key = bindingKey(service);
        const binding = existing.get(key);
        const enabled = selected.has(key);
        if (binding && binding.enabled !== enabled) {
          await updateServiceBinding(binding.id, enabled);
        } else if (!binding && enabled) {
          await createServiceBinding(authorization.id, {
            plugin_id: service.plugin_id, service_id: service.service_id, enabled: true,
          });
        }
      }
      onClose();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={t("Manage services")} onClose={onClose}>
      <div className="service-picker">
        {loading ? <p className="empty-service">{t("Loading...")}</p> : null}
        {!loading && services.length === 0 ? <p className="empty-service">{t("No services are available on this node.")}</p> : null}
        {services.map((service) => {
          const key = bindingKey(service);
          return (
            <label key={key} className="service-option">
              <input type="checkbox" checked={selected.has(key)} onChange={() => toggle(key)} />
              <span><strong>{service.display_name}</strong><small>{service.plugin_id} / {service.service_id}</small></span>
            </label>
          );
        })}
      </div>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
        <button className="primary-button compact" disabled={busy || loading} onClick={save} type="button">{busy ? t("Saving...") : t("Save")}</button>
      </div>
    </Modal>
  );
}

function bindingKey(value: { plugin_id: string; service_id: string }): string {
  return `${value.plugin_id}\u0000${value.service_id}`;
}

function AuthorizationDialog({ value, nodes, users, onClose, onSaved }: {
  value?: Authorization;
  nodes: Node[];
  users: User[];
  onClose: () => void;
  onSaved: (authorization: Authorization, token?: string) => void;
}) {
  const { t } = useI18n();
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
    <Modal title={value ? t("Edit authorization") : t("Add authorization")} onClose={onClose} width="wide">
      <form onSubmit={submit}>
        <div className="form-grid">
          <SelectField label={t("User")} value={userID} onChange={setUserID} disabled={value !== undefined} options={users.map((user) => ({ value: user.id, label: user.display_name }))} />
          <SelectField label={t("Node")} value={nodeID} onChange={setNodeID} disabled={value !== undefined} options={nodes.map((node) => ({ value: node.id, label: node.name }))} />
          <NumberField label={t("Traffic quota (GiB)")} value={quotaGiB} onChange={setQuotaGiB} min="0" step="0.01" required={false} />
          <SelectField label={t("Reset")} value={resetKind} onChange={(next) => {
            const kind = next as ResetKind;
            setResetKind(kind);
            if ((kind === "weekly" || kind === "monthly" || kind === "interval_days") && resetValue === "") setResetValue(kind === "interval_days" ? "30" : "1");
          }} options={[
            { value: "never", label: t("Never") }, { value: "daily", label: t("Daily") }, { value: "weekly", label: t("Weekly") },
            { value: "monthly", label: t("Monthly") }, { value: "interval_days", label: t("Every N days") },
          ]} />
          {resetKind === "weekly" ? <SelectField label={t("Weekday")} value={resetValue || "1"} onChange={setResetValue} options={weekdayKeys.map((label, index) => ({ value: String(index + 1), label: t(label) }))} /> : null}
          {resetKind === "monthly" ? <NumberField label={t("Day of month")} value={resetValue} onChange={setResetValue} min="1" max="31" step="1" /> : null}
          {resetKind === "interval_days" ? <NumberField label={t("Interval (days)")} value={resetValue} onChange={setResetValue} min="1" max="3650" step="1" /> : null}
          <label className="field"><span>{t("Timezone")}</span><input value={timezone} onChange={(event) => setTimezone(event.target.value)} list="relayward-timezones" required /></label>
          {resetKind === "interval_days" ? <DateTimeField label={t("Period anchor")} value={periodAnchor} onChange={setPeriodAnchor} required /> : null}
          <DateTimeField label={t("Expires")} value={expiresAt} onChange={setExpiresAt} />
          <NumberField label={t("Soft IP limit")} value={softIPLimit} onChange={setSoftIPLimit} min="1" max="1024" step="1" required={false} />
          <NumberField label={t("Activity window (minutes)")} value={activityMinutes} onChange={setActivityMinutes} min="1" max="1440" step="1" />
          <NumberField label={t("Block duration (minutes)")} value={blockMinutes} onChange={setBlockMinutes} min="1" max="10080" step="1" />
        </div>
        <datalist id="relayward-timezones"><option value="UTC" /><option value="Asia/Shanghai" /><option value="Asia/Singapore" /><option value="Europe/London" /><option value="America/New_York" /></datalist>
        <label className="toggle-field form-toggle"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span>{t("Enabled")}</span></label>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
          <button className="primary-button compact" disabled={busy} type="submit">{busy ? t("Saving...") : value ? t("Save") : t("Add authorization")}</button>
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
  const { t } = useI18n();
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
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
        <button className={danger ? "danger-button" : "primary-button compact"} disabled={busy} onClick={confirm} type="button">{busy ? t("Saving...") : action}</button>
      </div>
    </Modal>
  );
}

function TokenDialog({ title, token, onClose }: { title: string; token: string; onClose: () => void }) {
  const { t } = useI18n();
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
    <Modal title={t(title)} onClose={onClose} dismissible={false}>
      <code className="one-time-token">{link}</code>
      <div className="dialog-actions">
        <button className="secondary-button" onClick={copy} type="button">{copied ? t("Copied") : t("Copy")}</button>
        <button className="primary-button compact" onClick={onClose} type="button">{t("Done")}</button>
      </div>
    </Modal>
  );
}

function AuthorizationStatus({ value }: { value: Authorization }) {
  const { t, formatDateTime } = useI18n();
  const status = value.enforcement;
  if (!status) return <span className="inline-status"><span className="status-dot status-dot--warning" />{t("Not reported")}</span>;
  const labels = {
    active: "Active",
    administrator_disabled: "Disabled",
    expired: "Expired",
    quota_exceeded: "Quota reached",
  } as const;
  const tone = status.reason === "active" && status.services_enabled ? "ok"
    : status.reason === "administrator_disabled" ? "muted" : "warning";
  return (
    <span className="runtime-value" title={t("Observed {time}", { time: formatDateTime(status.observed_at) })}>
      <span className="inline-status"><span className={`status-dot status-dot--${tone}`} />{t(labels[status.reason])}</span>
      <small>{t("Generation {generation}", { generation: status.generation })}</small>
    </span>
  );
}

function TrafficUsage({ value }: { value: Authorization }) {
  const { t, formatDateTime } = useI18n();
  const traffic = value.current_traffic;
  if (!traffic) return <span className="secondary-cell">{t("No data / {quota}", { quota: formatQuota(value.traffic_limit_bytes, t) })}</span>;
  const total = traffic.upload_bytes + traffic.download_bytes;
  const periodEnd = traffic.period.ends_at ? formatDateTime(traffic.period.ends_at) : t("No end");
  return (
    <span className="runtime-value" title={t("{start} - {end}; observed {observed}", { start: formatDateTime(traffic.period.starts_at), end: periodEnd, observed: formatDateTime(traffic.observed_at) })}>
      <strong>{formatBytes(total)}</strong>
      <small>{t("of {quota}", { quota: formatQuota(value.traffic_limit_bytes, t) })}</small>
    </span>
  );
}

function IPStatus({ value }: { value: Authorization }) {
  const { t } = useI18n();
  if (value.soft_ip_limit === null) return <span className="secondary-cell">{t("Not limited")}</span>;
  if (!value.enforcement) return <span className="secondary-cell">{t("Not reported / {limit}", { limit: value.soft_ip_limit })}</span>;
  return (
    <span className="runtime-value">
      <strong>{value.enforcement.active_ip_count} / {value.soft_ip_limit}</strong>
      <small>{t("{count} blocked", { count: value.enforcement.blocked_ip_count })}</small>
    </span>
  );
}

function IconAction({ label, danger = false, onClick, children }: { label: string; danger?: boolean; onClick: () => void; children: ReactNode }) {
  return <button className={`icon-button${danger ? " icon-button--danger" : ""}`} aria-label={label} title={label} onClick={onClick} type="button">{children}</button>;
}

const weekdayKeys = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];

function formatQuota(value: number | null, t: Translate): string {
  if (value === null) return t("Unlimited");
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

function formatReset(value: Authorization, t: Translate): string {
  switch (value.reset.kind) {
    case "never": return t("Never");
    case "daily": return t("Daily / {timezone}", { timezone: value.reset.timezone });
    case "weekly": return t("{weekday} / {timezone}", { weekday: t(weekdayKeys[(value.reset.value ?? 1) - 1] ?? "Weekly"), timezone: value.reset.timezone });
    case "monthly": return t("Day {day} / {timezone}", { day: value.reset.value ?? 1, timezone: value.reset.timezone });
    case "interval_days": return t("Every {days} days / {timezone}", { days: value.reset.value ?? 1, timezone: value.reset.timezone });
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
