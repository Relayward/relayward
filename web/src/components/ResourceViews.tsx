import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { KeyRound, Pencil, Plus, RefreshCw, ShieldX, Trash2 } from "lucide-react";

import {
  APIError,
  createNode,
  createNodeRegistrationToken,
  createUser,
  deleteNode,
  deleteUser,
  getLatestAgentUpdate,
  listNodes,
  listUsers,
  requestAgentUpdate,
  revokeNodeCredential,
  updateNode,
  updateUser,
  type AgentUpdate,
  type Node,
  type NodeInput,
  type NodeRegistrationToken,
  type User,
  type UserInput,
} from "../api";
import { useI18n } from "../i18n";
import { Field, FormError } from "./AuthScreen";
import { Modal } from "./Modal";

export function NodesView() {
  const { t } = useI18n();
  const [items, setItems] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<Node | "new">();
  const [deleting, setDeleting] = useState<Node>();
  const [revoking, setRevoking] = useState<Node>();
  const [token, setToken] = useState<NodeRegistrationToken>();
  const [tokenNode, setTokenNode] = useState<Node>();
  const [updatingNodeID, setUpdatingNodeID] = useState<string>();
  const [updates, setUpdates] = useState<Record<string, AgentUpdate | null>>({});
  const updating = items.find((node) => node.id === updatingNodeID);

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const values = await listNodes();
        const latest = await Promise.all(values.map(async (node) => [node.id, await getLatestAgentUpdate(node.id)] as const));
        if (active) {
          setItems(values);
          setUpdates(Object.fromEntries(latest));
          setError(undefined);
        }
      } catch (cause) {
        if (active) setError(errorMessage(cause));
      } finally {
        if (active) setLoading(false);
      }
    };
    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, 10_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, []);

  async function issueToken(node: Node) {
    setError(undefined);
    try {
      setToken(await createNodeRegistrationToken(node.id));
      setTokenNode(node);
    } catch (cause) {
      setError(errorMessage(cause));
    }
  }

  return (
    <section aria-labelledby="nodes-title">
      <ResourceHeading eyebrow={t("Infrastructure")} title={t("Nodes")} id="nodes-title" onAdd={() => setEditing("new")} />
      <ResourceTable headers={["Name", "Address", "Agent", "Version", "Policy", "Update", "State", "Actions"].map((value) => t(value))} loading={loading} empty={t("No nodes have been created.")} error={error}>
        {items.map((node) => (
          <tr key={node.id}>
            <td data-label={t("Name")}><strong>{node.name}</strong></td>
            <td data-label={t("Address")} className="secondary-cell">{node.public_address || t("Not set")}</td>
            <td data-label={t("Agent")}><Status value={t(agentStatusLabel(node.agent_status))} tone={agentStatusTone(node.agent_status)} /></td>
            <td data-label={t("Version")} className="secondary-cell">{node.agent_version || t("Not reported")}</td>
            <td data-label={t("Policy")}><NodePolicyStatus node={node} /></td>
            <td data-label={t("Update")}><AgentUpdateStatus value={updates[node.id]} /></td>
            <td data-label={t("State")}><Status value={node.enabled ? t("Enabled") : t("Disabled")} tone={node.enabled ? "ok" : "muted"} /></td>
            <td className="table-actions">
              <IconAction label={t("Create registration token")} onClick={() => issueToken(node)}><KeyRound size={17} /></IconAction>
              <IconAction
                label={t("Update Agent")}
                title={translatedOptional(agentUpdateUnavailable(node), t)}
                disabled={agentUpdateUnavailable(node) !== undefined}
                onClick={() => setUpdatingNodeID(node.id)}
              ><RefreshCw size={17} /></IconAction>
              <IconAction
                label={t("Revoke Agent credential")}
                title={node.registered_at === null ? t("No active Agent credential") : undefined}
                danger
                disabled={node.registered_at === null}
                onClick={() => setRevoking(node)}
              ><ShieldX size={17} /></IconAction>
              <IconAction label={t("Edit node")} onClick={() => setEditing(node)}><Pencil size={17} /></IconAction>
              <IconAction label={t("Delete node")} danger onClick={() => setDeleting(node)}><Trash2 size={17} /></IconAction>
            </td>
          </tr>
        ))}
      </ResourceTable>
      {editing ? (
        <NodeDialog
          value={editing === "new" ? undefined : editing}
          onClose={() => setEditing(undefined)}
          onSaved={(node) => {
            setItems((current) => editing === "new" ? [...current, node].sort(byName) : current.map((item) => item.id === node.id ? node : item).sort(byName));
            setEditing(undefined);
          }}
        />
      ) : null}
      {deleting ? (
        <ConfirmAction
          title={t("Delete node")}
          name={deleting.name}
          action={t("Delete")}
          onClose={() => setDeleting(undefined)}
          onConfirm={async () => {
            await deleteNode(deleting.id);
            setItems((current) => current.filter((item) => item.id !== deleting.id));
            setDeleting(undefined);
          }}
        />
      ) : null}
      {revoking ? (
        <ConfirmAction
          title={t("Revoke Agent credential")}
          name={revoking.name}
          action={t("Revoke")}
          onClose={() => setRevoking(undefined)}
          onConfirm={async () => {
            const node = await revokeNodeCredential(revoking.id);
            setItems((current) => current.map((item) => item.id === node.id ? node : item).sort(byName));
            setRevoking(undefined);
          }}
        />
      ) : null}
      {token && tokenNode ? <TokenDialog node={tokenNode} token={token} onClose={() => { setToken(undefined); setTokenNode(undefined); }} /> : null}
      {updating ? (
        <AgentUpdateDialog
          node={updating}
          latest={updates[updating.id]}
          onClose={() => setUpdatingNodeID(undefined)}
          onUpdated={(value) => setUpdates((current) => ({ ...current, [value.node_id]: value }))}
        />
      ) : null}
    </section>
  );
}

