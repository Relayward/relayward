import { useCallback, useEffect, useRef, useState, type ReactNode } from "react";
import {
  Activity,
  ArrowLeft,
  Boxes,
  CircleAlert,
  Cpu,
  EllipsisVertical,
  KeyRound,
  Pencil,
  Power,
  RefreshCw,
  ShieldX,
  Trash2,
} from "lucide-react";

import {
  APIError,
  deleteNode,
  getLatestAgentUpdate,
  getNode,
  listNodeCommands,
  listNodePluginInstances,
  revokeNodeCredential,
  updateNode,
  type AgentUpdate,
  type Node,
  type NodeCommand,
  type NodePluginInstance,
} from "../api";
import type { PluginNodeDetailPage } from "../adminNavigation";
import { useI18n } from "../i18n";
import { pluginIcons } from "../pluginIcons";
import { AgentUpdateDialog } from "./AgentUpdateDialog";
import { FormError } from "./AuthScreen";
import { ConfirmNodeAction, NodeEditorDialog } from "./NodeActions";
import { NodeEnrollmentDialog } from "./NodeEnrollmentDialog";
import { PageHeader, StatusBadge } from "./PageLayout";
import { PluginFrame, type PluginNavigationTarget } from "./PluginFrame";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuItem,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "./ui/dropdown-menu";
import { Skeleton } from "./ui/skeleton";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

