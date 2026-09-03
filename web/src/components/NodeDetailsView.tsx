import { useCallback, useEffect, useRef, useState } from "react";
import {
  ArrowLeft,
  ChevronLeft,
  ChevronRight,
  CircleAlert,
  EllipsisVertical,
  HeartPulse,
  KeyRound,
  Pencil,
  Power,
  RefreshCw,
  Server,
  ShieldCheck,
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
import { AgentUpdateDialog } from "./AgentUpdateDialog";
import { FormError } from "./AuthScreen";
import { ConfirmNodeAction, NodeEditorDialog } from "./NodeActions";
import { NodeEnrollmentDialog } from "./NodeEnrollmentDialog";
import { NodeEndpointsPanel } from "./NodeEndpointsPanel";
import { PageHeader, StatusBadge, SummaryBar, SummaryItem } from "./PageLayout";
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

export function NodeDetailsView({ nodeID, pluginPages, onBack, onDeleted, onOpenDDNS, onNavigate }: {
  nodeID: string;
  pluginPages: PluginNodeDetailPage[];
  onBack: () => void;
  onDeleted: () => void;
  onOpenDDNS: () => void;
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
          <Button variant="outline" size="icon" aria-label={t("Refresh")} disabled={refreshing} onClick={() => { void load(false); }} type="button">
            <RefreshCw className={refreshing ? "animate-spin" : undefined} />
          </Button>
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
      <Tabs defaultValue="overview" className="w-full min-w-0 gap-4">
        <NodeTabNavigation pluginPages={pluginPages} />

        <TabsContent value="overview">
          <SummaryBar className="mb-4">
            <SummaryItem
              icon={<HeartPulse size={17} />}
              label={t("Agent")}
              value={t(titleCase(node.agent_status))}
              note={t("Last heartbeat {time}", { time: formatOptional(node.last_seen_at, formatDateTime, t("Never")) })}
              tone={agentTone(node.agent_status)}
            />
            <SummaryItem
              icon={<Power size={17} />}
              label={t("Node")}
              value={t(node.enabled ? "Enabled" : "Disabled")}
              note={node.registered_at ? t("Registered {time}", { time: formatDateTime(node.registered_at) }) : t("Not registered")}
              tone={node.enabled ? "success" : "default"}
            />
            <SummaryItem
              icon={<Server size={17} />}
              label={t("Runtime instances")}
              value={`${healthyRuntimeCount(instances)} / ${instances.length}`}
              note={instances.length > 0 ? t("Healthy instances") : t("No runtime instances are configured on this node.")}
              tone={runtimeTone(instances)}
            />
            <SummaryItem
              icon={<ShieldCheck size={17} />}
              label={t("Policy")}
              value={t(policyLabel(node))}
              note={t("Policy generation {generation}", { generation: node.policy ? `${node.policy.applied_generation} / ${node.policy.desired_generation}` : t("Not configured") })}
              tone={summaryPolicyTone(node)}
            />
          </SummaryBar>

          <Card>
            <CardHeader>
              <CardTitle>{t("Node information")}</CardTitle>
              <CardDescription>{t("Connection and runtime identity reported by the Agent.")}</CardDescription>
            </CardHeader>
            <CardContent>
              <dl className="grid gap-x-8 gap-y-6 sm:grid-cols-2 xl:grid-cols-3">
                <Detail label={t("Hostname")} value={node.hostname || t("Not reported")} />
                <Detail label={t("Version")} value={node.agent_version || t("Not reported")} />
                <Detail label={t("Platform")} value={[node.agent_os, node.agent_arch].filter(Boolean).join(" / ") || t("Not reported")} />
                <Detail label={t("Started")} value={formatOptional(node.agent_started_at, formatDateTime, t("Not reported"))} />
                <Detail label={t("Created")} value={formatDateTime(node.created_at)} />
                <Detail label={t("Updated")} value={formatDateTime(node.updated_at)} />
              </dl>
            </CardContent>
            <div className="border-t px-6 pt-6">
              <div className="mb-3 grid gap-1">
                <strong className="text-sm font-medium">{t("Capabilities")}</strong>
                <span className="text-sm text-muted-foreground">{t("Features supported by the connected Agent.")}</span>
              </div>
              <div className="flex min-h-6 flex-wrap gap-2">
                {node.capabilities.length > 0
                  ? node.capabilities.map((capability) => <Badge variant="outline" key={capability}>{capability}</Badge>)
                  : <span className="text-sm text-muted-foreground">{t("No reported capabilities")}</span>}
              </div>
            </div>
          </Card>
        </TabsContent>

        <TabsContent value="endpoints"><NodeEndpointsPanel nodeID={node.id} onManageDDNS={onOpenDDNS} /></TabsContent>

        {pluginPages.map((page) => (
          <TabsContent className="min-w-0 data-[state=inactive]:hidden" forceMount key={page.plugin.plugin_id} value={`plugin:${page.plugin.plugin_id}`}>
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

function NodeTabNavigation({ pluginPages }: { pluginPages: PluginNodeDetailPage[] }) {
  const { locale, t } = useI18n();
  const listRef = useRef<HTMLDivElement>(null);
  const [canScrollStart, setCanScrollStart] = useState(false);
  const [canScrollEnd, setCanScrollEnd] = useState(false);
  const pageLabels = pluginPages.map((page) => page.label).join("\u0000");

  const updateOverflow = useCallback(() => {
    const list = listRef.current;
    if (!list) return;
    const maxScroll = Math.max(0, list.scrollWidth - list.clientWidth);
    const current = Math.min(maxScroll, Math.max(0, list.scrollLeft));
    setCanScrollStart(current > 1);
    setCanScrollEnd(maxScroll - current > 1);
  }, []);

  useEffect(() => {
    const list = listRef.current;
    if (!list) return;
    const frame = window.requestAnimationFrame(updateOverflow);
    const observer = new ResizeObserver(updateOverflow);
    observer.observe(list);
    window.addEventListener("resize", updateOverflow);
    return () => {
      window.cancelAnimationFrame(frame);
      observer.disconnect();
      window.removeEventListener("resize", updateOverflow);
    };
  }, [locale, pageLabels, updateOverflow]);

  function scrollTabs(direction: -1 | 1) {
    const list = listRef.current;
    if (!list) return;
    list.scrollBy({ left: direction * Math.max(160, Math.floor(list.clientWidth * 0.7)), behavior: "smooth" });
  }

  return (
    <div className="relative min-w-0">
      <TabsList ref={listRef} variant="underline" aria-label={t("Node details")} onScroll={updateOverflow}>
        <TabsTrigger variant="underline" value="overview">{t("Overview")}</TabsTrigger>
        <TabsTrigger variant="underline" value="endpoints">{t("Subscription endpoints")}</TabsTrigger>
        {pluginPages.map((page) => (
          <TabsTrigger variant="underline" key={page.plugin.plugin_id} value={`plugin:${page.plugin.plugin_id}`}>
            {page.label}{page.unavailable ? <CircleAlert className="text-destructive" /> : null}
          </TabsTrigger>
        ))}
        <TabsTrigger variant="underline" value="runtimes">{t("Runtime instances")}</TabsTrigger>
        <TabsTrigger variant="underline" value="history">{t("Execution history")}</TabsTrigger>
      </TabsList>
      {canScrollStart ? (
        <Button className="absolute top-1/2 left-0 z-10 size-8 -translate-y-1/2 bg-background shadow-sm" variant="outline" size="icon" aria-label={t("Scroll tabs left")} onClick={() => scrollTabs(-1)} type="button">
          <ChevronLeft />
        </Button>
      ) : null}
      {canScrollEnd ? (
        <Button className="absolute top-1/2 right-0 z-10 size-8 -translate-y-1/2 bg-background shadow-sm" variant="outline" size="icon" aria-label={t("Scroll tabs right")} onClick={() => scrollTabs(1)} type="button">
          <ChevronRight />
        </Button>
      ) : null}
    </div>
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

function Detail({ label, value }: { label: string; value: string }) {
  return <div className="grid min-w-0 gap-1"><dt className="text-sm text-muted-foreground">{label}</dt><dd className="m-0 truncate text-sm font-medium" title={value}>{value}</dd></div>;
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

function agentTone(status: Node["agent_status"]): "success" | "warning" | "danger" | "default" {
  if (status === "online") return "success";
  if (status === "offline") return "danger";
  if (status === "disabled") return "default";
  return "warning";
}

function healthyRuntimeCount(instances: NodePluginInstance[]): number {
  return instances.filter((instance) => instance.actual_state === "running" && instance.health === "healthy").length;
}

function runtimeTone(instances: NodePluginInstance[]): "success" | "warning" | "danger" | "default" {
  if (instances.length === 0) return "default";
  const healthy = healthyRuntimeCount(instances);
  if (healthy === instances.length) return "success";
  if (instances.some((instance) => instance.actual_state === "failed" || instance.health === "unhealthy")) return "danger";
  return "warning";
}

function summaryPolicyTone(node: Node): "success" | "warning" | "danger" | "default" {
  const tone = policyTone(node);
  return tone === "muted" ? "default" : tone;
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
