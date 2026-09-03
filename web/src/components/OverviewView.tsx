import { useCallback, useEffect, useMemo, useState } from "react";
import { AlertTriangle, ArrowRight, CheckCircle2, RefreshCw, Server } from "lucide-react";

import {
  APIError,
  listAudit,
  listAuthorizations,
  listNodePluginInstances,
  listNodes,
  listUsers,
  type AuditEntry,
  type Authorization,
  type Node,
  type NodePluginInstance,
  type SessionInfo,
  type User,
} from "../api";
import { auditActionLabel, auditTargetTypeLabel } from "../auditPresentation";
import { useI18n } from "../i18n";
import type { SystemInfo } from "../system";
import { FormError } from "./AuthScreen";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "./ui/card";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";

type OverviewTarget = "nodes" | "plugins" | "authorizations" | "audit";
type IssueTone = "warning" | "danger";
type Translate = ReturnType<typeof useI18n>["t"];

export interface OverviewData {
  nodes: Node[];
  users: User[];
  authorizations: Authorization[];
  instances: NodePluginInstance[];
  audit: AuditEntry[];
}

interface OverviewIssue {
  key: string;
  title: string;
  detail: string;
  target: OverviewTarget;
  tone: IssueTone;
}

export function OverviewView({ system, session, onNavigate }: {
  system: SystemInfo;
  session: SessionInfo;
  onNavigate: (target: OverviewTarget) => void;
}) {
  const { t, formatDateTime } = useI18n();
  const [data, setData] = useState<OverviewData>();
  const [error, setError] = useState<string>();
  const [loading, setLoading] = useState(true);
  const [refreshedAt, setRefreshedAt] = useState<Date>();

  const refresh = useCallback(async () => {
    setLoading(true);
    setError(undefined);
    try {
      const [nodes, users, authorizations, instances, audit] = await Promise.all([
        listNodes(), listUsers(), listAuthorizations(), listNodePluginInstances(), listAudit(undefined, 40),
      ]);
      setData({ nodes, users, authorizations, instances, audit });
      setRefreshedAt(new Date());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
    }
  }, []);

  useEffect(() => { void refresh(); }, [refresh]);

  const now = refreshedAt ?? new Date();
  const names = useMemo(() => buildNames(data), [data]);
  const operationalIssues = useMemo(
    () => buildOperationalIssues(data, session.secrets_available, now, t, formatDateTime),
    [data, formatDateTime, now, session.secrets_available, t],
  );
  const authorizationRisks = useMemo(
    () => buildAuthorizationRisks(data, now, names, t),
    [data, names, now, t],
  );
  const totalIssues = operationalIssues.length + authorizationRisks.length;
  const onlineNodes = data?.nodes.filter((node) => node.agent_status === "online").length ?? 0;
  const activeNodes = data?.nodes.filter((node) => node.enabled).length ?? 0;
  const healthyInstances = data?.instances.filter((instance) => instance.desired_state !== "absent" && instanceHealthy(instance)).length ?? 0;
  const expectedInstances = data?.instances.filter((instance) => instance.desired_state !== "absent").length ?? 0;
  const appliedPolicies = data?.nodes.filter((node) => node.policy?.status === "applied").length ?? 0;
  const configuredPolicies = data?.nodes.filter((node) => node.policy !== null).length ?? 0;
  const recentChanges = useMemo(() => uniqueRecentChanges(data?.audit ?? []).slice(0, 6), [data]);
  const attentionItems = useMemo(() => [...operationalIssues, ...authorizationRisks].slice(0, 6), [authorizationRisks, operationalIssues]);

  return (
    <section className="flex-1 space-y-6 pt-0" aria-labelledby="system-title">
      <div className="flex md:flex-row flex-col md:items-center justify-between gap-4 md:gap-6">
        <div className="flex flex-col gap-2">
          <h1 className="text-2xl font-bold tracking-tight" id="system-title">{t("System overview")}</h1>
          <p className="text-muted-foreground">{t("Review system health, unresolved risks, and recent control-plane changes.")}</p>
        </div>
        <Button variant="outline" className="cursor-pointer" disabled={loading} onClick={() => void refresh()} type="button">
          <RefreshCw className={loading ? "animate-spin" : ""} />
          {t("Refresh")}
        </Button>
      </div>

      {error ? <FormError message={t(error)} /> : null}

      <div className="@container/main space-y-6">
        <div className="*:data-[slot=card]:from-primary/5 *:data-[slot=card]:to-card dark:*:data-[slot=card]:bg-card *:data-[slot=card]:bg-gradient-to-t *:data-[slot=card]:shadow-xs grid gap-4 sm:grid-cols-2 @5xl:grid-cols-4">
          <OverviewMetric
            label={t("Agent heartbeat")}
            value={`${onlineNodes} / ${activeNodes}`}
            badge={t("Nodes {online}/{total}", { online: onlineNodes, total: activeNodes })}
            healthy={onlineNodes === activeNodes}
            footer={onlineNodes === activeNodes ? t("All core systems are operational") : t("{count} items need attention", { count: activeNodes - onlineNodes })}
            detail={t("Current Agent heartbeat, runtime, policy, and access status by node.")}
          />
          <OverviewMetric
            label={t("Runtime")}
            value={`${healthyInstances} / ${expectedInstances}`}
            badge={t("Runtimes {healthy}/{total}", { healthy: healthyInstances, total: expectedInstances })}
            healthy={healthyInstances === expectedInstances}
            footer={healthyInstances === expectedInstances ? t("All core systems are operational") : t("{count} items need attention", { count: expectedInstances - healthyInstances })}
            detail={t("Agent, runtime, policy, and secret-storage problems.")}
          />
          <OverviewMetric
            label={t("Policy")}
            value={`${appliedPolicies} / ${configuredPolicies}`}
            badge={t("Policies {applied}/{total}", { applied: appliedPolicies, total: configuredPolicies })}
            healthy={appliedPolicies === configuredPolicies}
            footer={appliedPolicies === configuredPolicies ? t("All core systems are operational") : t("{count} items need attention", { count: configuredPolicies - appliedPolicies })}
            detail={t("The desired authorization policy is not active on this node.")}
          />
          <OverviewMetric
            label={t("Operational attention")}
            value={String(totalIssues)}
            badge={t("{count} items need attention", { count: totalIssues })}
            healthy={totalIssues === 0}
            footer={totalIssues === 0 ? t("All core systems are operational") : t("Resolve the highest-impact items below first.")}
            detail={t("Quota, expiry, enforcement, and blocked-IP conditions.")}
          />
        </div>

        <div className="grid gap-6 grid-cols-1 @5xl:grid-cols-2">
          <AttentionCard items={attentionItems} loaded={data !== undefined} onNavigate={onNavigate} t={t} />
          <RecentChangesCard entries={recentChanges} loaded={data !== undefined} names={names} onNavigate={onNavigate} t={t} formatDateTime={formatDateTime} />
        </div>

        <Card className="min-w-0 h-fit">
          <CardHeader className="flex flex-col items-start justify-between space-y-0 gap-4 pb-4 sm:flex-row sm:items-center">
            <div className="min-w-0">
              <CardTitle>{t("Node operations")}</CardTitle>
              <CardDescription>{t("Current Agent heartbeat, runtime, policy, and access status by node.")}</CardDescription>
            </div>
            <Button variant="outline" size="sm" className="shrink-0 cursor-pointer" onClick={() => onNavigate("nodes")} type="button">
              {t("View")}
              <ArrowRight />
            </Button>
          </CardHeader>
          <CardContent>
            <div className="rounded-lg border bg-card">
              <Table className="min-w-[820px]">
                <TableHeader>
                  <TableRow className="border-b">
                    <TableHead className="py-5 px-6 font-semibold">{t("Node")}</TableHead>
                    <TableHead className="py-5 px-6 font-semibold">{t("Agent heartbeat")}</TableHead>
                    <TableHead className="py-5 px-6 font-semibold">{t("Runtime")}</TableHead>
                    <TableHead className="py-5 px-6 font-semibold">{t("Policy")}</TableHead>
                    <TableHead className="py-5 px-6 font-semibold">{t("Authorizations")}</TableHead>
                  </TableRow>
                </TableHeader>
                <TableBody>
                  {(data?.nodes ?? []).map((node) => {
                    const instances = data?.instances.filter((instance) => instance.node_id === node.id && instance.desired_state !== "absent") ?? [];
                    const authorizations = data?.authorizations.filter((authorization) => authorization.node_id === node.id && authorization.enabled) ?? [];
                    const risks = authorizationRisks.filter((issue) => issue.key.startsWith(`authorization:${node.id}:`)).length;
                    return (
                      <TableRow key={node.id} className="hover:bg-muted/30 transition-colors">
                        <TableCell className="font-medium py-5 px-6">
                          <span className="grid gap-0.5"><strong className="font-semibold">{node.name}</strong><small className="text-xs text-muted-foreground">{node.hostname || t("Not reported")}</small></span>
                        </TableCell>
                        <TableCell className="py-5 px-6"><AgentState node={node} now={now} t={t} /></TableCell>
                        <TableCell className="py-5 px-6"><RuntimeState instances={instances} t={t} /></TableCell>
                        <TableCell className="py-5 px-6"><PolicyState node={node} t={t} /></TableCell>
                        <TableCell className="py-5 px-6"><StateBadge healthy={risks === 0}>{risks > 0 ? t("{count} at risk", { count: risks }) : t("{count} active", { count: authorizations.length })}</StateBadge></TableCell>
                      </TableRow>
                    );
                  })}
                  {data && data.nodes.length === 0 ? <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{t("No nodes have been created.")}</TableCell></TableRow> : null}
                  {!data ? <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{t("Loading...")}</TableCell></TableRow> : null}
                </TableBody>
              </Table>
            </div>
          </CardContent>
        </Card>
      </div>

      <p className="text-right text-xs text-muted-foreground">{refreshedAt ? t("Updated {time}", { time: formatDateTime(refreshedAt) }) : t("Loading...")} · {system.version}</p>
    </section>
  );
}