export function NodeDetailsView({ nodeID, pluginPages, onBack, onDeleted, onNavigate }: {
  nodeID: string;
  pluginPages: PluginNodeDetailPage[];
  onBack: () => void;
  onDeleted: () => void;
  onNavigate: (target: PluginNavigationTarget) => void;
}) {
  const { t, formatDateTime } = useI18n();
  const [node, setNode] = useState<Node>();
  const [latestUpdate, setLatestUpdate] = useState<AgentUpdate | null>();
  const [commands, setCommands] = useState<NodeCommand[]>([]);
  const [instances, setInstances] = useState<NodePluginInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState(false);
  const [enrolling, setEnrolling] = useState(false);
  const [updating, setUpdating] = useState(false);
  const [revoking, setRevoking] = useState(false);
  const [deleting, setDeleting] = useState(false);
  const [changingEnabled, setChangingEnabled] = useState(false);
  const loadGeneration = useRef(0);

  const load = useCallback(async (showLoading: boolean) => {
    const generation = ++loadGeneration.current;
    if (showLoading) setLoading(true);
    else setRefreshing(true);
    try {
      const [nextNode, nextUpdate, nextCommands, allInstances] = await Promise.all([
        getNode(nodeID),
        getLatestAgentUpdate(nodeID),
        listNodeCommands(nodeID),
        listNodePluginInstances(),
      ]);
      if (loadGeneration.current !== generation) return;
      setNode(nextNode);
      setLatestUpdate(nextUpdate);
      setCommands(nextCommands);
      setInstances(allInstances.filter((instance) => instance.node_id === nodeID));
      setError(undefined);
    } catch (cause) {
      if (loadGeneration.current !== generation) return;
      setError(errorMessage(cause));
    } finally {
      if (loadGeneration.current !== generation) return;
      setLoading(false);
      setRefreshing(false);
    }
  }, [nodeID]);

  useEffect(() => {
    let active = true;
    const refresh = async (showLoading: boolean) => {
      if (!active) return;
      await load(showLoading);
    };
    void refresh(true);
    const timer = window.setInterval(() => { void refresh(false); }, 10_000);
    return () => {
      active = false;
      loadGeneration.current += 1;
      window.clearInterval(timer);
    };
  }, [load]);

  async function toggleEnabled() {
    if (!node) return;
    setChangingEnabled(true);
    setError(undefined);
    try {
      setNode(await updateNode(node.id, { name: node.name, enabled: !node.enabled }));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setChangingEnabled(false);
    }
  }

  if (loading && node === undefined) return <NodeDetailsLoading onBack={onBack} />;
  if (node === undefined) {
    return (
      <section aria-labelledby="node-details-error-title">
        <Button className="mb-4" variant="ghost" onClick={onBack} type="button"><ArrowLeft />{t("Back to nodes")}</Button>
        <Card>
          <CardHeader>
            <CardTitle id="node-details-error-title">{t("Node details unavailable")}</CardTitle>
            <CardDescription>{t(error ?? "The request could not be completed.")}</CardDescription>
          </CardHeader>
          <CardContent><Button onClick={() => { void load(true); }} type="button"><RefreshCw />{t("Retry")}</Button></CardContent>
        </Card>
      </section>
    );
  }

  const updateUnavailable = agentUpdateUnavailable(node);
  return (
    <section className="min-w-0 overflow-hidden" aria-labelledby="node-details-title">
      <Button className="mb-4" variant="ghost" onClick={onBack} type="button"><ArrowLeft />{t("Back to nodes")}</Button>
      <PageHeader
        id="node-details-title"
        eyebrow={t("Node details")}
        title={node.name}
        description={[node.hostname, node.agent_version ? `Agent ${node.agent_version}` : ""].filter(Boolean).join(" · ") || t("Agent has not reported node information yet.")}
        actions={<>
          <Button variant="outline" onClick={() => setEnrolling(true)} type="button"><KeyRound />{t(node.registered_at ? "Re-register Agent" : "Register Agent")}</Button>
          <Button
            variant="outline"
            disabled={updateUnavailable !== undefined}
            title={updateUnavailable === undefined ? undefined : t(updateUnavailable)}
            onClick={() => setUpdating(true)}
            type="button"
          ><RefreshCw />{t("Update Agent")}</Button>
          <Button onClick={() => setEditing(true)} type="button"><Pencil />{t("Edit node")}</Button>
          <DropdownMenu>
            <DropdownMenuTrigger asChild>
              <Button variant="outline" size="icon" aria-label={t("More node actions")}><EllipsisVertical /></Button>
            </DropdownMenuTrigger>
            <DropdownMenuContent align="end">
              <DropdownMenuItem disabled={changingEnabled} onSelect={() => { void toggleEnabled(); }}>
                <Power />{t(node.enabled ? "Disable node" : "Enable node")}
              </DropdownMenuItem>
              <DropdownMenuItem disabled={node.registered_at === null} onSelect={() => setRevoking(true)}>
                <ShieldX />{t("Revoke Agent credential")}
              </DropdownMenuItem>
              <DropdownMenuSeparator />
              <DropdownMenuItem variant="destructive" onSelect={() => setDeleting(true)}>
                <Trash2 />{t("Delete node")}
              </DropdownMenuItem>
            </DropdownMenuContent>
          </DropdownMenu>
        </>}
      />

      <FormError message={error !== undefined ? t(error) : undefined} />
      <Tabs defaultValue="overview" className="w-full min-w-0">
        <div className="mb-4 flex min-w-0 items-center justify-between gap-3">
          <TabsList className="min-w-0 flex-1 justify-start overflow-x-auto">
            <TabsTrigger value="overview"><Cpu />{t("Overview")}</TabsTrigger>
            {pluginPages.map((page) => {
              const Icon = pluginIcons[page.icon];
              return <TabsTrigger key={page.plugin.plugin_id} value={`plugin:${page.plugin.plugin_id}`}><Icon />{page.label}{page.unavailable ? <CircleAlert className="text-destructive" /> : null}</TabsTrigger>;
            })}
            <TabsTrigger value="runtimes"><Boxes />{t("Runtime instances")}</TabsTrigger>
            <TabsTrigger value="history"><Activity />{t("Execution history")}</TabsTrigger>
          </TabsList>
          <Button className="shrink-0" variant="outline" size="sm" disabled={refreshing} onClick={() => { void load(false); }} type="button">
            <RefreshCw className={refreshing ? "animate-spin" : undefined} /><span className="hidden sm:inline">{t("Refresh")}</span>
          </Button>
        </div>

        <TabsContent value="overview">
          <div className="grid gap-4 lg:grid-cols-2">
            <DetailCard title={t("Agent information")} description={t("Connection and runtime identity reported by the Agent.")}>
              <Detail label={t("Status")}><NodeStatus status={node.agent_status} /></Detail>
              <Detail label={t("Version")} value={node.agent_version || t("Not reported")} />
              <Detail label={t("Hostname")} value={node.hostname || t("Not reported")} />
              <Detail label={t("Platform")} value={[node.agent_os, node.agent_arch].filter(Boolean).join(" / ") || t("Not reported")} />
              <Detail label={t("Started")} value={formatOptional(node.agent_started_at, formatDateTime, t("Not reported"))} />
              <Detail label={t("Last seen")} value={formatOptional(node.last_seen_at, formatDateTime, t("Never"))} />
            </DetailCard>
            <DetailCard title={t("Node information")} description={t("Registration and policy state managed by Relayward.")}>
              <Detail label={t("State")}><StatusBadge tone={node.enabled ? "success" : "muted"}>{t(node.enabled ? "Enabled" : "Disabled")}</StatusBadge></Detail>
              <Detail label={t("Registration")} value={formatOptional(node.registered_at, formatDateTime, t("Not registered"))} />
              <Detail label={t("Policy")}><StatusBadge tone={policyTone(node)}>{t(policyLabel(node))}</StatusBadge></Detail>
              <Detail label={t("Policy generation")} value={node.policy ? `${node.policy.applied_generation} / ${node.policy.desired_generation}` : t("Not configured")} />
              <Detail label={t("Created")} value={formatDateTime(node.created_at)} />
              <Detail label={t("Updated")} value={formatDateTime(node.updated_at)} />
            </DetailCard>
          </div>
          <Card className="mt-4">
            <CardHeader><CardTitle>{t("Capabilities")}</CardTitle><CardDescription>{t("Features supported by the connected Agent.")}</CardDescription></CardHeader>
            <CardContent className="flex min-h-14 flex-wrap content-start gap-2">
              {node.capabilities.length > 0
                ? node.capabilities.map((capability) => <Badge variant="outline" key={capability}>{capability}</Badge>)
                : <span className="text-sm text-muted-foreground">{t("No reported capabilities")}</span>}
            </CardContent>
          </Card>
        </TabsContent>

        {pluginPages.map((page) => (
          <TabsContent className="min-w-0" key={page.plugin.plugin_id} value={`plugin:${page.plugin.plugin_id}`}>
            <Card className="overflow-hidden py-0">
              <PluginFrame plugin={page.plugin} title={page.label} nodeID={node.id} embedded onNavigate={onNavigate} />
            </Card>
          </TabsContent>
        ))}

        <TabsContent value="runtimes"><NodeRuntimeTable instances={instances} loading={loading} /></TabsContent>
        <TabsContent value="history"><NodeCommandTable commands={commands} loading={loading} /></TabsContent>
      </Tabs>

      {editing ? <NodeEditorDialog value={node} onClose={() => setEditing(false)} onSaved={(value) => { setNode(value); setEditing(false); }} /> : null}
      {enrolling ? (
        <NodeEnrollmentDialog
          key={`${node.id}:${node.registered_at ? "reregister" : "register"}`}
          node={node}
          mode={node.registered_at ? "reregister" : "register"}
          onClose={() => setEnrolling(false)}
          onNodeUpdated={setNode}
        />
      ) : null}
      {updating ? (
        <AgentUpdateDialog
          node={node}
          latest={latestUpdate}
          onClose={() => setUpdating(false)}
          onNodeUpdated={setNode}
          onUpdateChanged={(_, value) => setLatestUpdate(value)}
        />
      ) : null}
      {revoking ? (
        <ConfirmNodeAction
          title={t("Revoke Agent credential")}
          name={node.name}
          action={t("Revoke")}
          onClose={() => setRevoking(false)}
          onConfirm={async () => { setNode(await revokeNodeCredential(node.id)); setRevoking(false); }}
        />
      ) : null}
      {deleting ? (
        <ConfirmNodeAction
          title={t("Delete node")}
          name={node.name}
          action={t("Delete")}
          onClose={() => setDeleting(false)}
          onConfirm={async () => { await deleteNode(node.id); onDeleted(); }}
        />
      ) : null}
    </section>
  );
}

