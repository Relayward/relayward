import { type FormEvent, type ReactNode, useEffect, useId, useMemo, useState } from "react";
import { BadgeCheck, Database, KeyRound, ListChecks, Network, Pencil, Plus, ShieldAlert, Trash2 } from "lucide-react";

import {
  APIError,
  createAuthorization,
  createServiceBinding,
  deleteAuthorization,
  getSystemSettings,
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
  type SystemSettings,
} from "../api";
import { useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { PageHeader, StatusBadge, SummaryBar, SummaryItem } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { Checkbox } from "./ui/checkbox";
import { DialogFooter } from "./ui/dialog";
import { Input } from "./ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const gibibyte = 1024 ** 3;
type Translate = ReturnType<typeof useI18n>["t"];

export function AuthorizationsView() {
  const { t, formatDate } = useI18n();
  const [items, setItems] = useState<Authorization[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [settings, setSettings] = useState<SystemSettings>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<Authorization | "new">();
  const [deleting, setDeleting] = useState<Authorization>();
  const [rotating, setRotating] = useState<Authorization>();
  const [servicesFor, setServicesFor] = useState<Authorization>();
  const [shownToken, setShownToken] = useState<{ title: string; value: string }>();
  const [search, setSearch] = useState("");

  useEffect(() => {
    let active = true;
    Promise.all([listAuthorizations(), listNodes(), listUsers(), getSystemSettings()]).then(([authorizations, nodeItems, userItems, systemSettings]) => {
      if (!active) return;
      setItems(authorizations);
      setNodes(nodeItems);
      setUsers(userItems);
      setSettings(systemSettings);
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
  const visibleItems = items.filter((authorization) => {
    const query = search.trim().toLocaleLowerCase();
    return [userNames.get(authorization.user_id), nodeNames.get(authorization.node_id)]
      .some((value) => value?.toLocaleLowerCase().includes(query));
  });
  const summary = useMemo(() => {
    const enabled = items.filter((item) => item.enabled).length;
    const quota = items.reduce((total, item) => total + (item.traffic_limit_bytes ?? 0), 0);
    const unlimited = items.filter((item) => item.traffic_limit_bytes === null).length;
    const ipLimited = items.filter((item) => item.soft_ip_limit !== null).length;
    const needsAttention = items.filter((item) => item.enabled && (
      item.enforcement === null || item.enforcement.reason !== "active" || item.enforcement.blocked_ip_count > 0
    )).length;
    return { enabled, quota, unlimited, ipLimited, needsAttention };
  }, [items]);

  return (
    <section aria-labelledby="authorizations-title">
      <PageHeader
        id="authorizations-title"
        eyebrow={t("Access")}
        title={t("Authorizations")}
        description={t("Manage node access, traffic quotas, expiry, and IP enforcement.")}
      />
      <SummaryBar>
        <SummaryItem icon={<BadgeCheck size={17} />} label={t("Enabled authorizations")} value={`${summary.enabled} / ${items.length}`} tone="success" />
        <SummaryItem icon={<Database size={17} />} label={t("Configured quota")} value={formatBytes(summary.quota)} note={summary.unlimited > 0 ? t("{count} unlimited", { count: summary.unlimited }) : t("All authorizations are limited")} tone="primary" />
        <SummaryItem icon={<Network size={17} />} label={t("IP limits")} value={summary.ipLimited} note={t("Configured authorizations")} />
        <SummaryItem icon={<ShieldAlert size={17} />} label={t("Needs attention")} value={summary.needsAttention} note={t("Enforcement or blocked IPs")} tone={summary.needsAttention > 0 ? "warning" : "success"} />
      </SummaryBar>
      <Card className="min-w-0 h-fit">
        <CardHeader className="flex flex-col items-start justify-between space-y-0 gap-4 pb-4 sm:flex-row sm:items-center">
          <div className="min-w-0">
            <CardTitle>{t("Authorization list")}</CardTitle>
            <CardDescription>{t("{count} authorizations", { count: visibleItems.length })}</CardDescription>
          </div>
          <div className="flex w-full flex-col gap-3 sm:w-auto sm:flex-row sm:items-center">
            <Input className="max-w-sm" value={search} onChange={(event) => setSearch(event.target.value)} placeholder={t("Search users or nodes...")} />
            <Button
              className="shrink-0"
              disabled={!canAdd}
              onClick={() => setEditing("new")}
              title={canAdd ? t("Add authorization") : t("A node and user are required")}
              type="button"
            ><Plus />{t("Add authorization")}</Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {error ? <FormError message={t(error)} /> : null}
          <div className="rounded-lg border bg-card">
            <Table className="min-w-[1000px]">
              <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("User")}</TableHead><TableHead>{t("Node")}</TableHead><TableHead>{t("Traffic")}</TableHead><TableHead>{t("Reset")}</TableHead><TableHead>{t("Expiry")}</TableHead><TableHead>{t("Enforcement")}</TableHead><TableHead>{t("IP slots")}</TableHead><TableHead className="text-right">{t("Actions")}</TableHead></TableRow></TableHeader>
              <TableBody>
                {loading ? <TableRow><TableCell colSpan={8} className="h-24 text-center text-muted-foreground">{t("Loading...")}</TableCell></TableRow> : null}
                {!loading && items.length === 0 ? <TableRow><TableCell colSpan={8} className="h-24 text-center text-muted-foreground">{t("No authorizations have been created.")}</TableCell></TableRow> : null}
                {visibleItems.map((authorization) => (
                  <TableRow key={authorization.id}>
                    <TableCell><strong className="font-semibold">{userNames.get(authorization.user_id) ?? t("Unknown user")}</strong></TableCell>
                    <TableCell className="text-muted-foreground">{nodeNames.get(authorization.node_id) ?? t("Unknown node")}</TableCell>
                    <TableCell><TrafficUsage value={authorization} /></TableCell>
                    <TableCell className="text-muted-foreground">{formatReset(authorization, t)}</TableCell>
                    <TableCell className="text-muted-foreground">{authorization.expires_at ? formatDate(authorization.expires_at) : t("Never")}</TableCell>
                    <TableCell><AuthorizationStatus value={authorization} /></TableCell>
                    <TableCell><IPStatus value={authorization} /></TableCell>
                    <TableCell className="w-px text-right whitespace-nowrap">
                      <IconAction label={t("Manage services")} onClick={() => setServicesFor(authorization)}><ListChecks size={17} /></IconAction>
                      <IconAction label={t("Rotate subscription token")} onClick={() => setRotating(authorization)}><KeyRound size={17} /></IconAction>
                      <IconAction label={t("Edit authorization")} onClick={() => setEditing(authorization)}><Pencil size={17} /></IconAction>
                      <IconAction label={t("Delete authorization")} danger onClick={() => setDeleting(authorization)}><Trash2 size={17} /></IconAction>
                    </TableCell>
                  </TableRow>
                ))}
              </TableBody>
            </Table>
          </div>
        </CardContent>
      </Card>
      {editing ? (
        <AuthorizationDialog
          value={editing === "new" ? undefined : editing}
          nodes={nodes}
          users={users}
          defaultTimezone={settings?.timezone ?? "UTC"}
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
      {shownToken ? <TokenDialog title={shownToken.title} token={shownToken.value} publicURL={settings?.public_url ?? ""} onClose={() => setShownToken(undefined)} /> : null}
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
      <div className="grid max-h-[360px] overflow-y-auto">
        {loading ? <p className="my-5 text-center text-sm text-muted-foreground">{t("Loading...")}</p> : null}
        {!loading && services.length === 0 ? <p className="my-5 text-center text-sm text-muted-foreground">{t("No services are available on this node.")}</p> : null}
        {services.map((service) => {
          const key = bindingKey(service);
          return (
            <ServiceOption key={key} service={service} checked={selected.has(key)} onCheckedChange={() => toggle(key)} />
          );
        })}
      </div>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <DialogFooter>
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button disabled={busy || loading} onClick={save} type="button">{busy ? t("Saving...") : t("Save")}</Button>
      </DialogFooter>
    </Modal>
  );
}

function ServiceOption({ service, checked, onCheckedChange }: { service: PluginService; checked: boolean; onCheckedChange: () => void }) {
  const id = useId();
  return (
    <label htmlFor={id} className="flex min-h-[58px] cursor-pointer items-center gap-3 border-b border-border py-2.5 last:border-b-0">
      <Checkbox id={id} checked={checked} onCheckedChange={onCheckedChange} />
      <span className="grid min-w-0 gap-0.5"><strong className="text-sm font-semibold">{service.display_name}</strong><small className="[overflow-wrap:anywhere] text-xs text-muted-foreground">{service.plugin_id} / {service.service_id}</small></span>
    </label>
  );
}

function bindingKey(value: { plugin_id: string; service_id: string }): string {
  return `${value.plugin_id}\u0000${value.service_id}`;
}

function AuthorizationDialog({ value, nodes, users, defaultTimezone, onClose, onSaved }: {
  value?: Authorization;
  nodes: Node[];
  users: User[];
  defaultTimezone: string;
  onClose: () => void;
  onSaved: (authorization: Authorization, token?: string) => void;
}) {
  const { t } = useI18n();
  const enabledID = useId();
  const [userID, setUserID] = useState(value?.user_id ?? users[0]?.id ?? "");
  const [nodeID, setNodeID] = useState(value?.node_id ?? nodes[0]?.id ?? "");
  const [enabled, setEnabled] = useState(value?.enabled ?? true);
  const [quotaGiB, setQuotaGiB] = useState(value?.traffic_limit_bytes === null || value === undefined ? "" : String(value.traffic_limit_bytes / gibibyte));
  const [resetKind, setResetKind] = useState<ResetKind>(value?.reset.kind ?? "never");
  const [resetValue, setResetValue] = useState(value?.reset.value === null || value === undefined ? "" : String(value.reset.value));
  const [timezone, setTimezone] = useState(value?.reset.timezone ?? defaultTimezone);
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
      <form className="grid gap-5" onSubmit={submit}>
        <div className="grid grid-cols-2 gap-x-4 gap-y-4 max-[700px]:grid-cols-1">
          <SelectField label={t("User")} value={userID} onChange={setUserID} disabled={value !== undefined} options={users.map((user) => ({ value: user.id, label: user.display_name }))} />
          <SelectField label={t("Node")} value={nodeID} onChange={setNodeID} disabled={value !== undefined} options={nodes.map((node) => ({ value: node.id, label: node.name }))} />
          <NumberField label={t("{field} (optional)", { field: t("Traffic quota (GiB)") })} value={quotaGiB} onChange={setQuotaGiB} min="0" step="0.01" required={false} />
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
          <FieldLabel label={t("Timezone")}><Input value={timezone} onChange={(event) => setTimezone(event.target.value)} list="relayward-timezones" required /></FieldLabel>
          {resetKind === "interval_days" ? <DateTimeField label={t("Period anchor")} value={periodAnchor} onChange={setPeriodAnchor} required /> : null}
          <DateTimeField label={t("{field} (optional)", { field: t("Expires") })} value={expiresAt} onChange={setExpiresAt} />
          <NumberField label={t("{field} (optional)", { field: t("Soft IP limit") })} value={softIPLimit} onChange={setSoftIPLimit} min="1" max="1024" step="1" required={false} />
          <NumberField label={t("Activity window (minutes)")} value={activityMinutes} onChange={setActivityMinutes} min="1" max="1440" step="1" />
          <NumberField label={t("Block duration (minutes)")} value={blockMinutes} onChange={setBlockMinutes} min="1" max="10080" step="1" />
        </div>
        <datalist id="relayward-timezones"><option value="UTC" /><option value="Asia/Shanghai" /><option value="Asia/Singapore" /><option value="Europe/London" /><option value="America/New_York" /></datalist>
        <label className="flex min-h-8 cursor-pointer items-center gap-2 text-sm font-semibold text-foreground/80" htmlFor={enabledID}>
          <Checkbox id={enabledID} checked={enabled} onCheckedChange={(checked) => setEnabled(checked === true)} />
          <span>{t("Enabled")}</span>
        </label>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
          <Button disabled={busy} type="submit">{busy ? t("Saving...") : value ? t("Save") : t("Add authorization")}</Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function SelectField({ label, value, onChange, options, disabled = false }: {
  label: string; value: string; onChange: (value: string) => void; options: { value: string; label: string }[]; disabled?: boolean;
}) {
  const id = useId();
  const labelID = `${id}-label`;
  return (
    <label className="grid gap-1.5" htmlFor={id}>
      <span className="text-sm font-semibold text-foreground/80" id={labelID}>{label}</span>
      <Select value={value} onValueChange={onChange} disabled={disabled} required>
        <SelectTrigger id={id} aria-labelledby={labelID}><SelectValue /></SelectTrigger>
        <SelectContent>{options.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
      </Select>
    </label>
  );
}

function NumberField({ label, value, onChange, min, max, step, required = true }: {
  label: string; value: string; onChange: (value: string) => void; min: string; max?: string; step: string; required?: boolean;
}) {
  return <FieldLabel label={label}><Input type="number" value={value} onChange={(event) => onChange(event.target.value)} min={min} max={max} step={step} required={required} /></FieldLabel>;
}

function DateTimeField({ label, value, onChange, required = false }: { label: string; value: string; onChange: (value: string) => void; required?: boolean }) {
  return <FieldLabel label={label}><Input type="datetime-local" value={value} onChange={(event) => onChange(event.target.value)} required={required} /></FieldLabel>;
}

function FieldLabel({ label, children }: { label: string; children: ReactNode }) {
  return <label className="grid gap-1.5"><span className="text-sm font-semibold text-foreground/80">{label}</span>{children}</label>;
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
      <p className={cn("m-0 [overflow-wrap:anywhere] border-l-[3px] p-3", danger ? "border-destructive bg-destructive/10" : "border-primary bg-primary/10")}>{subject}</p>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <DialogFooter>
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button variant={danger ? "destructive" : "default"} disabled={busy} onClick={confirm} type="button">{busy ? t("Saving...") : action}</Button>
      </DialogFooter>
    </Modal>
  );
}

function TokenDialog({ title, token, publicURL, onClose }: { title: string; token: string; publicURL: string; onClose: () => void }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);
  const link = buildSubscriptionLink(token, publicURL, window.location.origin);
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
      <code className="block [overflow-wrap:anywhere] rounded-sm border border-border bg-muted p-3 text-sm">{link}</code>
      <DialogFooter>
        <Button variant="secondary" onClick={copy} type="button">{copied ? t("Copied") : t("Copy")}</Button>
        <Button onClick={onClose} type="button">{t("Done")}</Button>
      </DialogFooter>
    </Modal>
  );
}

export function buildSubscriptionLink(token: string, publicURL: string, currentOrigin: string): string {
  return new URL(`/s/${encodeURIComponent(token)}`, publicURL || currentOrigin).toString();
}

function AuthorizationStatus({ value }: { value: Authorization }) {
  const { t, formatDateTime } = useI18n();
  const status = value.enforcement;
  if (!status) return <StatusBadge tone="warning">{t("Not reported")}</StatusBadge>;
  const labels = {
    active: "Active",
    administrator_disabled: "Disabled",
    expired: "Expired",
    quota_exceeded: "Quota reached",
  } as const;
  const tone = status.reason === "active" && status.services_enabled ? "success"
    : status.reason === "administrator_disabled" ? "muted" : "warning";
  return (
    <span className="grid gap-0.5 whitespace-nowrap" title={t("Observed {time}", { time: formatDateTime(status.observed_at) })}>
      <StatusBadge tone={tone}>{t(labels[status.reason])}</StatusBadge>
      <small className="text-xs text-muted-foreground">{t("Generation {generation}", { generation: status.generation })}</small>
    </span>
  );
}

function TrafficUsage({ value }: { value: Authorization }) {
  const { t, formatDateTime } = useI18n();
  const traffic = value.current_traffic;
  if (!traffic) return <span className="text-muted-foreground">{t("No data / {quota}", { quota: formatQuota(value.traffic_limit_bytes, t) })}</span>;
  const total = traffic.upload_bytes + traffic.download_bytes;
  const percentage = value.traffic_limit_bytes === null || value.traffic_limit_bytes <= 0
    ? 0
    : Math.min(total / value.traffic_limit_bytes * 100, 100);
  const periodEnd = traffic.period.ends_at ? formatDateTime(traffic.period.ends_at) : t("No end");
  return (
    <span className="grid gap-0.5 whitespace-nowrap" title={t("{start} - {end}; observed {observed}", { start: formatDateTime(traffic.period.starts_at), end: periodEnd, observed: formatDateTime(traffic.observed_at) })}>
      <strong className="font-semibold">{formatBytes(total)}</strong>
      <small className="text-xs text-muted-foreground">{t("of {quota}", { quota: formatQuota(value.traffic_limit_bytes, t) })}</small>
      {value.traffic_limit_bytes !== null ? <span className="mt-1 h-1.5 w-28 overflow-hidden rounded-full bg-muted"><span className={cn("block h-full rounded-full", percentage >= 100 ? "bg-destructive" : percentage >= 80 ? "bg-warning" : "bg-primary")} style={{ width: `${percentage}%` }} /></span> : null}
    </span>
  );
}

function IPStatus({ value }: { value: Authorization }) {
  const { t } = useI18n();
  if (value.soft_ip_limit === null) return <StatusBadge tone="muted">{t("Not limited")}</StatusBadge>;
  if (!value.enforcement) return <StatusBadge tone="warning">{t("Not reported / {limit}", { limit: value.soft_ip_limit })}</StatusBadge>;
  return (
    <span className="grid gap-0.5 whitespace-nowrap">
      <StatusBadge tone={value.enforcement.blocked_ip_count > 0 ? "warning" : "info"}>{value.enforcement.active_ip_count} / {value.soft_ip_limit}</StatusBadge>
      <small className="text-xs text-muted-foreground">{t("{count} blocked", { count: value.enforcement.blocked_ip_count })}</small>
    </span>
  );
}

function IconAction({ label, danger = false, onClick, children }: { label: string; danger?: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          className={cn(
            "ml-0.5 text-muted-foreground",
            danger && "text-destructive hover:bg-destructive/10 hover:text-destructive",
          )}
          variant="ghost"
          size="icon"
          aria-label={label}
          onClick={onClick}
          type="button"
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{label}</TooltipContent>
    </Tooltip>
  );
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