function OverviewMetric({ label, value, badge, healthy, footer, detail }: {
  label: string;
  value: string;
  badge: string;
  healthy: boolean;
  footer: string;
  detail: string;
}) {
  const Icon = healthy ? CheckCircle2 : AlertTriangle;
  return (
    <Card className="@container/card">
      <CardHeader>
        <CardDescription>{label}</CardDescription>
        <CardTitle className="text-2xl font-semibold tabular-nums @[250px]/card:text-3xl">{value}</CardTitle>
        <CardAction><Badge variant="outline" className={healthy ? "border-success/20 bg-success-soft text-success-strong" : "border-warning/20 bg-warning-soft text-warning-strong"}><Icon />{badge}</Badge></CardAction>
      </CardHeader>
      <CardFooter className="flex-col items-start gap-1.5 text-sm">
        <div className={healthy ? "line-clamp-1 flex gap-2 font-medium text-success-strong" : "line-clamp-1 flex gap-2 font-medium text-warning-strong"}>{footer}<Icon className="size-4" /></div>
        <div className="text-muted-foreground">{detail}</div>
      </CardFooter>
    </Card>
  );
}

function AttentionCard({ items, loaded, onNavigate, t }: {
  items: OverviewIssue[];
  loaded: boolean;
  onNavigate: (target: OverviewTarget) => void;
  t: Translate;
}) {
  return (
    <Card className="min-w-0 cursor-pointer">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 gap-4 pb-4">
        <div className="min-w-0">
          <CardTitle>{t("Operational attention")}</CardTitle>
          <CardDescription>{t("Resolve the highest-impact items below first.")}</CardDescription>
        </div>
        {items[0] ? <Button variant="outline" size="sm" className="shrink-0 cursor-pointer" onClick={() => onNavigate(items[0].target)} type="button">{t("View")}<ArrowRight /></Button> : null}
      </CardHeader>
      <CardContent className="space-y-3">
        {items.map((item) => (
          <div className="flex p-3 rounded-lg border gap-2" key={item.key}>
            <div className={item.tone === "danger" ? "flex size-8 shrink-0 items-center justify-center rounded-full bg-destructive-soft text-destructive" : "flex size-8 shrink-0 items-center justify-center rounded-full bg-warning-soft text-warning-strong"}><AlertTriangle className="h-4 w-4" /></div>
            <div className="flex min-w-0 flex-1 items-center flex-wrap justify-between gap-1">
              <div className="flex min-w-0 items-center space-x-3"><div className="min-w-0 flex-1"><p className="text-sm font-medium truncate">{item.title}</p><p className="text-xs text-muted-foreground truncate">{item.detail}</p></div></div>
              <div className="flex items-center space-x-3"><Badge variant={item.tone === "danger" ? "destructive" : "outline"} className={item.tone === "danger" ? undefined : "border-warning/20 bg-warning-soft text-warning-strong"}>{t(item.tone === "danger" ? "Failure" : "Pending")}</Badge><Button variant="ghost" size="sm" className="h-8 cursor-pointer" onClick={() => onNavigate(item.target)} type="button">{t("View")}</Button></div>
            </div>
          </div>
        ))}
        {loaded && items.length === 0 ? <div className="flex p-3 rounded-lg border gap-2"><div className="flex size-8 shrink-0 items-center justify-center rounded-full bg-success-soft text-success-strong"><CheckCircle2 className="h-4 w-4" /></div><div className="flex flex-1 items-center"><p className="text-sm font-medium">{t("No operational problems detected.")}</p></div></div> : null}
        {!loaded ? <div className="flex p-3 rounded-lg border gap-2"><p className="text-sm text-muted-foreground">{t("Loading...")}</p></div> : null}
      </CardContent>
    </Card>
  );
}

