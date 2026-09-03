import { useCallback, useEffect, useMemo, useState } from "react";
import { Cloud, Globe2, Pencil, Plus, RefreshCw, Trash2 } from "lucide-react";

import {
  APIError,
  createDDNSRecord,
  createDNSProviderConnection,
  deleteDDNSRecord,
  deleteDNSProviderConnection,
  listDDNSRecords,
  listDNSProviderConnections,
  listNodePublicAddresses,
  listNodes,
  listPluginServices,
  updateDDNSRecord,
  updateDNSProviderConnection,
  type DDNSRecord,
  type DDNSRecordInput,
  type DNSProviderConnection,
  type Node,
  type NodePublicAddress,
  type NodePublicAddressFamily,
  type PluginService,
} from "../api";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { PageHeader, StatusBadge } from "./PageLayout";
import { Badge } from "./ui/badge";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { Combobox } from "./ui/combobox";
import { DialogFooter } from "./ui/dialog";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Skeleton } from "./ui/skeleton";
import { Switch } from "./ui/switch";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

type DDNSTab = "records" | "connections";
type DeleteTarget = { kind: "record"; value: DDNSRecord } | { kind: "connection"; value: DNSProviderConnection };

export function DDNSView() {
  const { t } = useI18n();
  const [tab, setTab] = useState<DDNSTab>("records");
  const [records, setRecords] = useState<DDNSRecord[]>([]);
  const [connections, setConnections] = useState<DNSProviderConnection[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [loading, setLoading] = useState(true);
  const [refreshing, setRefreshing] = useState(false);
  const [error, setError] = useState<string>();
  const [editingRecord, setEditingRecord] = useState<DDNSRecord | null | undefined>();
  const [editingConnection, setEditingConnection] = useState<DNSProviderConnection | null | undefined>();
  const [deleting, setDeleting] = useState<DeleteTarget>();

  const load = useCallback(async (initial: boolean) => {
    if (initial) setLoading(true);
    else setRefreshing(true);
    try {
      const [nextRecords, nextConnections, nextNodes] = await Promise.all([
        listDDNSRecords(), listDNSProviderConnections(), listNodes(),
      ]);
      setRecords(nextRecords);
      setConnections(nextConnections);
      setNodes(nextNodes);
      setError(undefined);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoading(false);
      setRefreshing(false);
    }
  }, []);

  useEffect(() => { void load(true); }, [load]);

  return (
    <section aria-labelledby="ddns-title">
      <PageHeader
        id="ddns-title"
        eyebrow={t("Resource management")}
        title={t("DDNS")}
        description={t("Manage DNS provider credentials and records updated from node public addresses.")}
        actions={<>
          <Button variant="outline" size="sm" disabled={refreshing} type="button" onClick={() => { void load(false); }}>
            <RefreshCw className={refreshing ? "animate-spin" : undefined} />{t("Refresh")}
          </Button>
          {tab === "records" ? (
            <Button size="sm" disabled={nodes.length === 0 || connections.length === 0} type="button" onClick={() => setEditingRecord(null)}><Plus />{t("Add DDNS record")}</Button>
          ) : <Button size="sm" type="button" onClick={() => setEditingConnection(null)}><Plus />{t("Add connection")}</Button>}
        </>}
      />
      <FormError message={error ? t(error) : undefined} />
      <Tabs value={tab} onValueChange={(value) => setTab(value as DDNSTab)}>
        <div className="mb-4 overflow-x-auto pb-1">
          <TabsList className="min-w-max" aria-label={t("DDNS")}>
            <TabsTrigger value="records">{t("Managed records")}</TabsTrigger>
            <TabsTrigger value="connections">{t("DNS provider connections")}</TabsTrigger>
          </TabsList>
        </div>

        <TabsContent value="records">
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>{t("Managed records")}</CardTitle>
              <CardDescription>{t("A or AAAA records synchronized from the public address reported by a selected node.")}</CardDescription>
            </CardHeader>
            <CardContent>
              {loading ? <Skeleton className="h-64" /> : records.length === 0 ? (
                <EmptyState icon={<Globe2 />} title={t("No managed DDNS records")} />
              ) : (
                <div className="min-w-0 divide-y rounded-lg border">
                  {records.map((record) => (
                    <article className="grid min-w-0 gap-4 px-4 py-4 lg:grid-cols-[minmax(0,1fr)_minmax(12rem,0.7fr)_auto] lg:items-center" key={record.id}>
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2">
                          <strong className="truncate text-sm">{record.display_name}</strong>
                          <StatusBadge tone={record.sync_status === "synced" ? "success" : record.sync_status === "failed" ? "danger" : "warning"}>{t(syncStatusLabel(record.sync_status))}</StatusBadge>
                          {!record.enabled ? <Badge variant="outline">{t("Disabled")}</Badge> : null}
                        </div>
                        <p className="mt-1 truncate text-sm text-muted-foreground" title={record.record_name}>{record.record_name}</p>
                      </div>
                      <div className="grid gap-1 text-sm text-muted-foreground">
                        <span>{record.node_name}</span>
                        <span>{record.source_family === "ipv6" ? "AAAA · IPv6" : "A · IPv4"}</span>
                        {record.actual_address ? <code className="truncate text-xs" title={record.actual_address}>{record.actual_address}</code> : null}
                      </div>
                      <div className="flex gap-2">
                        <Button size="sm" variant="outline" type="button" onClick={() => setEditingRecord(record)}><Pencil />{t("Edit")}</Button>
                        <Button size="sm" variant="outline" type="button" onClick={() => setDeleting({ kind: "record", value: record })}><Trash2 />{t("Delete")}</Button>
                      </div>
                      {record.sync_error ? <p className="text-sm text-destructive lg:col-span-3">{record.sync_error}</p> : null}
                    </article>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>

        <TabsContent value="connections">
          <Card className="min-w-0">
            <CardHeader>
              <CardTitle>{t("DNS provider connections")}</CardTitle>
              <CardDescription>{t("Cloudflare credentials are stored centrally and can be reused across nodes.")}</CardDescription>
              <CardAction><Badge variant="outline">Cloudflare</Badge></CardAction>
            </CardHeader>
            <CardContent>
              {loading ? <Skeleton className="h-64" /> : connections.length === 0 ? (
                <EmptyState icon={<Cloud />} title={t("No DNS provider connections")} />
              ) : (
                <div className="min-w-0 divide-y rounded-lg border">
                  {connections.map((connection) => (
                    <div className="flex min-w-0 flex-col gap-3 px-4 py-3 sm:flex-row sm:items-center sm:justify-between" key={connection.id}>
                      <div className="min-w-0">
                        <div className="flex flex-wrap items-center gap-2"><strong className="truncate text-sm">{connection.name}</strong><Badge variant="outline">Cloudflare</Badge></div>
                        <p className="mt-1 text-xs text-muted-foreground">{t(connection.has_token ? "API token configured" : "API token required")}</p>
                      </div>
                      <div className="flex gap-2">
                        <Button size="sm" variant="outline" type="button" onClick={() => setEditingConnection(connection)}><Pencil />{t("Edit")}</Button>
                        <Button size="sm" variant="outline" type="button" onClick={() => setDeleting({ kind: "connection", value: connection })}><Trash2 />{t("Delete")}</Button>
                      </div>
                    </div>
                  ))}
                </div>
              )}
            </CardContent>
          </Card>
        </TabsContent>
      </Tabs>

      {editingRecord !== undefined ? (
        <RecordDialog value={editingRecord ?? undefined} nodes={nodes} connections={connections} onClose={() => setEditingRecord(undefined)} onSaved={(value) => {
          setRecords((current) => sortRecords([...current.filter((candidate) => candidate.id !== value.id), value]));
          setEditingRecord(undefined);
        }} />
      ) : null}
      {editingConnection !== undefined ? (
        <ConnectionDialog value={editingConnection ?? undefined} onClose={() => setEditingConnection(undefined)} onSaved={(value) => {
          setConnections((current) => [...current.filter((candidate) => candidate.id !== value.id), value].sort((first, second) => first.name.localeCompare(second.name)));
          setEditingConnection(undefined);
        }} />
      ) : null}
      {deleting ? <DeleteDialog target={deleting} onClose={() => setDeleting(undefined)} onDeleted={() => {
        if (deleting.kind === "record") setRecords((current) => current.filter((value) => value.id !== deleting.value.id));
        else setConnections((current) => current.filter((value) => value.id !== deleting.value.id));
        setDeleting(undefined);
      }} /> : null}
    </section>
  );
}

function RecordDialog({ value, nodes, connections, onClose, onSaved }: {
  value?: DDNSRecord;
  nodes: Node[];
  connections: DNSProviderConnection[];
  onClose: () => void;
  onSaved: (value: DDNSRecord) => void;
}) {
  const { t } = useI18n();
  const [nodeID, setNodeID] = useState(value?.node_id ?? nodes[0]?.id ?? "");
  const [displayName, setDisplayName] = useState(value?.display_name ?? "");
  const [enabled, setEnabled] = useState(value?.enabled ?? true);
  const [family, setFamily] = useState<NodePublicAddressFamily>(value?.source_family || "ipv4");
  const [connectionID, setConnectionID] = useState(value?.dns_provider_connection_id ?? connections[0]?.id ?? "");
  const [zoneName, setZoneName] = useState(value?.zone_name ?? "");
  const [recordName, setRecordName] = useState(value?.record_name ?? "");
  const [ttl, setTTL] = useState(value?.ttl ?? 1);
  const [proxied, setProxied] = useState(value?.proxied ?? false);
  const [addresses, setAddresses] = useState<NodePublicAddress[]>([]);
  const [services, setServices] = useState<PluginService[]>([]);
  const [ports, setPorts] = useState<Record<string, string>>(() => Object.fromEntries(
    Object.entries(value?.public_port_overrides ?? {}).flatMap(([pluginID, pluginPorts]) => (
      Object.entries(pluginPorts).map(([serviceID, port]) => [servicePortKey(pluginID, serviceID), String(port)])
    )),
  ));
  const [loadingNode, setLoadingNode] = useState(false);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    if (!nodeID) {
      setAddresses([]);
      setServices([]);
      return () => { active = false; };
    }
    setLoadingNode(true);
    Promise.all([listNodePublicAddresses(nodeID), listPluginServices(nodeID)]).then(([nextAddresses, nextServices]) => {
      if (!active) return;
      setAddresses(nextAddresses);
      setServices(nextServices);
      setError(undefined);
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoadingNode(false);
    });
    return () => { active = false; };
  }, [nodeID]);

  const publishedServices = useMemo(() => [...services].sort((first, second) => (
    first.plugin_id.localeCompare(second.plugin_id) || first.display_name.localeCompare(second.display_name)
  )), [services]);

  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    if (!nodeID) return setError(t("Select a node."));
    if (!connectionID) return setError(t("Select a DNS provider connection."));
    if (ttl !== 1 && (!Number.isInteger(ttl) || ttl < 60 || ttl > 86400)) return setError(t("TTL must be automatic or from 60 to 86400 seconds."));
    const publicPortOverrides: Record<string, Record<string, number>> = {};
    for (const service of publishedServices) {
      const raw = ports[servicePortKey(service.plugin_id, service.service_id)] ?? "";
      if (raw.trim() === "") continue;
      const port = Number(raw);
      if (!Number.isInteger(port) || port < 1 || port > 65535) return setError(t("Public ports must be whole numbers from 1 to 65535."));
      publicPortOverrides[service.plugin_id] ??= {};
      publicPortOverrides[service.plugin_id][service.service_id] = port;
    }
    const input: DDNSRecordInput = {
      node_id: nodeID, display_name: displayName, enabled, source_family: family,
      public_port_overrides: publicPortOverrides, dns_provider_connection_id: connectionID || null,
      zone_name: zoneName, record_name: recordName, ttl, proxied,
    };
    setBusy(true);
    setError(undefined);
    try {
      onSaved(value ? await updateDDNSRecord(value.id, input) : await createDDNSRecord(input));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  const familyOptions = (["ipv4", "ipv6"] as NodePublicAddressFamily[]).map((candidate) => {
    const observed = addresses.find((address) => address.family === candidate)?.address;
    return { value: candidate, label: `${candidate === "ipv4" ? "A · IPv4" : "AAAA · IPv6"}${observed ? ` · ${observed}` : ` · ${t("Not reported")}`}` };
  });

  return (
    <Modal title={t(value ? "Edit DDNS record" : "Add DDNS record")} width="wide" onClose={onClose} dismissible={!busy}>
      <form className="grid min-w-0 gap-5" onSubmit={(event) => { void submit(event); }}>
        <FormError message={error ? t(error) : undefined} />
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label={t("Record label")} htmlFor="ddns-display-name"><Input id="ddns-display-name" required maxLength={100} value={displayName} onChange={(event) => setDisplayName(event.target.value)} /></Field>
          <Field label={t("Node")} htmlFor="ddns-node"><Combobox id="ddns-node" disabled={value !== undefined} required value={nodeID} onValueChange={(next) => { setNodeID(next); setPorts({}); }} options={nodes.map((node) => ({ value: node.id, label: node.name }))} placeholder={t("Select a node")} searchPlaceholder={t("Search nodes...")} emptyText={t("No nodes found.")} /></Field>
        </div>
        <SwitchField label={t("Enabled")} checked={enabled} onCheckedChange={setEnabled} />
        <div className="grid gap-5 sm:grid-cols-2">
          <Field label={t("Address family")} htmlFor="ddns-family"><Combobox id="ddns-family" disabled={loadingNode} value={family} onValueChange={(next) => setFamily(next as NodePublicAddressFamily)} options={familyOptions} searchPlaceholder={t("Search address families...")} emptyText={t("No address families found.")} /></Field>
          <Field label={t("DNS provider connection")} htmlFor="ddns-connection"><Combobox id="ddns-connection" required value={connectionID} onValueChange={setConnectionID} options={connections.map((connection) => ({ value: connection.id, label: connection.name }))} placeholder={t("Select a connection")} searchPlaceholder={t("Search connections...")} emptyText={t("No connections found.")} /></Field>
          <Field label={t("DNS zone")} htmlFor="ddns-zone"><Input id="ddns-zone" required maxLength={253} placeholder="example.com" value={zoneName} onChange={(event) => setZoneName(event.target.value)} /></Field>
          <Field label={t("Record name")} htmlFor="ddns-record-name"><Input id="ddns-record-name" required maxLength={253} placeholder="edge.example.com" value={recordName} onChange={(event) => setRecordName(event.target.value)} /></Field>
          <Field label={t("TTL in seconds")} htmlFor="ddns-ttl"><Input id="ddns-ttl" type="number" min={1} max={86400} required disabled={proxied} value={ttl} onChange={(event) => setTTL(Number(event.target.value))} /></Field>
          <SwitchField label={t("Cloudflare proxy")} checked={proxied} onCheckedChange={(checked) => { setProxied(checked); if (checked) setTTL(1); }} />
        </div>
        <div className="grid gap-3 border-t pt-5">
          <div><h3 className="text-sm font-medium">{t("Public port overrides")}</h3><p className="mt-1 text-sm text-muted-foreground">{t("Leave empty to publish the inbound listening port for this endpoint.")}</p></div>
          {loadingNode ? <Skeleton className="h-24" /> : publishedServices.length === 0 ? <p className="text-sm text-muted-foreground">{t("No plugin services are published on this node.")}</p> : (
            <div className="grid gap-3 sm:grid-cols-2">
              {publishedServices.map((service) => {
                const key = servicePortKey(service.plugin_id, service.service_id);
                return <Field key={key} label={`${service.display_name} · ${service.plugin_id} / ${service.service_id}`} htmlFor={`ddns-port-${key}`}>
                  <Input id={`ddns-port-${key}`} type="number" min={1} max={65535} placeholder={t("Listening port")} value={ports[key] ?? ""} onChange={(event) => setPorts((current) => ({ ...current, [key]: event.target.value }))} />
                </Field>;
              })}
            </div>
          )}
        </div>
        <DialogFooter><Button variant="outline" disabled={busy} type="button" onClick={onClose}>{t("Cancel")}</Button><Button disabled={busy || loadingNode} type="submit">{t(busy ? "Saving..." : "Save")}</Button></DialogFooter>
      </form>
    </Modal>
  );
}

function ConnectionDialog({ value, onClose, onSaved }: { value?: DNSProviderConnection; onClose: () => void; onSaved: (value: DNSProviderConnection) => void }) {
  const { t } = useI18n();
  const [name, setName] = useState(value?.name ?? "Cloudflare");
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  async function submit(event: React.FormEvent<HTMLFormElement>) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const input = { name, provider: "cloudflare" as const, ...(token.trim() ? { api_token: token.trim() } : {}) };
      onSaved(value ? await updateDNSProviderConnection(value.id, input) : await createDNSProviderConnection(input));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }
  return (
    <Modal title={t(value ? "Edit DNS provider connection" : "Add DNS provider connection")} onClose={onClose} dismissible={!busy}>
      <form className="grid gap-5" onSubmit={(event) => { void submit(event); }}>
        <FormError message={error ? t(error) : undefined} />
        <Field label={t("Connection name")} htmlFor="provider-name"><Input id="provider-name" required maxLength={100} value={name} onChange={(event) => setName(event.target.value)} /></Field>
        <Field label={t(value?.has_token ? "Replace API token (optional)" : "Cloudflare API token")} htmlFor="provider-token"><Input id="provider-token" type="password" required={!value?.has_token} maxLength={4096} autoComplete="off" value={token} onChange={(event) => setToken(event.target.value)} /></Field>
        <DialogFooter><Button variant="outline" disabled={busy} type="button" onClick={onClose}>{t("Cancel")}</Button><Button disabled={busy} type="submit">{t(busy ? "Saving..." : "Save")}</Button></DialogFooter>
      </form>
    </Modal>
  );
}

function DeleteDialog({ target, onClose, onDeleted }: { target: DeleteTarget; onClose: () => void; onDeleted: () => void }) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const name = target.kind === "record" ? target.value.display_name : target.value.name;
  async function remove() {
    setBusy(true);
    setError(undefined);
    try {
      if (target.kind === "record") await deleteDDNSRecord(target.value.id);
      else await deleteDNSProviderConnection(target.value.id);
      onDeleted();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }
  return (
    <Modal title={t(target.kind === "record" ? "Delete DDNS record" : "Delete DNS provider connection")} onClose={onClose} dismissible={!busy}>
      <div className="grid gap-5">
        <FormError message={error ? t(error) : undefined} />
        <p className="text-sm text-muted-foreground">{t("Delete {name}? This action cannot be undone.", { name })}</p>
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
  return <div className="grid min-h-40 place-content-center justify-items-center gap-2 rounded-lg border border-dashed px-4 text-center text-sm text-muted-foreground"><span className="[&>svg]:size-5">{icon}</span><span>{title}</span></div>;
}

function syncStatusLabel(value: DDNSRecord["sync_status"]): string {
  switch (value) {
    case "pending": return "Pending";
    case "synced": return "Synced";
    case "failed": return "Failed";
    default: return "Not applicable";
  }
}

function servicePortKey(pluginID: string, serviceID: string): string {
  return `${pluginID}/${serviceID}`;
}

function sortRecords(values: DDNSRecord[]): DDNSRecord[] {
  return values.sort((first, second) => first.node_name.localeCompare(second.node_name) || first.display_name.localeCompare(second.display_name));
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError && cause.violations.length > 0) {
    return `${cause.message} ${cause.violations.map((violation) => `${violation.field}: ${violation.description}`).join("; ")}`;
  }
  return cause instanceof Error && cause.message ? cause.message : "The request could not be completed.";
}