function NodeDetailsLoading({ onBack }: { onBack: () => void }) {
  const { t } = useI18n();
  return (
    <section aria-label={t("Loading...")}>
      <Button className="mb-4" variant="ghost" onClick={onBack} type="button"><ArrowLeft />{t("Back to nodes")}</Button>
      <div className="mb-6 space-y-2"><Skeleton className="h-4 w-24" /><Skeleton className="h-8 w-56 max-w-full" /><Skeleton className="h-4 w-80 max-w-full" /></div>
      <Skeleton className="mb-4 h-9 w-full max-w-xl" />
      <div className="grid gap-4 lg:grid-cols-2"><Skeleton className="h-80" /><Skeleton className="h-80" /></div>
    </section>
  );
}

function DetailCard({ title, description, children }: { title: string; description: string; children: ReactNode }) {
  return (
    <Card className="gap-0">
      <CardHeader className="border-b"><CardTitle>{title}</CardTitle><CardDescription>{description}</CardDescription></CardHeader>
      <CardContent className="px-0"><dl className="divide-y">{children}</dl></CardContent>
    </Card>
  );
}

function Detail({ label, value, children }: { label: string; value?: string; children?: ReactNode }) {
  return <div className="flex min-h-12 items-center justify-between gap-4 px-6 py-2.5"><dt className="text-sm text-muted-foreground">{label}</dt><dd className="m-0 min-w-0 truncate text-right text-sm font-medium" title={value}>{children ?? value}</dd></div>;
}