function RecentChangesCard({ entries, loaded, names, onNavigate, t, formatDateTime }: {
  entries: AuditEntry[];
  loaded: boolean;
  names: ReturnType<typeof buildNames>;
  onNavigate: (target: OverviewTarget) => void;
  t: Translate;
  formatDateTime: (value: string | Date) => string;
}) {
  return (
    <Card className="min-w-0 cursor-pointer">
      <CardHeader className="flex flex-row items-center justify-between space-y-0 gap-4 pb-4">
        <div className="min-w-0">
          <CardTitle>{t("Recent system changes")}</CardTitle>
          <CardDescription>{t("Recent control-plane changes and their outcomes.")}</CardDescription>
        </div>
        <Button variant="outline" size="sm" className="shrink-0 cursor-pointer" onClick={() => onNavigate("audit")} type="button">{t("View audit")}<ArrowRight /></Button>
      </CardHeader>
      <CardContent className="space-y-3">
        {entries.map((entry) => (
          <div className="flex p-3 rounded-lg border gap-2" key={entry.id}>
            <div className="flex items-center justify-center w-8 h-8 rounded-full bg-primary/10 text-primary font-semibold text-sm"><Server className="h-4 w-4" /></div>
            <div className="flex min-w-0 flex-1 items-center flex-wrap justify-between gap-1">
              <div className="flex min-w-0 items-center space-x-3"><div className="min-w-0 flex-1"><p className="text-sm font-medium truncate">{auditActionLabel(entry.action, t)}</p><p className="text-xs text-muted-foreground truncate">{auditTarget(entry, names, t)}</p></div></div>
              <div className="flex items-center space-x-3"><Badge variant={entry.outcome === "success" ? "outline" : "destructive"} className={entry.outcome === "success" ? "border-success/20 bg-success-soft text-success-strong" : undefined}>{t(entry.outcome === "success" ? "Success" : "Failure")}</Badge><div className="text-right"><p className="text-xs text-muted-foreground">{formatDateTime(entry.occurred_at)}</p></div></div>
            </div>
          </div>
        ))}
        {loaded && entries.length === 0 ? <div className="flex p-3 rounded-lg border gap-2"><p className="text-sm text-muted-foreground">{t("No recent system changes.")}</p></div> : null}
        {!loaded ? <div className="flex p-3 rounded-lg border gap-2"><p className="text-sm text-muted-foreground">{t("Loading...")}</p></div> : null}
      </CardContent>
    </Card>
  );
}

