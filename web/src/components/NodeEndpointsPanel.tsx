import { useCallback, useEffect, useMemo, useState } from "react";
import { Cloud, Globe2, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";

import {
  APIError,
  createNodeEndpoint,
  deleteNodeEndpoint,
  listNodeEndpoints,
  listNodePublicAddresses,
  listPluginServices,
  updateNodeEndpoint,
  type NodeEndpoint,
  type NodeEndpointInput,
  type NodePublicAddress,
  type NodePublicAddressFamily,
  type PluginService,
} from "../api";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { StatusBadge } from "./PageLayout";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { Combobox } from "./ui/combobox";
import { DialogFooter } from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Skeleton } from "./ui/skeleton";
import { Switch } from "./ui/switch";

type EndpointMode = "direct" | "nat" | "domain";

export function NodeEndpointsPanel({ nodeID, onManageDDNS }: { nodeID: string; onManageDDNS: () => void }) {
  const { t, formatDateTime } = useI18n();
  const [addresses, setAddresses] = useState<NodePublicAddress[]>([]);
  const [endpoints, setEndpoints] = useState<NodeEndpoint[]>([]);
  const [services, setServices] = useState<PluginService[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string>();
  const [editingEndpoint, setEditingEndpoint] = useState<NodeEndpoint | null | undefined>();
  const [deleting, setDeleting] = useState<NodeEndpoint>();

  const load = useCallback(async (initial: boolean) => {
    if (initial) setLoading(true);
    else setRefreshing(true);
    try {
      const [nextAddresses, nextEndpoints, nextServices] = await Promise.all([
        listNodePublicAddresses(nodeID), listNodeEndpoints(nodeID), listPluginServices(nodeID),
      ]);
      setAddresses(nextAddresses);
      setEndpoints(nextEndpoints);
      setServices(nextServices);
      setError(undefined);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, [nodeID]);

  useEffect(() => { void load(true); }, [load]);

  if (loading) return <EndpointsLoading />;

  return (
    <div className="grid min-w-0 gap-4">
      <FormError message={error ? t(error) : undefined} />
      <Card className="min-w-0">
          <CardHeader>
            <CardTitle>{t("Observed public addresses")}</CardTitle>
            <CardDescription>{t("Public addresses reported by the Agent for direct endpoints and centrally managed DDNS records.")}</CardDescription>
          </CardHeader>
          <CardContent>
            <div className="min-w-0 divide-y rounded-lg border">
              {(["ipv4", "ipv6"] as NodePublicAddressFamily[]).map((family) => {
                const value = addresses.find((address) => address.family === family);
                return (
                  <div className="grid min-w-0 gap-1 px-4 py-3 sm:grid-cols-[5rem_minmax(0,1fr)] sm:items-center" key={family}>
                    <span className="text-sm font-medium">{family === "ipv4" ? "IPv4" : "IPv6"}</span>
                    {value ? (
                      <span className="grid min-w-0 gap-0.5">
                        <code className="truncate text-sm" title={value.address}>{value.address}</code>
                        <small className="text-xs text-muted-foreground">{t("Observed {time}", { time: formatDateTime(value.observed_at) })}</small>
                      </span>
                    ) : <span className="text-sm text-muted-foreground">{t("Not reported")}</span>}
                  </div>
                );
              })}
            </div>
          </CardContent>
      </Card>

      <Card className="min-w-0">
        <CardHeader className="max-sm:!grid-cols-1">
          <CardTitle>{t("Subscription endpoints")}</CardTitle>
          <CardDescription>{t("Public routes expanded into subscription entries for this node.")}</CardDescription>
          <CardAction className="flex gap-2 max-sm:col-span-1 max-sm:col-start-1 max-sm:row-span-1 max-sm:row-start-3 max-sm:justify-self-start">
            <Button size="sm" variant="outline" disabled={refreshing} type="button" aria-label={t("Refresh")} onClick={() => { void load(false); }}>
              <RefreshCw className={refreshing ? "animate-spin" : undefined} />{t("Refresh")}
            </Button>
            <Button size="sm" type="button" onClick={() => setEditingEndpoint(null)}><Plus />{t("Add endpoint")}</Button>
          </CardAction>
        </CardHeader>
        <CardContent>
          {endpoints.length === 0 ? (
            <EmptyState icon={<Globe2 />} title={t("No subscription endpoints")} />
          ) : (
            <div className="min-w-0 divide-y rounded-lg border">
              {endpoints.map((endpoint) => (
                <article className="grid min-w-0 gap-4 px-4 py-4 lg:grid-cols-[minmax(0,1fr)_minmax(14rem,0.8fr)_auto] lg:items-center" key={endpoint.id}>
                  <div className="min-w-0">
                    <div className="flex flex-wrap items-center gap-2">
                      <strong className="truncate text-sm">{endpoint.display_name}</strong>
                      <StatusBadge tone={endpoint.available ? "success" : "muted"}>{t(endpoint.available ? "Available" : "Unavailable")}</StatusBadge>
                      {!endpoint.enabled ? <Badge variant="outline">{t("Disabled")}</Badge> : null}
                    </div>
                    <p className="mt-1 truncate text-sm text-muted-foreground" title={endpoint.resolved_address || endpoint.address || endpoint.record_name}>
                      {endpoint.resolved_address || endpoint.address || endpoint.record_name || t("Waiting for an address")}
                    </p>
                  </div>
                  <div className="grid gap-1 text-sm text-muted-foreground">
                    <span>{t(endpointKindLabel(endpoint))}</span>
                    {endpoint.kind === "managed_ddns" ? (
                      <span className={endpoint.sync_status === "failed" ? "text-destructive" : undefined}>
                        {t("DDNS: {status}", { status: t(syncStatusLabel(endpoint.sync_status)) })}
                      </span>
                    ) : null}
                    <span>{t("{count} port overrides", { count: portOverrideCount(endpoint.public_port_overrides) })}</span>
                  </div>
                  <div className="flex gap-2">
                    {endpoint.kind === "managed_ddns" ? (
                      <Button size="sm" variant="outline" type="button" onClick={onManageDDNS}><Cloud />{t("Manage DDNS")}</Button>
                    ) : <>
                      <Button size="sm" variant="outline" type="button" onClick={() => setEditingEndpoint(endpoint)}><Pencil />{t("Edit")}</Button>
                      <Button size="sm" variant="outline" type="button" onClick={() => setDeleting(endpoint)}><Trash2 />{t("Delete")}</Button>
                    </>}
                  </div>
                  {endpoint.sync_error ? <p className="text-sm text-destructive lg:col-span-3">{endpoint.sync_error}</p> : null}
                </article>
              ))}
            </div>
          )}
        </CardContent>
      </Card>

      {editingEndpoint !== undefined ? (
        <EndpointDialog
          nodeID={nodeID} value={editingEndpoint ?? undefined} addresses={addresses} services={services}
          onClose={() => setEditingEndpoint(undefined)}
          onSaved={(value) => {
            setEndpoints((current) => [...current.filter((candidate) => candidate.id !== value.id), value]
              .sort((first, second) => first.display_name.localeCompare(second.display_name)));
            setEditingEndpoint(undefined);
          }}
        />
      ) : null}
      {deleting ? <DeleteDialog nodeID={nodeID} value={deleting} onClose={() => setDeleting(undefined)} onDeleted={() => {
        setEndpoints((current) => current.filter((value) => value.id !== deleting.id));
        setDeleting(undefined);
      }} /> : null}
    </div>
  );
}

function EndpointDialog({ nodeID, value, addresses, services, onClose, onSaved }: {
  nodeID: string;
  value?: NodeEndpoint;
  addresses: NodePublicAddress[];
  services: PluginService[];
  onClose: () => void;
  onSaved: (value: NodeEndpoint) => void;
}) {
  const { t } = useI18n();
  const initialMode = endpointMode(value);
  const [mode, setMode] = useState<EndpointMode>(initialMode);
  const [displayName, setDisplayName] = useState(value?.display_name ?? "");
  const [enabled, setEnabled] = useState(value?.enabled ?? true);
  const [family, setFamily] = useState<NodePublicAddressFamily>(value?.source_family || "ipv4");
  const [address, setAddress] = useState(value?.address ?? "");
  const [ports, setPorts] = useState<Record<string, string>>(() => Object.fromEntries(
    Object.entries(value?.public_port_overrides ?? {}).flatMap(([pluginID, pluginPorts]) => (
      Object.entries(pluginPorts).map(([serviceID, port]) => [servicePortKey(pluginID, serviceID), String(port)])
    )),
  ));
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const publishedServices = useMemo(() => [...services].sort((first, second) => (
    first.plugin_id.localeCompare(second.plugin_id) || first.display_name.localeCompare(second.display_name)
  )), [services]);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    const publicPortOverrides: Record<string, Record<string, number>> = {};
    for (const service of publishedServices) {
      const raw = ports[servicePortKey(service.plugin_id, service.service_id)] ?? "";
      if (raw.trim() === "") continue;
      const port = Number(raw);
      if (!Number.isInteger(port) || port < 1 || port > 65535) {
        setError(t("Public ports must be whole numbers from 1 to 65535."));
        return;
      }
      publicPortOverrides[service.plugin_id] ??= {};
      publicPortOverrides[service.plugin_id][service.service_id] = port;
    }
    const input = endpointInput({ displayName, enabled, mode, family, address, publicPortOverrides });
    setBusy(true);
    setError(undefined);
    try {
      onSaved(value ? await updateNodeEndpoint(nodeID, value.id, input) : await createNodeEndpoint(nodeID, input));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  const familyOptions = (["ipv4", "ipv6"] as NodePublicAddressFamily[]).map((candidate) => {
    const observed = addresses.find((item) => item.family === candidate)?.address;
    return { value: candidate, label: `${candidate === "ipv4" ? "IPv4" : "IPv6"}${observed ? ` · ${observed}` : ` · ${t("Not reported")}`}` };
  });
  return (
    <Modal title={t(value ? "Edit subscription endpoint" : "Add subscription endpoint")} width="wide" onClose={onClose} dismissible={!busy}>
      <form className="grid min-w-0 gap-5" onSubmit={(event) => { void submit(event); }}>
        <FormError message={error ? t(error) : undefined} />
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label={t("Endpoint name")} htmlFor="endpoint-name"><Input id="endpoint-name" required maxLength={100} value={displayName} onChange={(event) => setDisplayName(event.target.value)} /></Field>
          <Field label={t("Endpoint type")} htmlFor="endpoint-type"><Combobox id="endpoint-type" value={mode} onValueChange={(next) => setMode(next as EndpointMode)} options={[
            { value: "direct", label: t("Direct public address") }, { value: "nat", label: t("NAT or fixed public address") },
            { value: "domain", label: t("Domain") },
          ]} searchPlaceholder={t("Search endpoint types...")} emptyText={t("No endpoint types found.")} /></Field>
        </div>
        <SwitchField label={t("Enabled")} checked={enabled} onCheckedChange={setEnabled} />
        {mode === "direct" ? (
          <Field label={t("Address family")} htmlFor="endpoint-family"><Combobox id="endpoint-family" value={family} onValueChange={(next) => setFamily(next as NodePublicAddressFamily)} options={familyOptions} searchPlaceholder={t("Search address families...")} emptyText={t("No address families found.")} /></Field>
        ) : null}
        {mode === "nat" || mode === "domain" ? (
          <Field label={t(mode === "nat" ? "Public IP or domain" : "Domain")} htmlFor="endpoint-address"><Input id="endpoint-address" required maxLength={253} placeholder="edge.example.com" value={address} onChange={(event) => setAddress(event.target.value)} /></Field>
        ) : null}
        <div className="grid gap-3 border-t pt-5">
          <div><h3 className="text-sm font-medium">{t("Public port overrides")}</h3><p className="mt-1 text-sm text-muted-foreground">{t("Leave empty to publish the inbound listening port for this endpoint.")}</p></div>
          {publishedServices.length === 0 ? <p className="text-sm text-muted-foreground">{t("No plugin services are published on this node.")}</p> : (
            <div className="grid gap-3 sm:grid-cols-2">
              {publishedServices.map((service) => {
                const key = servicePortKey(service.plugin_id, service.service_id);
                return <Field key={key} label={`${service.display_name} · ${service.plugin_id} / ${service.service_id}`} htmlFor={`endpoint-port-${key}`}>
                  <Input id={`endpoint-port-${key}`} type="number" min={1} max={65535} placeholder={t("Listening port")} value={ports[key] ?? ""} onChange={(event) => setPorts((current) => ({ ...current, [key]: event.target.value }))} />
                </Field>;
              })}
            </div>
          )}
        </div>
        <DialogFooter><Button variant="outline" disabled={busy} type="button" onClick={onClose}>{t("Cancel")}</Button><Button disabled={busy} type="submit">{t(busy ? "Saving..." : "Save")}</Button></DialogFooter>
      </form>
    </Modal>
  );
}

function DeleteDialog({ nodeID, value, onClose, onDeleted }: { nodeID: string; value: NodeEndpoint; onClose: () => void; onDeleted: () => void }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  async function remove() {
    setBusy(true);
    setError(undefined);
    try {
      await deleteNodeEndpoint(nodeID, value.id);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }
  return (
    <Modal title={t("Delete subscription endpoint")} onClose={onClose} dismissible={!busy}>
      <div className="grid gap-5">
        <FormError message={error ? t(error) : undefined} />
        <p className="text-sm text-muted-foreground">{t("Delete {name}? This action cannot be undone.", { name: value.display_name })}</p>
        <DialogFooter><Button variant="outline" disabled={busy} type="button" onClick={onClose}>{t("Cancel")}</Button><Button variant="destructive" disabled={busy} type="button" onClick={() => { void remove(); }}>{t(busy ? "Deleting..." : "Delete")}</Button></DialogFooter>
      </div>
    </Modal>
  );
}

function Field({ label, htmlFor, children }: { label: string; htmlFor: string; children: React.ReactNode }) {
  return <div className="grid min-w-0 gap-2"><Label htmlFor={htmlFor}>{label}</Label>{children}</div>;
}

function SwitchField({ label, checked, onCheckedChange }: { label: string; checked: boolean; onCheckedChange: (checked: boolean) => void }) {
  return <label className="flex min-h-9 cursor-pointer items-center justify-between gap-4 rounded-lg border px-3 py-2 text-sm font-medium"><span>{label}</span><Switch checked={checked} onCheckedChange={onCheckedChange} /></label>;
}

function EmptyState({ icon, title }: { icon: React.ReactNode; title: string }) {
  return <div className="grid min-h-28 place-content-center justify-items-center gap-2 rounded-lg border border-dashed px-4 text-center text-sm text-muted-foreground"><span className="[&>svg]:size-5">{icon}</span><span>{title}</span></div>;
}

function EndpointsLoading() {
  return <div className="grid gap-4"><Skeleton className="h-64" /><Skeleton className="h-80" /></div>;
}

function endpointMode(value?: NodeEndpoint): EndpointMode {
  if (!value) return "direct";
  return value.kind === "managed_ddns" ? "direct" : value.kind;
}

function endpointInput(value: {
  displayName: string; enabled: boolean; mode: EndpointMode; family: NodePublicAddressFamily; address: string;
  publicPortOverrides: Record<string, Record<string, number>>;
}): NodeEndpointInput {
  return {
    display_name: value.displayName,
    kind: value.mode,
    enabled: value.enabled,
    source_family: value.mode === "direct" ? value.family : "",
    address: value.mode === "nat" || value.mode === "domain" ? value.address : "",
    public_port_overrides: value.publicPortOverrides,
  };
}

function servicePortKey(pluginID: string, serviceID: string): string {
  return `${pluginID}/${serviceID}`;
}

function portOverrideCount(value: Record<string, Record<string, number>>): number {
  return Object.values(value).reduce((count, services) => count + Object.keys(services).length, 0);
}

function endpointKindLabel(value: NodeEndpoint): string {
  if (value.kind === "direct") return value.source_family === "ipv6" ? "Direct IPv6" : "Direct IPv4";
  if (value.kind === "nat") return "NAT or fixed address";
  if (value.kind === "domain") return "Domain";
  return "Relayward-managed DDNS";
}

function syncStatusLabel(value: NodeEndpoint["sync_status"]): string {
  switch (value) {
    case "pending": return "Pending";
    case "synced": return "Synced";
    case "failed": return "Failed";
    default: return "Not applicable";
  }
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError && cause.violations.length > 0) {
    return `${cause.message} ${cause.violations.map((violation) => `${violation.field}: ${violation.description}`).join("; ")}`;
  }
  return cause instanceof Error && cause.message ? cause.message : "The request could not be completed.";
}