function NodeRuntimeTable({ instances, loading }: { instances: NodePluginInstance[]; loading: boolean }) {
  const { t, formatDateTime } = useI18n();
  return (
    <Card className="min-w-0">
      <CardHeader><CardTitle>{t("Runtime instances")}</CardTitle><CardDescription>{t("Runtime plugin state reported by this node.")}</CardDescription></CardHeader>
      <CardContent><div className="min-w-0 overflow-hidden rounded-lg border"><Table className="min-w-[680px]">
        <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Plugin")}</TableHead><TableHead>{t("Desired")}</TableHead><TableHead>{t("Actual")}</TableHead><TableHead>{t("Generation")}</TableHead><TableHead>{t("Health")}</TableHead><TableHead>{t("Updated")}</TableHead></TableRow></TableHeader>
        <TableBody>{instances.map((instance) => (
          <TableRow key={instance.plugin_id}>
            <TableCell><span className="grid gap-0.5"><strong className="font-semibold">{instance.plugin_name}</strong><small className="text-xs text-muted-foreground">{instance.plugin_id}</small></span></TableCell>
            <TableCell><StateBadge value={instance.desired_state} /></TableCell><TableCell><StateBadge value={instance.actual_state} /></TableCell>
            <TableCell className="text-muted-foreground">{instance.actual_generation} / {instance.generation}</TableCell>
            <TableCell><StatusBadge tone={instance.health === "healthy" ? "success" : instance.health === "unhealthy" ? "danger" : "muted"}>{t(titleCase(instance.health))}</StatusBadge></TableCell>
            <TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(instance.updated_at)}</TableCell>
          </TableRow>
        ))}</TableBody>
      </Table>{!loading && instances.length === 0 ? <div className="flex min-h-24 items-center justify-center border-t px-4 text-center text-sm text-muted-foreground">{t("No runtime instances are configured on this node.")}</div> : null}</div></CardContent>
    </Card>
  );
}