function AgentState({ node, now, t }: { node: Node; now: Date; t: Translate }) {
  return <span className="grid gap-1"><StateBadge healthy={node.agent_status === "online"}>{t(titleCase(node.agent_status))}</StateBadge>{node.last_seen_at ? <small className="text-xs text-muted-foreground">{relativeTime(node.last_seen_at, now, t)}</small> : null}</span>;
}

function RuntimeState({ instances, t }: { instances: NodePluginInstance[]; t: Translate }) {
  if (instances.length === 0) return <Badge variant="outline" className="text-muted-foreground px-1.5">{t("Not configured")}</Badge>;
  const healthy = instances.filter(instanceHealthy).length;
  return <StateBadge healthy={healthy === instances.length}>{t("{healthy} / {total} healthy", { healthy, total: instances.length })}</StateBadge>;
}

function PolicyState({ node, t }: { node: Node; t: Translate }) {
  if (node.policy === null) return <Badge variant="outline" className="text-muted-foreground px-1.5">{t("Not configured")}</Badge>;
  return <StateBadge healthy={node.policy.status === "applied"}>{t(titleCase(node.policy.status))}</StateBadge>;
}

function StateBadge({ healthy, children }: { healthy: boolean; children: string }) {
  return <Badge variant="outline" className={healthy ? "border-success/20 bg-success-soft px-1.5 text-success-strong" : "border-warning/20 bg-warning-soft px-1.5 text-warning-strong"}>{healthy ? <CheckCircle2 /> : <AlertTriangle />}{children}</Badge>;
}

