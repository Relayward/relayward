import { useEffect, useState, type ReactNode } from "react";
import { Activity, Boxes, Cpu, RefreshCw } from "lucide-react";

import {
  APIError,
  listNodeCommands,
  listNodePluginInstances,
  type Node,
  type NodeCommand,
  type NodePluginInstance,
} from "../api";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { StatusBadge } from "./PageLayout";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

export function NodeDetailsDialog({ node, onClose }: { node: Node; onClose: () => void }) {
  const { t, formatDateTime } = useI18n();
  const [commands, setCommands] = useState<NodeCommand[]>([]);
  const [instances, setInstances] = useState<NodePluginInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [refreshKey, setRefreshKey] = useState(0);

  useEffect(() => {
    let active = true;
    const load = async (showLoading: boolean) => {
      if (showLoading && active) setLoading(true);
      try {
        const [nextCommands, allInstances] = await Promise.all([
          listNodeCommands(node.id),
          listNodePluginInstances(),
        ]);
        if (active) {
          setCommands(nextCommands);
          setInstances(allInstances.filter((instance) => instance.node_id === node.id));
          setError(undefined);
        }
      } catch (cause) {
        if (active) setError(errorMessage(cause));
      } finally {
        if (active && showLoading) setLoading(false);
      }
    };
    void load(true);
    const timer = window.setInterval(() => { void load(false); }, 10_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [node.id, refreshKey]);

  return (
    <Modal title={t("{name} node details", { name: node.name })} onClose={onClose} width="wide">
      <div className="flex min-h-0 min-w-0 flex-col gap-4">
        <div className="flex justify-end">
          <Button variant="outline" size="sm" disabled={loading} onClick={() => setRefreshKey((value) => value + 1)} type="button">
            <RefreshCw className={loading ? "animate-spin" : undefined} />{t("Refresh")}
          </Button>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <Tabs defaultValue="overview" className="min-h-0 min-w-0">
          <TabsList className="max-w-full overflow-x-auto">
            <TabsTrigger value="overview"><Cpu />{t("Overview")}</TabsTrigger>
            <TabsTrigger value="runtimes"><Boxes />{t("Runtime instances")}</TabsTrigger>
            <TabsTrigger value="history"><Activity />{t("Execution history")}</TabsTrigger>
          </TabsList>
          <TabsContent value="overview" className="pt-2">
            <div className="grid gap-4 sm:grid-cols-2">
              <DetailGroup title={t("Agent information")}>
                <Detail label={t("Status")}><NodeStatus status={node.agent_status} /></Detail>
                <Detail label={t("Version")} value={node.agent_version || t("Not reported")} />
                <Detail label={t("Hostname")} value={node.hostname || t("Not reported")} />
                <Detail label={t("Platform")} value={[node.agent_os, node.agent_arch].filter(Boolean).join(" / ") || t("Not reported")} />
                <Detail label={t("Started")} value={formatOptional(node.agent_started_at, formatDateTime, t("Not reported"))} />
                <Detail label={t("Last seen")} value={formatOptional(node.last_seen_at, formatDateTime, t("Never"))} />
              </DetailGroup>
              <DetailGroup title={t("Node information")}>
                <Detail label={t("Registration")} value={formatOptional(node.registered_at, formatDateTime, t("Not registered"))} />
                <Detail label={t("Policy")}>
                  <StatusBadge tone={policyTone(node)}>{t(policyLabel(node))}</StatusBadge>
                </Detail>
                <Detail label={t("Policy generation")} value={node.policy ? `${node.policy.applied_generation} / ${node.policy.desired_generation}` : t("Not configured")} />
                <Detail label={t("Created")} value={formatDateTime(node.created_at)} />
                <Detail label={t("Updated")} value={formatDateTime(node.updated_at)} />
              </DetailGroup>
            </div>
            <section className="mt-4 space-y-2" aria-labelledby="node-capabilities-title">
              <h3 className="m-0 text-sm font-semibold" id="node-capabilities-title">{t("Capabilities")}</h3>
              <div className="flex min-h-14 flex-wrap content-start gap-2 rounded-lg border p-4">
                {node.capabilities.length > 0
                  ? node.capabilities.map((capability) => <Badge variant="outline" key={capability}>{capability}</Badge>)
                  : <span className="text-sm text-muted-foreground">{t("No reported capabilities")}</span>}
              </div>
            </section>
          </TabsContent>
          <TabsContent value="runtimes" className="min-w-0 pt-2">
            <div className="min-w-0 overflow-hidden rounded-lg border">
              <Table className="min-w-[680px]">
                <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Plugin")}</TableHead><TableHead>{t("Desired")}</TableHead><TableHead>{t("Actual")}</TableHead><TableHead>{t("Generation")}</TableHead><TableHead>{t("Health")}</TableHead><TableHead>{t("Updated")}</TableHead></TableRow></TableHeader>
                <TableBody>
                  {instances.map((instance) => (
                    <TableRow key={instance.plugin_id}>
                      <TableCell><span className="grid gap-0.5"><strong className="font-semibold">{instance.plugin_name}</strong><small className="text-xs text-muted-foreground">{instance.plugin_id}</small></span></TableCell>
                      <TableCell><StateBadge value={instance.desired_state} /></TableCell>
                      <TableCell><StateBadge value={instance.actual_state} /></TableCell>
                      <TableCell className="text-muted-foreground">{instance.actual_generation} / {instance.generation}</TableCell>
                      <TableCell><StatusBadge tone={instance.health === "healthy" ? "success" : instance.health === "unhealthy" ? "danger" : "muted"}>{t(titleCase(instance.health))}</StatusBadge></TableCell>
                      <TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(instance.updated_at)}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {!loading && instances.length === 0 ? <div className="flex min-h-24 items-center justify-center border-t px-4 text-center text-sm text-muted-foreground">{t("No runtime instances are configured on this node.")}</div> : null}
              {loading ? <div className="flex min-h-24 items-center justify-center border-t px-4 text-center text-sm text-muted-foreground">{t("Loading...")}</div> : null}
            </div>
          </TabsContent>
          <TabsContent value="history" className="min-w-0 pt-2">
            <div className="min-w-0 overflow-hidden rounded-lg border">
              <Table className="min-w-[680px]">
                <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Operation")}</TableHead><TableHead>{t("Scope")}</TableHead><TableHead>{t("Status")}</TableHead><TableHead>{t("Delivery attempts")}</TableHead><TableHead>{t("Created")}</TableHead><TableHead>{t("Completed")}</TableHead></TableRow></TableHeader>
                <TableBody>
                  {commands.map((command) => (
                    <TableRow key={command.id}>
                      <TableCell><span className="grid max-w-[220px] gap-0.5"><strong className="font-semibold">{t(commandKindLabel(command.kind))}</strong><small className="truncate text-xs text-muted-foreground" title={command.id}>{command.id}</small></span></TableCell>
                      <TableCell className="text-muted-foreground">{command.scope_key || t(commandScopeLabel(command.kind))}</TableCell>
                      <TableCell><span className="grid max-w-[180px] gap-0.5" title={command.problem?.message}><StatusBadge tone={commandTone(command.status)}>{t(titleCase(command.status))}</StatusBadge>{command.problem?.message ? <small className="truncate text-xs text-destructive">{t(command.problem.message)}</small> : null}</span></TableCell>
                      <TableCell className="text-muted-foreground">{command.attempts}</TableCell>
                      <TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(command.created_at)}</TableCell>
                      <TableCell className="whitespace-nowrap text-muted-foreground">{command.completed_at ? formatDateTime(command.completed_at) : t("Not yet")}</TableCell>
                    </TableRow>
                  ))}
                </TableBody>
              </Table>
              {!loading && commands.length === 0 ? <div className="flex min-h-24 items-center justify-center border-t px-4 text-center text-sm text-muted-foreground">{t("No node commands have been recorded.")}</div> : null}
              {loading ? <div className="flex min-h-24 items-center justify-center border-t px-4 text-center text-sm text-muted-foreground">{t("Loading...")}</div> : null}
            </div>
          </TabsContent>
        </Tabs>
      </div>
    </Modal>
  );
}

function DetailGroup({ title, children }: { title: string; children: ReactNode }) {
  return <section className="space-y-2"><h3 className="m-0 text-sm font-semibold">{title}</h3><dl className="divide-y rounded-lg border">{children}</dl></section>;
}

function Detail({ label, value, children }: { label: string; value?: string; children?: ReactNode }) {
  return <div className="flex min-h-12 items-center justify-between gap-4 px-4 py-2.5"><dt className="text-sm text-muted-foreground">{label}</dt><dd className="m-0 min-w-0 truncate text-right text-sm font-medium" title={value}>{children ?? value}</dd></div>;
}

function NodeStatus({ status }: { status: Node["agent_status"] }) {
  const { t } = useI18n();
  const tone = status === "online" ? "success" : status === "disabled" ? "muted" : "warning";
  return <StatusBadge tone={tone}>{t(titleCase(status))}</StatusBadge>;
}

function StateBadge({ value }: { value: NodePluginInstance["desired_state"] | NodePluginInstance["actual_state"] }) {
  const { t } = useI18n();
  const tone = value === "running" ? "success" : value === "failed" ? "danger" : value === "stopped" ? "warning" : "muted";
  return <StatusBadge tone={tone}>{t(titleCase(value))}</StatusBadge>;
}

function policyLabel(node: Node): string {
  if (!node.policy) return "Not configured";
  return titleCase(node.policy.status);
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

function formatOptional(value: string | null, format: (value: string) => string, fallback: string): string {
  return value ? format(value) : fallback;
}

function titleCase(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1).replaceAll("_", " ");
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
