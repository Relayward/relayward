import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { ChevronRight, KeyRound, Mail, MessageCircle, Pencil, Plus, Power, RefreshCw, Server, ShieldX, Trash2, Users } from "lucide-react";

import {
  APIError,
  createUser,
  deleteUser,
  getLatestAgentUpdate,
  listNodes,
  listUsers,
  updateNode,
  updateUser,
  type AgentUpdate,
  type Node,
  type User,
  type UserInput,
} from "../api";
import { agentUpdatePresentation } from "../agentUpdate";
import { useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { Field, FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { NodeEditorDialog } from "./NodeActions";
import { PageHeader, SummaryBar, SummaryItem } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { DialogFooter } from "./ui/dialog";
import { Input } from "./ui/input";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Textarea } from "./ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

export function NodesView({ onOpenNode }: { onOpenNode: (nodeID: string) => void }) {
  const { t, formatDateTime } = useI18n();
  const [items, setItems] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [creating, setCreating] = useState(false);
  const [changingNodeID, setChangingNodeID] = useState<string>();
  const [updates, setUpdates] = useState<Record<string, AgentUpdate | null>>({});
  const [search, setSearch] = useState("");
  const visibleItems = items.filter((node) => [node.name, node.hostname, node.agent_os, node.agent_status]
    .some((value) => value?.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase())));

  async function toggleNode(node: Node) {
    setChangingNodeID(node.id);
    setError(undefined);
    try {
      const updated = await updateNode(node.id, { name: node.name, enabled: !node.enabled });
      setItems((current) => current.map((item) => item.id === updated.id ? updated : item).sort(byName));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setChangingNodeID(undefined);
    }
  }

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

  return (
    <section aria-labelledby="nodes-title">
      <PageHeader id="nodes-title" eyebrow={t("Infrastructure")} title={t("Nodes")} description={t("Manage Agent registration, runtime state, policy delivery, and updates.")} />
      <SummaryBar>
        <SummaryItem icon={<Server size={17} />} label={t("Online nodes")} value={`${items.filter((node) => node.agent_status === "online").length} / ${items.length}`} tone="success" />
        <SummaryItem icon={<KeyRound size={17} />} label={t("Registered Agents")} value={items.filter((node) => node.registered_at !== null).length} tone="primary" />
        <SummaryItem icon={<RefreshCw size={17} />} label={t("Pending updates")} value={Object.values(updates).filter((update) => update?.status === "pending").length} tone="warning" />
        <SummaryItem icon={<ShieldX size={17} />} label={t("Disabled nodes")} value={items.filter((node) => !node.enabled).length} tone={items.some((node) => !node.enabled) ? "warning" : "default"} />
      </SummaryBar>
      <ResourceTable
        title={t("Node list")}
        description={t("{count} nodes", { count: visibleItems.length })}
        actions={<><Input className="max-w-sm" value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("Search nodes...")} /><Button className="shrink-0" onClick={() => setCreating(true)} type="button"><Plus />{t("Add node")}</Button></>}
        tableClassName="min-w-[840px]"
        headers={["Node", "Agent", "Policy", "Update", "Last seen", "Actions"].map((value) => t(value))}
        loading={loading}
        empty={t("No nodes have been created.")}
        error={error}
      >
        {visibleItems.map((node) => (
          <TableRow
            className="group cursor-pointer hover:bg-primary/5 focus-visible:bg-primary/5 focus-visible:outline-none focus-visible:ring-2 focus-visible:ring-ring focus-visible:ring-inset"
            key={node.id}
            role="link"
            tabIndex={0}
            aria-label={t("View {name} node details", { name: node.name })}
            onClick={(event) => {
              if (!isInteractiveTarget(event.target)) onOpenNode(node.id);
            }}
            onKeyDown={(event) => {
              if (event.target !== event.currentTarget || (event.key !== "Enter" && event.key !== " ")) return;
              event.preventDefault();
              onOpenNode(node.id);
            }}
          >
            <TableCell><span className="flex min-w-[170px] items-center gap-3"><span className="flex size-8 shrink-0 items-center justify-center rounded-md bg-primary-soft text-primary transition-colors group-hover:bg-primary group-hover:text-primary-foreground"><Server size={16} /></span><span className="grid min-w-0 gap-0.5"><strong className="truncate font-semibold transition-colors group-hover:text-primary-strong">{node.name}</strong><small className="truncate text-xs text-muted-foreground">{[node.agent_os, node.agent_arch].filter(Boolean).join(" · ") || t("Not reported")}</small></span></span></TableCell>
            <TableCell><span className="grid gap-1"><Status value={t(agentStatusLabel(node.agent_status))} tone={agentStatusTone(node.agent_status)} /><small className="text-xs text-muted-foreground">{node.agent_version || t("Not reported")}</small></span></TableCell>
            <TableCell><NodePolicyStatus node={node} /></TableCell>
            <TableCell><AgentUpdateStatus node={node} value={updates[node.id]} /></TableCell>
            <TableCell className="whitespace-nowrap text-muted-foreground">{node.last_seen_at ? formatDateTime(node.last_seen_at) : t("Never")}</TableCell>
            <TableCell className="w-px text-right whitespace-nowrap">
              <span className="inline-flex items-center gap-2">
                <IconAction
                  label={t(node.enabled ? "Disable node" : "Enable node")}
                  disabled={changingNodeID === node.id}
                  onClick={() => { void toggleNode(node); }}
                ><Power size={17} /></IconAction>
                <span className="flex size-8 items-center justify-center text-muted-foreground transition-all group-hover:translate-x-0.5 group-hover:text-primary" aria-hidden="true"><ChevronRight size={18} /></span>
              </span>
            </TableCell>
          </TableRow>
        ))}
      </ResourceTable>
      {creating ? (
        <NodeEditorDialog
          onClose={() => setCreating(false)}
          onSaved={(node) => {
            setItems((current) => [...current, node].sort(byName));
            setCreating(false);
            onOpenNode(node.id);
          }}
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
  const [search, setSearch] = useState("");
  const visibleItems = items.filter((user) => [user.display_name, user.email, user.telegram]
    .some((value) => value?.toLocaleLowerCase().includes(search.trim().toLocaleLowerCase())));

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
      <PageHeader id="users-title" eyebrow={t("Access")} title={t("Users")} description={t("Manage subscription users and their contact details.")} />
      <SummaryBar className="xl:grid-cols-3">
        <SummaryItem icon={<Users size={17} />} label={t("Users")} value={items.length} tone="primary" />
        <SummaryItem icon={<Mail size={17} />} label={t("Email configured")} value={items.filter((user) => user.email).length} tone="default" />
        <SummaryItem icon={<MessageCircle size={17} />} label={t("Telegram configured")} value={items.filter((user) => user.telegram).length} tone="default" />
      </SummaryBar>
      <ResourceTable
        title={t("User list")}
        description={t("{count} users", { count: visibleItems.length })}
        actions={<><Input className="max-w-sm" value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("Search users...")} /><Button className="shrink-0" onClick={() => setEditing("new")} type="button"><Plus />{t("Add user")}</Button></>}
        tableClassName="min-w-[640px]"
        headers={["User identifier", "Email", "Telegram", "Actions"].map((value) => t(value))}
        loading={loading}
        empty={t("No users have been created.")}
        error={error}
      >
        {visibleItems.map((user) => (
          <TableRow key={user.id}>
            <TableCell><span className="flex items-center gap-3"><span className="flex size-8 shrink-0 items-center justify-center rounded-full bg-primary-soft text-xs font-bold text-primary-strong">{user.display_name.slice(0, 1).toUpperCase()}</span><strong className="font-semibold">{user.display_name}</strong></span></TableCell>
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

function ResourceTable({ title, description, actions, tableClassName, headers, loading, empty, error, children }: {
  title: string;
  description: string;
  actions?: ReactNode;
  tableClassName: string;
  headers: string[];
  loading: boolean;
  empty: string;
  error?: string;
  children: ReactNode;
}) {
  const { t } = useI18n();
  return (
      <Card className="min-w-0 h-fit">
        <CardHeader className="flex flex-col items-start justify-between space-y-0 gap-4 pb-4 sm:flex-row sm:items-center">
          <div className="min-w-0">
            <CardTitle>{title}</CardTitle>
            <CardDescription>{description}</CardDescription>
          </div>
          {actions ? <div className="flex w-full flex-col gap-3 sm:w-auto sm:flex-row sm:items-center">{actions}</div> : null}
        </CardHeader>
        <CardContent className="space-y-4">
          {error ? <FormError message={t(error)} /> : null}
          <div className="rounded-lg border bg-card">
            <Table className={tableClassName}>
              <TableHeader><TableRow className="hover:bg-transparent">{headers.map((header, index) => <TableHead className={index === headers.length - 1 ? "text-right" : undefined} key={`${header}-${index}`}>{header}</TableHead>)}</TableRow></TableHeader>
              <TableBody>
                {loading ? <TableRow><TableCell colSpan={headers.length} className="h-24 text-center text-muted-foreground">{t("Loading...")}</TableCell></TableRow> : children}
                {!loading && !error && Array.isArray(children) && children.length === 0 ? <TableRow><TableCell colSpan={headers.length} className="h-24 text-center text-muted-foreground">{empty}</TableCell></TableRow> : null}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
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
          <Field label={t("User identifier")} value={displayName} onChange={setDisplayName} autoFocus />
          <Field label={t("{field} (optional)", { field: t("Email") })} value={email} onChange={setEmail} type="email" required={false} />
          <Field label={t("{field} (optional)", { field: t("Telegram") })} value={telegram} onChange={setTelegram} required={false} />
          <label className="grid gap-1.5"><span className="text-sm font-semibold text-foreground/80">{t("{field} (optional)", { field: t("Note") })}</span><Textarea value={note} onChange={(event) => setNote(event.target.value)} rows={4} /></label>
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

function AgentUpdateStatus({ node, value }: { node: Node; value: AgentUpdate | null | undefined }) {
  const { t } = useI18n();
  const presentation = agentUpdatePresentation(value, node.agent_status);
  const tone = presentation.tone === "success" ? "ok"
    : presentation.tone === "danger" ? "error"
      : presentation.tone;
  return (
    <span className="grid max-w-[150px] gap-0.5" title={value?.problem ? t(value.problem.message) : undefined}>
      <Status value={t(presentation.label)} tone={tone} />
      {value && presentation.detail ? <small className="overflow-hidden text-ellipsis whitespace-nowrap text-muted-foreground">{t(presentation.detail)}</small> : null}
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

function isInteractiveTarget(target: EventTarget | null): boolean {
  return target instanceof Element && target.closest("button, a, input, select, textarea, [role=button], [role=menuitem]") !== null;
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