export function buildOperationalIssues(data: OverviewData | undefined, secretsAvailable: boolean, now: Date, t: Translate, formatDateTime: (value: string | Date) => string): OverviewIssue[] {
  if (!data) return [];
  const issues: OverviewIssue[] = [];
  if (!secretsAvailable) issues.push({ key: "secrets", title: t("Secret storage requires recovery"), detail: t("Encrypted plugin credentials and protected configuration are unavailable."), target: "audit", tone: "danger" });
  if (data.nodes.length === 0) issues.push({ key: "nodes:none", title: t("No nodes have been created"), detail: t("Create and register a node before configuring runtime services."), target: "nodes", tone: "warning" });
  for (const node of data.nodes.filter((value) => value.enabled)) {
    if (node.registered_at === null) {
      issues.push({ key: `node:${node.id}:unregistered`, title: t("{node} is not registered", { node: node.name }), detail: t("The Agent has not completed registration."), target: "nodes", tone: "warning" });
    } else if (node.agent_status !== "online") {
      issues.push({ key: `node:${node.id}:offline`, title: t("{node} is offline", { node: node.name }), detail: node.last_seen_at ? t("Last heartbeat {time}", { time: formatDateTime(node.last_seen_at) }) : t("No Agent heartbeat has been recorded."), target: "nodes", tone: "danger" });
    }
    if (node.policy?.status === "failed" || node.policy?.status === "unsupported") {
      issues.push({ key: `node:${node.id}:policy`, title: t("{node} policy is not applied", { node: node.name }), detail: node.policy.last_problem?.message ?? t("The desired authorization policy is not active on this node."), target: "nodes", tone: "danger" });
    } else if (node.policy?.status === "pending" && olderThan(node.policy.updated_at, now, 5 * 60_000)) {
      issues.push({ key: `node:${node.id}:policy-pending`, title: t("{node} policy is still pending", { node: node.name }), detail: t("The policy has not converged for more than five minutes."), target: "nodes", tone: "warning" });
    }
  }
  if (data.nodes.length > 0 && data.instances.length === 0) issues.push({ key: "instances:none", title: t("No runtime plugins are configured"), detail: t("Nodes cannot provide proxy services until a runtime plugin is configured."), target: "plugins", tone: "warning" });
  for (const instance of data.instances.filter((value) => value.desired_state !== "absent")) {
    const label = t("{plugin} on {node}", { plugin: instance.plugin_name, node: instance.node_name });
    if (instance.reconcile_status === "failed" || instance.reconcile_status === "expired" || instance.actual_state === "failed") {
      issues.push({ key: `instance:${instance.node_id}:${instance.plugin_id}:failed`, title: t("{instance} failed", { instance: label }), detail: instance.last_problem?.message || instance.reason || t("The runtime did not reach its desired state."), target: "plugins", tone: "danger" });
    } else if (instance.health === "unhealthy") {
      issues.push({ key: `instance:${instance.node_id}:${instance.plugin_id}:health`, title: t("{instance} is unhealthy", { instance: label }), detail: instance.reason || t("The runtime health check is failing."), target: "plugins", tone: "danger" });
    } else if ((instance.command_status === "pending" || instance.reconcile_status === "pending") && olderThan(instance.updated_at, now, 5 * 60_000)) {
      issues.push({ key: `instance:${instance.node_id}:${instance.plugin_id}:pending`, title: t("{instance} is still reconciling", { instance: label }), detail: t("The desired runtime state has not converged for more than five minutes."), target: "plugins", tone: "warning" });
    } else if (instance.actual_state !== instance.desired_state || instance.actual_generation !== instance.generation) {
      issues.push({ key: `instance:${instance.node_id}:${instance.plugin_id}:drift`, title: t("{instance} has configuration drift", { instance: label }), detail: t("The desired and observed runtime states do not match."), target: "plugins", tone: "warning" });
    }
  }
  return issues;
}

