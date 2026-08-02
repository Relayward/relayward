import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { KeyRound, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";

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
  updateNode,
  updateUser,
  type AgentUpdate,
  type Node,
  type NodeInput,
  type NodeRegistrationToken,
  type User,
  type UserInput,
} from "../api";
import { Field, FormError } from "./AuthScreen";
import { Modal } from "./Modal";

export function NodesView() {
  const [items, setItems] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<Node | "new">();
  const [deleting, setDeleting] = useState<Node>();
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
      <ResourceHeading eyebrow="Infrastructure" title="Nodes" id="nodes-title" onAdd={() => setEditing("new")} />
      <ResourceTable headers={["Name", "Address", "Agent", "Version", "Policy", "Update", "State", "Actions"]} loading={loading} empty="No nodes have been created." error={error}>
        {items.map((node) => (
          <tr key={node.id}>
            <td data-label="Name"><strong>{node.name}</strong></td>
            <td data-label="Address" className="secondary-cell">{node.public_address || "Not set"}</td>
            <td data-label="Agent"><Status value={agentStatusLabel(node.agent_status)} tone={agentStatusTone(node.agent_status)} /></td>
            <td data-label="Version" className="secondary-cell">{node.agent_version || "Not reported"}</td>
            <td data-label="Policy"><NodePolicyStatus node={node} /></td>
            <td data-label="Update"><AgentUpdateStatus value={updates[node.id]} /></td>
            <td data-label="State"><Status value={node.enabled ? "Enabled" : "Disabled"} tone={node.enabled ? "ok" : "muted"} /></td>
            <td className="table-actions">
              <IconAction label="Create registration token" onClick={() => issueToken(node)}><KeyRound size={17} /></IconAction>
              <IconAction
                label="Update Agent"
                title={agentUpdateUnavailable(node)}
                disabled={agentUpdateUnavailable(node) !== undefined}
                onClick={() => setUpdatingNodeID(node.id)}
              ><RefreshCw size={17} /></IconAction>
              <IconAction label="Edit node" onClick={() => setEditing(node)}><Pencil size={17} /></IconAction>
              <IconAction label="Delete node" danger onClick={() => setDeleting(node)}><Trash2 size={17} /></IconAction>
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
        <ConfirmDelete
          title="Delete node"
          name={deleting.name}
          onClose={() => setDeleting(undefined)}
          onConfirm={async () => {
            await deleteNode(deleting.id);
            setItems((current) => current.filter((item) => item.id !== deleting.id));
            setDeleting(undefined);
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
      <ResourceHeading eyebrow="Access" title="Users" id="users-title" onAdd={() => setEditing("new")} />
      <ResourceTable headers={["Name", "Email", "Telegram", "Actions"]} loading={loading} empty="No users have been created." error={error}>
        {items.map((user) => (
          <tr key={user.id}>
            <td data-label="Name"><strong>{user.display_name}</strong></td>
            <td data-label="Email" className="secondary-cell">{user.email || "Not set"}</td>
            <td data-label="Telegram" className="secondary-cell">{user.telegram || "Not set"}</td>
            <td className="table-actions">
              <IconAction label="Edit user" onClick={() => setEditing(user)}><Pencil size={17} /></IconAction>
              <IconAction label="Delete user" danger onClick={() => setDeleting(user)}><Trash2 size={17} /></IconAction>
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
        <ConfirmDelete
          title="Delete user"
          name={deleting.display_name}
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
  return (
    <div className="section-heading">
      <div><p className="eyebrow">{eyebrow}</p><h1 id={id}>{title}</h1></div>
      <button className="primary-button compact button-with-icon" onClick={onAdd} type="button"><Plus size={17} />Add</button>
    </div>
  );
}

function ResourceTable({ headers, loading, empty, error, children }: { headers: string[]; loading: boolean; empty: string; error?: string; children: ReactNode }) {
  return (
    <>
      <FormError message={error} />
      <div className="table-frame">
        <table className="resource-table">
          <thead><tr>{headers.map((header, index) => <th key={`${header}-${index}`}>{header}</th>)}</tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={headers.length} className="empty-cell">Loading...</td></tr> : children}
            {!loading && !error && Array.isArray(children) && children.length === 0 ? <tr><td colSpan={headers.length} className="empty-cell">{empty}</td></tr> : null}
          </tbody>
        </table>
      </div>
    </>
  );
}

function NodeDialog({ value, onClose, onSaved }: { value?: Node; onClose: () => void; onSaved: (node: Node) => void }) {
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
    <Modal title={value ? "Edit node" : "Add node"} onClose={onClose}>
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <Field label="Name" value={name} onChange={setName} autoFocus />
          <Field label="Public address" value={address} onChange={setAddress} required={false} />
          <label className="toggle-field"><input type="checkbox" checked={enabled} onChange={(event) => setEnabled(event.target.checked)} /><span>Enabled</span></label>
        </div>
        <FormError message={error} />
        <DialogActions busy={busy} onClose={onClose} submitLabel={value ? "Save" : "Add node"} />
      </form>
    </Modal>
  );
}

function UserDialog({ value, onClose, onSaved }: { value?: User; onClose: () => void; onSaved: (user: User) => void }) {
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
    <Modal title={value ? "Edit user" : "Add user"} onClose={onClose}>
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <Field label="Display name" value={displayName} onChange={setDisplayName} autoFocus />
          <Field label="Email" value={email} onChange={setEmail} type="email" required={false} />
          <Field label="Telegram" value={telegram} onChange={setTelegram} required={false} />
          <label className="field"><span>Note</span><textarea value={note} onChange={(event) => setNote(event.target.value)} rows={4} /></label>
        </div>
        <FormError message={error} />
        <DialogActions busy={busy} onClose={onClose} submitLabel={value ? "Save" : "Add user"} />
      </form>
    </Modal>
  );
}

function DialogActions({ busy, onClose, submitLabel }: { busy: boolean; onClose: () => void; submitLabel: string }) {
  return (
    <div className="dialog-actions">
      <button className="quiet-button" onClick={onClose} type="button">Cancel</button>
      <button className="primary-button compact" disabled={busy} type="submit">{busy ? "Saving..." : submitLabel}</button>
    </div>
  );
}

function ConfirmDelete({ title, name, onClose, onConfirm }: { title: string; name: string; onClose: () => void; onConfirm: () => Promise<void> }) {
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
      <FormError message={error} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">Cancel</button>
        <button className="danger-button" disabled={busy} onClick={confirm} type="button">{busy ? "Deleting..." : "Delete"}</button>
      </div>
    </Modal>
  );
}

function TokenDialog({ node, token, onClose }: { node: Node; token: NodeRegistrationToken; onClose: () => void }) {
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
    <Modal title={`${node.name} registration token`} onClose={onClose} dismissible={false}>
      <code className="one-time-token">{token.token}</code>
      <dl className="token-metadata"><dt>Expires</dt><dd>{new Date(token.expires_at).toLocaleString()}</dd></dl>
      <div className="dialog-actions">
        <button className="secondary-button" onClick={copy} type="button">{copied ? "Copied" : "Copy"}</button>
        <button className="primary-button compact" onClick={onClose} type="button">Done</button>
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
    <Modal title={`${node.name} Agent update`} onClose={onClose}>
      <dl className="agent-update-details">
        <div><dt>Current version</dt><dd>{node.agent_version || "Not reported"}</dd></div>
        {latest ? (
          <>
            <div><dt>Target version</dt><dd>{latest.version}</dd></div>
            <div><dt>Status</dt><dd><Status value={agentUpdateStatusLabel(latest.status)} tone={agentUpdateStatusTone(latest.status)} /></dd></div>
            <div><dt>Delivery attempts</dt><dd>{latest.attempts}</dd></div>
            <div><dt>Last sent</dt><dd>{formatOptionalTime(latest.last_sent_at)}</dd></div>
            <div><dt>Completed</dt><dd>{formatOptionalTime(latest.completed_at)}</dd></div>
            <div><dt>Expires</dt><dd>{new Date(latest.expires_at).toLocaleString()}</dd></div>
          </>
        ) : null}
      </dl>
      {latest?.problem ? <p className="agent-update-problem" role="alert">{latest.problem.message}</p> : null}
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <Field label="Target version" value={version} onChange={setVersion} autoFocus disabled={pending || busy} />
        </div>
        <FormError message={error} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">Close</button>
          <button className="primary-button compact" disabled={pending || busy} type="submit">{busy ? "Queuing..." : "Queue update"}</button>
        </div>
      </form>
    </Modal>
  );
}

function AgentUpdateStatus({ value }: { value: AgentUpdate | null | undefined }) {
  if (!value) return <span className="secondary-cell">Never</span>;
  const detail = value.status === "pending"
    ? (value.attempts === 0 ? "Waiting" : `${value.attempts} sent`)
    : value.problem?.message;
  return (
    <span className="agent-update-cell" title={value.problem?.message}>
      <Status value={agentUpdateStatusLabel(value.status)} tone={agentUpdateStatusTone(value.status)} />
      {detail ? <small>{detail}</small> : null}
    </span>
  );
}

function NodePolicyStatus({ node }: { node: Node }) {
  const policy = node.policy;
  if (!policy) return <Status value="Not configured" tone="muted" />;
  const label = policy.status === "applied"
    ? `Applied ${policy.applied_generation}`
    : policy.status === "pending"
      ? `Pending ${policy.applied_generation}/${policy.desired_generation}`
      : policy.status === "not_configured"
        ? "Not configured"
        : policy.status.charAt(0).toUpperCase() + policy.status.slice(1);
  const tone = policy.status === "applied" ? "ok"
    : policy.status === "failed" || policy.status === "unsupported" ? "error"
      : policy.status === "pending" ? "warning" : "muted";
  return (
    <span className="agent-update-cell" title={policy.last_problem?.message}>
      <Status value={label} tone={tone} />
      {policy.last_problem ? <small>{policy.last_problem.message}</small> : null}
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

function formatOptionalTime(value: string | null) {
  return value ? new Date(value).toLocaleString() : "Not yet";
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