export function UsersView() {
  const { t } = useI18n();
  const [items, setItems] = useState<User[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<User | "new">();
  const [deleting, setDeleting] = useState<User>();

  useEffect(() => {
    let active = true;
    listUsers().then((values) => {
      if (active) setItems(values);
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, []);

  return (
    <section aria-labelledby="users-title">
      <ResourceHeading eyebrow={t("Access")} title={t("Users")} id="users-title" onAdd={() => setEditing("new")} />
      <ResourceTable headers={["Name", "Email", "Telegram", "Actions"].map((value) => t(value))} loading={loading} empty={t("No users have been created.")} error={error}>
        {items.map((user) => (
          <tr key={user.id}>
            <td data-label={t("Name")}><strong>{user.display_name}</strong></td>
            <td data-label={t("Email")} className="secondary-cell">{user.email || t("Not set")}</td>
            <td data-label={t("Telegram")} className="secondary-cell">{user.telegram || t("Not set")}</td>
            <td className="table-actions">
              <IconAction label={t("Edit user")} onClick={() => setEditing(user)}><Pencil size={17} /></IconAction>
              <IconAction label={t("Delete user")} danger onClick={() => setDeleting(user)}><Trash2 size={17} /></IconAction>
            </td>
          </tr>
        ))}
      </ResourceTable>
      {editing ? (
        <UserDialog
          value={editing === "new" ? undefined : editing}
          onClose={() => setEditing(undefined)}
          onSaved={(user) => {
            setItems((current) => editing === "new" ? [...current, user].sort(byDisplayName) : current.map((item) => item.id === user.id ? user : item).sort(byDisplayName));
            setEditing(undefined);
          }}
        />
      ) : null}
      {deleting ? (
        <ConfirmAction
          title={t("Delete user")}
          name={deleting.display_name}
          action={t("Delete")}
          onClose={() => setDeleting(undefined)}
          onConfirm={async () => {
            await deleteUser(deleting.id);
            setItems((current) => current.filter((item) => item.id !== deleting.id));
            setDeleting(undefined);
          }}
        />
      ) : null}
    </section>
  );
}

function ResourceHeading({ eyebrow, title, id, onAdd }: { eyebrow: string; title: string; id: string; onAdd: () => void }) {
  const { t } = useI18n();
  return (
    <div className="section-heading">
      <div><p className="eyebrow">{eyebrow}</p><h1 id={id}>{title}</h1></div>
      <button className="primary-button compact button-with-icon" onClick={onAdd} type="button"><Plus size={17} />{t("Add")}</button>
    </div>
  );
}

function ResourceTable({ headers, loading, empty, error, children }: { headers: string[]; loading: boolean; empty: string; error?: string; children: ReactNode }) {
  const { t } = useI18n();
  return (
    <>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="table-frame">
        <table className="resource-table">
          <thead><tr>{headers.map((header, index) => <th key={`${header}-${index}`}>{header}</th>)}</tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={headers.length} className="empty-cell">{t("Loading...")}</td></tr> : children}
            {!loading && !error && Array.isArray(children) && children.length === 0 ? <tr><td colSpan={headers.length} className="empty-cell">{empty}</td></tr> : null}
          </tbody>
        </table>
      </div>
    </>
  );
}

function NodeDialog({ value, onClose, onSaved }: { value?: Node; onClose: () => void; onSaved: (node: Node) => void }) {
  const { t } = useI18n();
  const [name, setName] = useState(value?.name ?? "");
  const [address, setAddress] = useState(value?.public_address ?? "");
  const [enabled, setEnabled] = useState(value?.enabled ?? true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    const input: NodeInput = { name, public_address: address, enabled };
    try {
      onSaved(value ? await updateNode(value.id, input) : await createNode(input));
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={value ? t("Edit node") : t("Add node")} onClose={onClose}>
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <Field label={t("Name")} value={name} onChange={setName} autoFocus />
          <Field label={t("Public address")} value={address} onChange={setAddress} required={false} />
          <label className="toggle-field"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span>{t("Enabled")}</span></label>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogActions busy={busy} onClose={onClose} submitLabel={value ? t("Save") : t("Add node")} />
      </form>
    </Modal>
  );
}

function UserDialog({ value, onClose, onSaved }: { value?: User; onClose: () => void; onSaved: (user: User) => void }) {
  const { t } = useI18n();
  const [displayName, setDisplayName] = useState(value?.display_name ?? "");
  const [email, setEmail] = useState(value?.email ?? "");
  const [telegram, setTelegram] = useState(value?.telegram ?? "");
  const [note, setNote] = useState(value?.note ?? "");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    const input: UserInput = { display_name: displayName, email: email || null, telegram: telegram || null, note };
    try {
      onSaved(value ? await updateUser(value.id, input) : await createUser(input));
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={value ? t("Edit user") : t("Add user")} onClose={onClose}>
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <Field label={t("Display name")} value={displayName} onChange={setDisplayName} autoFocus />
          <Field label={t("Email")} value={email} onChange={setEmail} type="email" required={false} />
          <Field label={t("Telegram")} value={telegram} onChange={setTelegram} required={false} />
          <label className="field"><span>{t("Note")}</span><textarea value={note} onChange={(event) => setNote(event.target.value)} rows={4} /></label>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogActions busy={busy} onClose={onClose} submitLabel={value ? t("Save") : t("Add user")} />
      </form>
    </Modal>
  );
}

function DialogActions({ busy, onClose, submitLabel }: { busy: boolean; onClose: () => void; submitLabel: string }) {
  const { t } = useI18n();
  return (
    <div className="dialog-actions">
      <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
      <button className="primary-button compact" disabled={busy} type="submit">{busy ? t("Saving...") : submitLabel}</button>
    </div>
  );
}

function ConfirmAction({ title, name, action, onClose, onConfirm }: { title: string; name: string; action: string; onClose: () => void; onConfirm: () => Promise<void> }) {
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
      <p className="confirmation-name">{name}</p>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
        <button className="danger-button" disabled={busy} onClick={confirm} type="button">{busy ? t("Working...") : action}</button>
      </div>
    </Modal>
  );
}

function TokenDialog({ node, token, onClose }: { node: Node; token: NodeRegistrationToken; onClose: () => void }) {
  const { t, formatDateTime } = useI18n();
  const [copied, setCopied] = useState(false);
  async function copy() {
    try {
      await navigator.clipboard.writeText(token.token);
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }
  return (
    <Modal title={t("{name} registration token", { name: node.name })} onClose={onClose} dismissible={false}>
      <code className="one-time-token">{token.token}</code>
      <dl className="token-metadata"><dt>{t("Expires")}</dt><dd>{formatDateTime(token.expires_at)}</dd></dl>
      <div className="dialog-actions">
        <button className="secondary-button" onClick={copy} type="button">{copied ? t("Copied") : t("Copy")}</button>
        <button className="primary-button compact" onClick={onClose} type="button">{t("Done")}</button>
      </div>
    </Modal>
  );
}

function AgentUpdateDialog({ node, latest, onClose, onUpdated }: {
  node: Node;
  latest: AgentUpdate | null | undefined;
  onClose: () => void;
  onUpdated: (value: AgentUpdate) => void;
}) {
  const { t, formatDateTime } = useI18n();
  const [version, setVersion] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const pending = latest?.status === "pending";

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const value = await requestAgentUpdate(node.id, version);
      onUpdated(value);
      setVersion("");
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={t("{name} Agent update", { name: node.name })} onClose={onClose}>
      <dl className="agent-update-details">
        <div><dt>{t("Current version")}</dt><dd>{node.agent_version || t("Not reported")}</dd></div>
        {latest ? (
          <>
            <div><dt>{t("Target version")}</dt><dd>{latest.version}</dd></div>
            <div><dt>{t("Status")}</dt><dd><Status value={t(agentUpdateStatusLabel(latest.status))} tone={agentUpdateStatusTone(latest.status)} /></dd></div>
            <div><dt>{t("Delivery attempts")}</dt><dd>{latest.attempts}</dd></div>
            <div><dt>{t("Last sent")}</dt><dd>{latest.last_sent_at ? formatDateTime(latest.last_sent_at) : t("Not yet")}</dd></div>
            <div><dt>{t("Completed")}</dt><dd>{latest.completed_at ? formatDateTime(latest.completed_at) : t("Not yet")}</dd></div>
            <div><dt>{t("Expires")}</dt><dd>{formatDateTime(latest.expires_at)}</dd></div>
          </>
        ) : null}
      </dl>
      {latest?.problem ? <p className="agent-update-problem" role="alert">{t(latest.problem.message)}</p> : null}
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <Field label={t("Target version")} value={version} onChange={setVersion} autoFocus disabled={pending || busy} />
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">{t("Close")}</button>
          <button className="primary-button compact" disabled={pending || busy} type="submit">{busy ? t("Queuing...") : t("Queue update")}</button>
        </div>
      </form>
    </Modal>
  );
}

function AgentUpdateStatus({ value }: { value: AgentUpdate | null | undefined }) {
  const { t } = useI18n();
  if (!value) return <span className="secondary-cell">{t("Never")}</span>;
  const detail = value.status === "pending"
    ? (value.attempts === 0 ? t("Waiting") : t("{count} sent", { count: value.attempts }))
    : value.problem ? t(value.problem.message) : undefined;
  return (
    <span className="agent-update-cell" title={value.problem ? t(value.problem.message) : undefined}>
      <Status value={t(agentUpdateStatusLabel(value.status))} tone={agentUpdateStatusTone(value.status)} />
      {detail ? <small>{detail}</small> : null}
    </span>
  );
}

function NodePolicyStatus({ node }: { node: Node }) {
  const { t } = useI18n();
  const policy = node.policy;
  if (!policy) return <Status value={t("Not configured")} tone="muted" />;
  const label = policy.status === "applied"
    ? t("Applied {generation}", { generation: policy.applied_generation })
    : policy.status === "pending"
      ? t("Pending {applied}/{desired}", { applied: policy.applied_generation, desired: policy.desired_generation })
      : policy.status === "not_configured"
        ? t("Not configured")
        : t(policy.status.charAt(0).toUpperCase() + policy.status.slice(1));
  const tone = policy.status === "applied" ? "ok"
    : policy.status === "failed" || policy.status === "unsupported" ? "error"
      : policy.status === "pending" ? "warning" : "muted";
  return (
    <span className="agent-update-cell" title={policy.last_problem ? t(policy.last_problem.message) : undefined}>
      <Status value={label} tone={tone} />
      {policy.last_problem ? <small>{t(policy.last_problem.message)}</small> : null}
    </span>
  );
}

function IconAction({ label, title, danger = false, disabled = false, onClick, children }: {
  label: string;
  title?: string;
  danger?: boolean;
  disabled?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return <button className={`icon-button${danger ? " icon-button--danger" : ""}`} aria-label={label} title={title ?? label} disabled={disabled} onClick={onClick} type="button">{children}</button>;
}

function Status({ value, tone }: { value: string; tone: "ok" | "warning" | "error" | "muted" }) {
  return <span className="inline-status"><span className={`status-dot status-dot--${tone}`} />{value}</span>;
}

function agentStatusLabel(status: Node["agent_status"]) {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function agentStatusTone(status: Node["agent_status"]): "ok" | "warning" | "muted" {
  if (status === "online") return "ok";
  if (status === "disabled") return "muted";
  return "warning";
}

function agentUpdateStatusLabel(status: AgentUpdate["status"]) {
  return status.charAt(0).toUpperCase() + status.slice(1);
}

function agentUpdateStatusTone(status: AgentUpdate["status"]): "ok" | "warning" | "error" | "muted" {
  if (status === "succeeded") return "ok";
  if (status === "failed") return "error";
  if (status === "pending") return "warning";
  return "muted";
}

function agentUpdateUnavailable(node: Node): string | undefined {
  if (!node.enabled) return "Enable the node before updating its Agent";
  if (node.registered_at === null) return "Register the Agent before updating it";
  if (!node.capabilities.includes("control.commands")) return "The Agent does not support durable commands";
  if (!node.capabilities.includes("agent.self_update")) return "The Agent does not support self-update";
  return undefined;
}

function translatedOptional(value: string | undefined, t: (message: string) => string): string | undefined {
  return value === undefined ? undefined : t(value);
}

function byName(left: Node, right: Node) {
  return left.name.localeCompare(right.name);
}

function byDisplayName(left: User, right: User) {
  return left.display_name.localeCompare(right.display_name);
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