export function buildAuthorizationRisks(data: OverviewData | undefined, now: Date, names: ReturnType<typeof buildNames>, t: Translate): OverviewIssue[] {
  if (!data) return [];
  const risks: OverviewIssue[] = [];
  for (const authorization of data.authorizations.filter((value) => value.enabled)) {
    const user = names.users.get(authorization.user_id) ?? t("Unknown user");
    const node = names.nodes.get(authorization.node_id) ?? t("Unknown node");
    const base = { key: `authorization:${authorization.node_id}:${authorization.id}`, detail: t("{user} on {node}", { user, node }), target: "authorizations" as const };
    const used = (authorization.current_traffic?.upload_bytes ?? 0) + (authorization.current_traffic?.download_bytes ?? 0);
    const ratio = authorization.traffic_limit_bytes && authorization.traffic_limit_bytes > 0 ? used / authorization.traffic_limit_bytes : 0;
    const expiry = authorization.expires_at ? new Date(authorization.expires_at) : undefined;
    if (authorization.enforcement?.reason === "quota_exceeded" || ratio >= 1) {
      risks.push({ ...base, title: t("Traffic quota exceeded"), tone: "danger" });
    } else if (authorization.enforcement?.reason === "expired" || (expiry && expiry <= now)) {
      risks.push({ ...base, title: t("Authorization expired"), tone: "danger" });
    } else if (ratio >= 0.8) {
      risks.push({ ...base, title: t("Traffic quota is {percent}% used", { percent: Math.floor(ratio * 100) }), tone: "warning" });
    } else if (expiry && expiry.getTime() - now.getTime() <= 7 * 24 * 60 * 60_000) {
      risks.push({ ...base, title: t("Authorization expires within seven days"), tone: "warning" });
    } else if (authorization.enforcement === null) {
      risks.push({ ...base, title: t("Authorization enforcement is not reported"), tone: "warning" });
    } else if (authorization.enforcement.blocked_ip_count > 0) {
      risks.push({ ...base, title: t("{count} source IPs are blocked", { count: authorization.enforcement.blocked_ip_count }), tone: "warning" });
    }
  }
  return risks;
}