function NodeCommandTable({ commands, loading }: { commands: NodeCommand[]; loading: boolean }) {
  const { t, formatDateTime } = useI18n();
  return (
    <Card className="min-w-0">
      <CardHeader><CardTitle>{t("Execution history")}</CardTitle><CardDescription>{t("Commands delivered to the Agent and their results.")}</CardDescription></CardHeader>
      <CardContent><div className="min-w-0 overflow-hidden rounded-lg border"><Table className="min-w-[680px]">
        <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Operation")}</TableHead><TableHead>{t("Scope")}</TableHead><TableHead>{t("Status")}</TableHead><TableHead>{t("Delivery attempts")}</TableHead><TableHead>{t("Created")}</TableHead><TableHead>{t("Completed")}</TableHead></TableRow></TableHeader>
        <TableBody>{commands.map((command) => (
          <TableRow key={command.id}>
            <TableCell><span className="grid max-w-[220px] gap-0.5"><strong className="font-semibold">{t(commandKindLabel(command.kind))}</strong><small className="truncate text-xs text-muted-foreground" title={command.id}>{command.id}</small></span></TableCell>
            <TableCell className="text-muted-foreground">{command.scope_key || t(commandScopeLabel(command.kind))}</TableCell>
            <TableCell><span className="grid max-w-[180px] gap-0.5" title={command.problem?.message}><StatusBadge tone={commandTone(command.status)}>{t(titleCase(command.status))}</StatusBadge>{command.problem?.message ? <small className="truncate text-xs text-destructive">{t(command.problem.message)}</small> : null}</span></TableCell>
            <TableCell className="text-muted-foreground">{command.attempts}</TableCell><TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(command.created_at)}</TableCell>
            <TableCell className="whitespace-nowrap text-muted-foreground">{command.completed_at ? formatDateTime(command.completed_at) : t("Not yet")}</TableCell>
          </TableRow>
        ))}</TableBody>
      </Table>{!loading && commands.length === 0 ? <div className="flex min-h-24 items-center justify-center border-t px-4 text-center text-sm text-muted-foreground">{t("No node commands have been recorded.")}</div> : null}</div></CardContent>
    </Card>
  );
}

function NodeStatus({ status }: { status: Node["agent_status"] }) {
  const { t } = useI18n();
  return <StatusBadge tone={status === "online" ? "success" : status === "disabled" ? "muted" : "warning"}>{t(titleCase(status))}</StatusBadge>;
}

function StateBadge({ value }: { value: NodePluginInstance["desired_state"] | NodePluginInstance["actual_state"] }) {
  const { t } = useI18n();
  const tone = value === "running" ? "success" : value === "failed" ? "danger" : value === "stopped" ? "warning" : "muted";
  return <StatusBadge tone={tone}>{t(titleCase(value))}</StatusBadge>;
}

function policyLabel(node: Node): string {
  return node.policy ? titleCase(node.policy.status) : "Not configured";
}

function policyTone(node: Node): "success" | "warning" | "danger" | "muted" {
  if (!node.policy) return "muted";
  if (node.policy.status === "applied") return "success";
  if (node.policy.status === "failed" || node.policy.status === "unsupported") return "danger";
  return "warning";
}

function commandKindLabel(kind: string): string {
  if (kind === "agent.update") return "Agent update";
  if (kind === "plugin.reconcile") return "Runtime reconciliation";
  if (kind === "policy.reconcile") return "Authorization policy reconciliation";
  return kind;
}

function commandScopeLabel(kind: string): string {
  if (kind === "agent.update") return "Agent";
  if (kind === "policy.reconcile") return "Node policy";
  return "Node";
}

function commandTone(status: NodeCommand["status"]): "success" | "warning" | "danger" | "muted" {
  if (status === "succeeded") return "success";
  if (status === "pending") return "warning";
  if (status === "failed") return "danger";
  return "muted";
}

function agentUpdateUnavailable(node: Node): string | undefined {
  if (!node.enabled) return "Enable the node before updating its Agent";
  if (node.registered_at === null) return "Register the Agent before updating it";
  if (!node.capabilities.includes("control.commands")) return "The Agent does not support durable commands";
  if (!node.capabilities.includes("agent.self_update")) return "The Agent does not support self-update";
  return undefined;
}

function formatOptional(value: string | null, format: (value: string) => string, fallback: string): string {
  return value ? format(value) : fallback;
}

function titleCase(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1).replaceAll("_", " ");
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.status === 404 ? "The node no longer exists." : cause.message;
  return "The request could not be completed.";
}
