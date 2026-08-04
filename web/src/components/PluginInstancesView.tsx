import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
import { Plus, Settings2 } from "lucide-react";

import {
  APIError,
  listNodes,
  listPluginInstallations,
  listNodePluginInstances,
  reconcileNodePlugin,
  type Node,
  type NodePluginInstance,
  type PluginInstallation,
  type PluginState,
} from "../api";
import { useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { Button } from "./ui/button";
import { DialogFooter } from "./ui/dialog";
import { Input } from "./ui/input";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Textarea } from "./ui/textarea";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type DesiredState = Exclude<PluginState, "failed">;

export function PluginInstancesView({ embedded = false }: { embedded?: boolean }) {
  const { t } = useI18n();
  const [items, setItems] = useState<NodePluginInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<NodePluginInstance>();
  const [creating, setCreating] = useState(false);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [plugins, setPlugins] = useState<PluginInstallation[]>([]);

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const [values, nodeValues, pluginValues] = await Promise.all([
          listNodePluginInstances(), listNodes(), listPluginInstallations(),
        ]);
        if (active) {
          setItems(values);
          setNodes(nodeValues.filter((node) => node.enabled && node.registered_at !== null &&
            node.capabilities.includes("control.commands") && node.capabilities.includes("plugin.supervision")));
          setPlugins(pluginValues.filter((plugin) => plugin.kind === "runtime" && plugin.state === "active"));
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
    <section aria-labelledby={embedded ? "node-plugin-instances-title" : "plugins-title"}>
      {embedded ? <h2 className="sr-only" id="node-plugin-instances-title">{t("Node plugin instances")}</h2> : (
        <div className="mb-6 flex items-end justify-between gap-4">
          <div><p className="m-0 text-xs font-semibold text-muted-foreground">{t("Runtime")}</p><h1 className="mt-0.5 mb-0 text-[25px] font-semibold" id="plugins-title">{t("Node plugins")}</h1></div>
        </div>
      )}
      {embedded ? (
        <div className="mb-3 flex min-h-11 flex-wrap items-center justify-end gap-4">
          {error ? <div className="mr-auto"><FormError message={t(error)} /></div> : null}
          <Button size="sm" onClick={() => setCreating(true)} type="button">
            <Plus size={17} />{t("Configure plugin")}
          </Button>
        </div>
      ) : error ? <div className="mb-3"><FormError message={t(error)} /></div> : null}
      <div className="overflow-hidden rounded-md border border-border bg-card">
        <Table className="min-w-[1000px]">
          <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Plugin")}</TableHead><TableHead>{t("Node")}</TableHead><TableHead>{t("Desired")}</TableHead><TableHead>{t("Actual")}</TableHead><TableHead>{t("Generation")}</TableHead><TableHead>{t("Health")}</TableHead><TableHead>{t("Version")}</TableHead><TableHead>{t("Delivery")}</TableHead><TableHead className="text-right">{t("Actions")}</TableHead></TableRow></TableHeader>
          <TableBody>
            {items.map((item) => (
              <TableRow key={`${item.node_id}:${item.plugin_id}`}>
                <TableCell><span className="grid max-w-[210px] gap-0.5"><strong className="font-semibold">{item.plugin_name}</strong><small className="overflow-hidden text-ellipsis whitespace-nowrap text-muted-foreground">{item.plugin_id}</small></span></TableCell>
                <TableCell>{item.node_name}</TableCell>
                <TableCell><StateStatus value={item.desired_state} /></TableCell>
                <TableCell><StateStatus value={item.actual_state} /></TableCell>
                <TableCell className="text-muted-foreground" title={t("Actual / desired")}>{item.actual_generation} / {item.generation}</TableCell>
                <TableCell><HealthStatus value={item} /></TableCell>
                <TableCell className="text-muted-foreground">{item.active_version || item.desired_version || t("None")}</TableCell>
                <TableCell><DeliveryStatus value={item} /></TableCell>
                <TableCell className="w-px text-right whitespace-nowrap">
                  <IconAction
                    label={t("Configure node plugin")}
                    description={item.command_status === "pending" ? t("A reconciliation is already pending") : t("Configure node plugin")}
                    disabled={item.command_status === "pending"}
                    onClick={() => setEditing(item)}
                  ><Settings2 size={17} /></IconAction>
                </TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {loading ? <div className="flex h-24 items-center justify-center border-t border-border px-4 text-center text-[13px] text-muted-foreground">{t("Loading...")}</div> : null}
        {!loading && items.length === 0 ? <div className="flex h-24 items-center justify-center border-t border-border px-4 text-center text-[13px] text-muted-foreground">{t("No node plugins have been configured.")}</div> : null}
      </div>
      {editing ? (
        <PluginConfigurationDialog
          value={editing}
          onClose={() => setEditing(undefined)}
          onSaved={(updated) => {
            setItems((current) => current.map((item) => item.node_id === updated.node_id && item.plugin_id === updated.plugin_id ? updated : item));
            setEditing(undefined);
          }}
        />
      ) : null}
      {creating ? (
        <NewPluginConfigurationDialog
          nodes={nodes}
          plugins={plugins}
          existing={items}
          onClose={() => setCreating(false)}
          onSaved={(created) => {
            setItems((current) => [...current, created].sort((left, right) =>
              left.plugin_name.localeCompare(right.plugin_name) || left.node_name.localeCompare(right.node_name)));
            setCreating(false);
          }}
        />
      ) : null}
    </section>
  );
}

function NewPluginConfigurationDialog({ nodes, plugins, existing, onClose, onSaved }: {
  nodes: Node[];
  plugins: PluginInstallation[];
  existing: NodePluginInstance[];
  onClose: () => void;
  onSaved: (value: NodePluginInstance) => void;
}) {
  const { t } = useI18n();
  const available = plugins.flatMap((plugin) => nodes
    .filter((node) => !existing.some((item) => item.node_id === node.id && item.plugin_id === plugin.plugin_id))
    .map((node) => ({ node, plugin })));
  const [selection, setSelection] = useState(available[0] ? `${available[0].node.id}\n${available[0].plugin.plugin_id}` : "");
  const [state, setState] = useState<Exclude<DesiredState, "absent">>("running");
  const [configuration, setConfiguration] = useState("{}");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    const selected = available.find((item) => `${item.node.id}\n${item.plugin.plugin_id}` === selection);
    if (selected === undefined) {
      setError("No compatible node and plugin combination is available.");
      return;
    }
    const parsed = parseConfiguration(configuration);
    if (parsed === undefined) {
      setError("Configuration must be a JSON object.");
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      onSaved(await reconcileNodePlugin(selected.node.id, selected.plugin.plugin_id, {
        desired_state: state,
        version: selected.plugin.active_version,
        configuration: parsed,
      }));
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={t("Configure node plugin")} onClose={onClose}>
      <form className="grid gap-5" onSubmit={submit}>
        <div className="grid gap-4">
          <SelectField
            label={t("Plugin and node")}
            value={selection}
            onChange={setSelection}
            disabled={available.length === 0}
            placeholder={t("No compatible targets")}
            options={available.map((item) => ({
              value: `${item.node.id}\n${item.plugin.plugin_id}`,
              label: t("{plugin} on {node}", { plugin: item.plugin.manifest.name, node: item.node.name }),
            }))}
          />
          <SelectField
            label={t("Desired state")}
            value={state}
            onChange={(value) => setState(value as Exclude<DesiredState, "absent">)}
            options={[{ value: "running", label: t("Running") }, { value: "stopped", label: t("Stopped") }]}
          />
          <FieldLabel label={t("Configuration")}>
            <Textarea className="min-h-44 font-mono" value={configuration} onChange={(event) => setConfiguration(event.target.value)} rows={8} spellCheck={false} required />
          </FieldLabel>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
          <Button disabled={busy || available.length === 0} type="submit">{busy ? t("Queuing...") : t("Configure")}</Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function PluginConfigurationDialog({ value, onClose, onSaved }: {
  value: NodePluginInstance;
  onClose: () => void;
  onSaved: (value: NodePluginInstance) => void;
}) {
  const { t } = useI18n();
  const [state, setState] = useState<DesiredState>(value.desired_state);
  const [version, setVersion] = useState(value.desired_version);
  const [configuration, setConfiguration] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    setError(undefined);
    let parsed: Record<string, unknown> | undefined;
    if (state !== "absent" && configuration.trim() !== "") {
      parsed = parseConfiguration(configuration);
      if (parsed === undefined) {
        setError("Configuration must be a JSON object.");
        return;
      }
    }
    setBusy(true);
    try {
      onSaved(await reconcileNodePlugin(value.node_id, value.plugin_id, {
        desired_state: state,
        version: state === "absent" ? "" : version,
        ...(parsed === undefined ? {} : { configuration: parsed }),
      }));
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={t("{plugin} on {node}", { plugin: value.plugin_name, node: value.node_name })} onClose={onClose}>
      <form className="grid gap-5" onSubmit={submit}>
        <div className="grid gap-4">
          <SelectField
            label={t("Desired state")}
            value={state}
            onChange={(next) => setState(next as DesiredState)}
            options={[
              { value: "running", label: t("Running") },
              { value: "stopped", label: t("Stopped") },
              { value: "absent", label: t("Absent") },
            ]}
          />
          <FieldLabel label={t("Version")}>
            <Input value={version} onChange={(event) => setVersion(event.target.value)} disabled={state === "absent"} required={state !== "absent"} />
          </FieldLabel>
          <FieldLabel label={t("Configuration override")}>
            <Textarea className="min-h-44 font-mono" value={configuration} onChange={(event) => setConfiguration(event.target.value)} disabled={state === "absent"} rows={8} spellCheck={false} />
          </FieldLabel>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
          <Button disabled={busy} type="submit">{busy ? t("Queuing...") : t("Apply")}</Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function SelectField({ label, value, onChange, options, disabled = false, placeholder }: {
  label: string;
  value: string;
  onChange: (value: string) => void;
  options: { value: string; label: string }[];
  disabled?: boolean;
  placeholder?: string;
}) {
  const id = useId();
  return (
    <label className="grid gap-1.5" htmlFor={id}>
      <span className="text-[13px] font-semibold text-foreground/80">{label}</span>
      <Select value={value} onValueChange={onChange} disabled={disabled}>
        <SelectTrigger id={id}><SelectValue placeholder={placeholder} /></SelectTrigger>
        <SelectContent>{options.map((option) => <SelectItem key={option.value} value={option.value}>{option.label}</SelectItem>)}</SelectContent>
      </Select>
    </label>
  );
}

function FieldLabel({ label, children }: { label: string; children: ReactNode }) {
  return <label className="grid gap-1.5"><span className="text-[13px] font-semibold text-foreground/80">{label}</span>{children}</label>;
}

function parseConfiguration(value: string): Record<string, unknown> | undefined {
  try {
    const candidate: unknown = JSON.parse(value);
    if (typeof candidate !== "object" || candidate === null || Array.isArray(candidate)) return undefined;
    return candidate as Record<string, unknown>;
  } catch {
    return undefined;
  }
}

function StateStatus({ value }: { value: PluginState }) {
  const { t } = useI18n();
  const tone = value === "running" ? "ok" : value === "failed" ? "error" : value === "stopped" ? "warning" : "muted";
  return <Status value={t(label(value))} tone={tone} />;
}

function HealthStatus({ value }: { value: NodePluginInstance }) {
  const { t } = useI18n();
  const tone = value.health === "healthy" ? "ok" : value.health === "unhealthy" ? "error" : "muted";
  const detail = value.reason || t(value.restart_count === 1 ? "1 restart" : "{count} restarts", { count: value.restart_count });
  return <span className="grid max-w-[150px] gap-0.5" title={value.reason}><Status value={t(label(value.health))} tone={tone} /><small className="overflow-hidden text-ellipsis whitespace-nowrap text-muted-foreground">{detail}</small></span>;
}

function DeliveryStatus({ value }: { value: NodePluginInstance }) {
  const { t } = useI18n();
  const tone = value.command_status === "succeeded" ? "ok" : value.command_status === "failed" ? "error" : value.command_status === "pending" ? "warning" : "muted";
  const attempts = t(value.command_attempts === 1 ? "1 delivery" : "{count} deliveries", { count: value.command_attempts });
  const detail = value.last_problem?.message ? t(value.last_problem.message) : value.command_status === "pending" && value.command_attempts === 0 ? t("Waiting") : attempts;
  return <span className="grid max-w-[150px] gap-0.5" title={value.last_problem?.message ? t(value.last_problem.message) : undefined}><Status value={t(label(value.command_status))} tone={tone} />{detail ? <small className="overflow-hidden text-ellipsis whitespace-nowrap text-muted-foreground">{detail}</small> : null}</span>;
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

function IconAction({ label, description, disabled, onClick, children }: {
  label: string;
  description: string;
  disabled: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          className="text-muted-foreground aria-disabled:cursor-not-allowed aria-disabled:opacity-55"
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

function label(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