export function buildNames(data: OverviewData | undefined) {
  return {
    nodes: new Map((data?.nodes ?? []).map((node) => [node.id, node.name])),
    users: new Map((data?.users ?? []).map((user) => [user.id, user.display_name])),
    authorizations: new Map((data?.authorizations ?? []).map((authorization) => [authorization.id, authorization])),
    plugins: new Map((data?.instances ?? []).map((instance) => [instance.plugin_id, instance.plugin_name])),
  };
}

function auditTarget(entry: AuditEntry, names: ReturnType<typeof buildNames>, t: Translate): string {
  if (entry.target_type === "node") return names.nodes.get(entry.target_id) ?? shortID(entry.target_id);
  if (entry.target_type === "user") return names.users.get(entry.target_id) ?? shortID(entry.target_id);
  if (entry.target_type === "authorization") {
    const authorization = names.authorizations.get(entry.target_id);
    if (authorization) return t("{user} on {node}", { user: names.users.get(authorization.user_id) ?? t("Unknown user"), node: names.nodes.get(authorization.node_id) ?? t("Unknown node") });
  }
  if (entry.target_type === "node_plugin_instance") {
    const nodeID = typeof entry.metadata.node_id === "string" ? entry.metadata.node_id : entry.target_id.split("/")[0];
    const pluginID = typeof entry.metadata.plugin_id === "string" ? entry.metadata.plugin_id : entry.target_id.slice(nodeID.length + 1);
    return t("{plugin} on {node}", {
      plugin: names.plugins.get(pluginID) ?? pluginID,
      node: names.nodes.get(nodeID) ?? shortID(nodeID),
    });
  }
  const targetType = auditTargetTypeLabel(entry.target_type, t);
  return entry.target_id ? `${targetType} / ${shortID(entry.target_id)}` : targetType;
}

function showOnOverview(entry: AuditEntry): boolean {
  return !["administrator.login", "administrator.logout", "node.policy_reconcile.request", "node.policy_reconcile.complete", "node.plugin_reconcile.request"].includes(entry.action);
}

function uniqueRecentChanges(entries: AuditEntry[]): AuditEntry[] {
  const seen = new Set<string>();
  return entries.filter((entry) => {
    if (!showOnOverview(entry)) return false;
    const key = `${entry.action}\n${entry.target_type}\n${entry.target_id}`;
    if (seen.has(key)) return false;
    seen.add(key);
    return true;
  });
}

export function instanceHealthy(instance: NodePluginInstance): boolean {
  if (instance.desired_state === "absent") return true;
  if (instance.desired_state === "stopped") return instance.actual_state === "stopped" && instance.reconcile_status === "succeeded";
  return instance.actual_state === "running" && instance.health === "healthy" && instance.reconcile_status === "succeeded" && instance.actual_generation === instance.generation;
}

function relativeTime(value: string, now: Date, t: Translate): string {
  const seconds = Math.max(0, Math.floor((now.getTime() - new Date(value).getTime()) / 1000));
  if (seconds < 60) return t("{count} seconds ago", { count: seconds });
  const minutes = Math.floor(seconds / 60);
  if (minutes < 60) return t("{count} minutes ago", { count: minutes });
  const hours = Math.floor(minutes / 60);
  if (hours < 48) return t("{count} hours ago", { count: hours });
  return t("{count} days ago", { count: Math.floor(hours / 24) });
}

function olderThan(value: string, now: Date, milliseconds: number): boolean {
  return now.getTime() - new Date(value).getTime() > milliseconds;
}

function shortID(value: string): string {
  return value.length > 12 ? `${value.slice(0, 8)}...` : value;
}

function titleCase(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1).replaceAll("_", " ");
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
