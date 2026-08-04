import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
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
import { cn } from "../lib/utils";
import { Field, FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
import { DialogFooter } from "./ui/dialog";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Textarea } from "./ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

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
      <ResourceTable tableClassName="min-w-[1000px]" headers={["Name", "Address", "Agent", "Version", "Policy", "Update", "State", "Actions"].map((value) => t(value))} loading={loading} empty={t("No nodes have been created.")} error={error}>
        {items.map((node) => (
          <TableRow key={node.id}>
            <TableCell><strong className="font-semibold">{node.name}</strong></TableCell>
            <TableCell className="text-muted-foreground">{node.public_address || t("Not set")}</TableCell>
            <TableCell><Status value={t(agentStatusLabel(node.agent_status))} tone={agentStatusTone(node.agent_status)} /></TableCell>
            <TableCell className="text-muted-foreground">{node.agent_version || t("Not reported")}</TableCell>
            <TableCell><NodePolicyStatus node={node} /></TableCell>
            <TableCell><AgentUpdateStatus value={updates[node.id]} /></TableCell>
            <TableCell><Status value={node.enabled ? t("Enabled") : t("Disabled")} tone={node.enabled ? "ok" : "muted"} /></TableCell>
            <TableCell className="w-px text-right whitespace-nowrap">
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
            </TableCell>
          </TableRow>
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
      <ResourceTable tableClassName="min-w-[640px]" headers={["Name", "Email", "Telegram", "Actions"].map((value) => t(value))} loading={loading} empty={t("No users have been created.")} error={error}>
        {items.map((user) => (
          <TableRow key={user.id}>
            <TableCell><strong className="font-semibold">{user.display_name}</strong></TableCell>
            <TableCell className="text-muted-foreground">{user.email || t("Not set")}</TableCell>
            <TableCell className="text-muted-foreground">{user.telegram || t("Not set")}</TableCell>
            <TableCell className="w-px text-right whitespace-nowrap">
              <IconAction label={t("Edit user")} onClick={() => setEditing(user)}><Pencil size={17} /></IconAction>
              <IconAction label={t("Delete user")} danger onClick={() => setDeleting(user)}><Trash2 size={17} /></IconAction>
            </TableCell>
          </TableRow>
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
    <div className="mb-6 flex items-end justify-between gap-4">
      <div><p className="m-0 text-xs font-semibold text-muted-foreground">{eyebrow}</p><h1 className="mt-0.5 mb-0 text-[25px] font-semibold" id={id}>{title}</h1></div>
      <Button size="sm" onClick={onAdd} type="button"><Plus size={17} />{t("Add")}</Button>
    </div>
  );
}

function ResourceTable({ tableClassName, headers, loading, empty, error, children }: {
  tableClassName: string;
  headers: string[];
  loading: boolean;
  empty: string;
  error?: string;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
    <>
      {error ? <div className="mb-3"><FormError message={t(error)} /></div> : null}
      <div className="overflow-hidden rounded-md border border-border bg-card">
        <Table className={tableClassName}>
          <TableHeader><TableRow className="hover:bg-transparent">{headers.map((header, index) => <TableHead className={index === headers.length - 1 ? "text-right" : undefined} key={`${header}-${index}`}>{header}</TableHead>)}</TableRow></TableHeader>
          <TableBody>
            {loading ? <TableRow><TableCell colSpan={headers.length} className="h-24 text-center text-muted-foreground">{t("Loading...")}</TableCell></TableRow> : children}
            {!loading && !error && Array.isArray(children) && children.length === 0 ? <TableRow><TableCell colSpan={headers.length} className="h-24 text-center text-muted-foreground">{empty}</TableCell></TableRow> : null}
          </TableBody>
        </Table>
      </div>
    </>
  );
}

function NodeDialog({ value, onClose, onSaved }: { value?: Node; onClose: () => void; onSaved: (node: Node) => void }) {
  const { t } = useI18n();
  const enabledID = useId();
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
      <form className="grid gap-5" onSubmit={submit}>
        <div className="grid gap-4">
          <Field label={t("Name")} value={name} onChange={setName} autoFocus />
          <Field label={t("Public address")} value={address} onChange={setAddress} required={false} />
          <label className="flex min-h-8 cursor-pointer items-center gap-2 text-[13px] font-semibold text-foreground/80" htmlFor={enabledID}>
            <Checkbox id={enabledID} checked={enabled} onCheckedChange={(checked) => setEnabled(checked === true)} />
            <span>{t("Enabled")}</span>
          </label>
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
      <form className="grid gap-5" onSubmit={submit}>
        <div className="grid gap-4">
          <Field label={t("Display name")} value={displayName} onChange={setDisplayName} autoFocus />
          <Field label={t("Email")} value={email} onChange={setEmail} type="email" required={false} />
          <Field label={t("Telegram")} value={telegram} onChange={setTelegram} required={false} />
          <label className="grid gap-1.5"><span className="text-[13px] font-semibold text-foreground/80">{t("Note")}</span><Textarea value={note} onChange={(event) => setNote(event.target.value)} rows={4} /></label>
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
    <DialogFooter>
      <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
      <Button disabled={busy} type="submit">{busy ? t("Saving...") : submitLabel}</Button>
    </DialogFooter>
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
      <p className="m-0 [overflow-wrap:anywhere] border-l-[3px] border-destructive bg-destructive/10 p-3">{name}</p>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <DialogFooter>
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button variant="destructive" disabled={busy} onClick={confirm} type="button">{busy ? t("Working...") : action}</Button>
      </DialogFooter>
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
      <code className="block [overflow-wrap:anywhere] rounded-sm border border-border bg-muted p-3 text-[13px]">{token.token}</code>
      <dl className="m-0 flex items-center justify-between gap-4 text-[13px]"><dt className="text-muted-foreground">{t("Expires")}</dt><dd className="m-0 text-right">{formatDateTime(token.expires_at)}</dd></dl>
      <DialogFooter>
        <Button variant="secondary" onClick={copy} type="button">{copied ? t("Copied") : t("Copy")}</Button>
        <Button onClick={onClose} type="button">{t("Done")}</Button>
      </DialogFooter>
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
      <dl className="m-0 divide-y divide-border border-y border-border text-[13px]">
        <UpdateDetail label={t("Current version")}>{node.agent_version || t("Not reported")}</UpdateDetail>
        {latest ? (
          <>
            <UpdateDetail label={t("Target version")}>{latest.version}</UpdateDetail>
            <UpdateDetail label={t("Status")}><Status value={t(agentUpdateStatusLabel(latest.status))} tone={agentUpdateStatusTone(latest.status)} /></UpdateDetail>
            <UpdateDetail label={t("Delivery attempts")}>{latest.attempts}</UpdateDetail>
            <UpdateDetail label={t("Last sent")}>{latest.last_sent_at ? formatDateTime(latest.last_sent_at) : t("Not yet")}</UpdateDetail>
            <UpdateDetail label={t("Completed")}>{latest.completed_at ? formatDateTime(latest.completed_at) : t("Not yet")}</UpdateDetail>
            <UpdateDetail label={t("Expires")}>{formatDateTime(latest.expires_at)}</UpdateDetail>
          </>
        ) : null}
      </dl>
      {latest?.problem ? <p className="m-0 [overflow-wrap:anywhere] border-l-[3px] border-destructive bg-destructive/10 px-3 py-2.5 text-[13px] text-destructive" role="alert">{t(latest.problem.message)}</p> : null}
      <form className="grid gap-5" onSubmit={submit}>
        <Field label={t("Target version")} value={version} onChange={setVersion} autoFocus disabled={pending || busy} />
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} type="button">{t("Close")}</Button>
          <Button disabled={pending || busy} type="submit">{busy ? t("Queuing...") : t("Queue update")}</Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function UpdateDetail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-h-10 items-center justify-between gap-4 py-2">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="m-0 [overflow-wrap:anywhere] text-right">{children}</dd>
    </div>
  );
}

function AgentUpdateStatus({ value }: { value: AgentUpdate | null | undefined }) {
  const { t } = useI18n();
  if (!value) return <span className="text-muted-foreground">{t("Never")}</span>;
  const detail = value.status === "pending"
    ? (value.attempts === 0 ? t("Waiting") : t("{count} sent", { count: value.attempts }))
    : value.problem ? t(value.problem.message) : undefined;
  return (
    <span className="grid max-w-[150px] gap-0.5" title={value.problem ? t(value.problem.message) : undefined}>
      <Status value={t(agentUpdateStatusLabel(value.status))} tone={agentUpdateStatusTone(value.status)} />
      {detail ? <small className="overflow-hidden text-ellipsis whitespace-nowrap text-muted-foreground">{detail}</small> : null}
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
    <span className="grid max-w-[150px] gap-0.5" title={policy.last_problem ? t(policy.last_problem.message) : undefined}>
      <Status value={label} tone={tone} />
      {policy.last_problem ? <small className="overflow-hidden text-ellipsis whitespace-nowrap text-muted-foreground">{t(policy.last_problem.message)}</small> : null}
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
  const description = title ?? label;
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          className={cn(
            "ml-0.5 text-muted-foreground aria-disabled:cursor-not-allowed aria-disabled:opacity-55",
            danger && "text-destructive hover:bg-destructive/10 hover:text-destructive",
          )}
          variant="ghost"
          size="icon"
          aria-disabled={disabled}
          aria-label={label}
          onClick={disabled ? undefined : onClick}
          type="button"
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{description}</TooltipContent>
    </Tooltip>
  );
}

function Status({ value, tone }: { value: string; tone: "ok" | "warning" | "error" | "muted" }) {
  const toneClass = {
    ok: "bg-success",
    warning: "bg-warning",
    error: "bg-destructive",
    muted: "bg-muted-foreground",
  }[tone];
  return <span className="inline-flex items-center gap-1.5 whitespace-nowrap"><span className={cn("size-2 shrink-0 rounded-full", toneClass)} />{value}</span>;
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
