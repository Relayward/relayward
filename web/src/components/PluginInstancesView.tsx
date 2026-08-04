import { type FormEvent, useEffect, useState } from "react";
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
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";

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
      {embedded ? <h2 className="visually-hidden" id="node-plugin-instances-title">{t("Node plugin instances")}</h2> : (
        <div className="section-heading">
          <div><p className="eyebrow">{t("Runtime")}</p><h1 id="plugins-title">{t("Node plugins")}</h1></div>
        </div>
      )}
      {embedded ? (
        <div className="subsection-actions">
          <FormError message={error !== undefined ? t(error) : undefined} />
          <button className="primary-button compact button-with-icon" onClick={() => setCreating(true)} type="button">
            <Plus size={17} />{t("Configure plugin")}
          </button>
        </div>
      ) : <FormError message={error !== undefined ? t(error) : undefined} />}
      <div className="table-frame">
        <table className="resource-table plugin-table">
          <thead><tr><th>{t("Plugin")}</th><th>{t("Node")}</th><th>{t("Desired")}</th><th>{t("Actual")}</th><th>{t("Generation")}</th><th>{t("Health")}</th><th>{t("Version")}</th><th>{t("Delivery")}</th><th>{t("Actions")}</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={9} className="empty-cell">{t("Loading...")}</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={9} className="empty-cell">{t("No node plugins have been configured.")}</td></tr> : null}
            {items.map((item) => (
              <tr key={`${item.node_id}:${item.plugin_id}`}>
                <td data-label={t("Plugin")}><span className="plugin-identity"><strong>{item.plugin_name}</strong><small>{item.plugin_id}</small></span></td>
                <td data-label={t("Node")}>{item.node_name}</td>
                <td data-label={t("Desired")}><StateStatus value={item.desired_state} /></td>
                <td data-label={t("Actual")}><StateStatus value={item.actual_state} /></td>
                <td data-label={t("Generation")} className="secondary-cell" title={t("Actual / desired")}>{item.actual_generation} / {item.generation}</td>
                <td data-label={t("Health")}><HealthStatus value={item} /></td>
                <td data-label={t("Version")} className="secondary-cell">{item.active_version || item.desired_version || t("None")}</td>
                <td data-label={t("Delivery")}><DeliveryStatus value={item} /></td>
                <td className="table-actions">
                  <button
                    className="icon-button"
                    aria-label={t("Configure node plugin")}
                    title={item.command_status === "pending" ? t("A reconciliation is already pending") : t("Configure node plugin")}
                    disabled={item.command_status === "pending"}
                    onClick={() => setEditing(item)}
                    type="button"
                  ><Settings2 size={17} /></button>
                </td>
              </tr>
            ))}
          </tbody>
        </table>
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
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <label className="field">
            <span>{t("Plugin and node")}</span>
            <select value={selection} onChange={(event) => setSelection(event.target.value)} disabled={available.length === 0}>
              {available.length === 0 ? <option value="">{t("No compatible targets")}</option> : null}
              {available.map((item) => (
                <option key={`${item.node.id}:${item.plugin.plugin_id}`} value={`${item.node.id}\n${item.plugin.plugin_id}`}>
                  {t("{plugin} on {node}", { plugin: item.plugin.manifest.name, node: item.node.name })}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            <span>{t("Desired state")}</span>
            <select value={state} onChange={(event) => setState(event.target.value as Exclude<DesiredState, "absent">)}>
              <option value="running">{t("Running")}</option>
              <option value="stopped">{t("Stopped")}</option>
            </select>
          </label>
          <label className="field">
            <span>{t("Configuration")}</span>
            <textarea value={configuration} onChange={(event) => setConfiguration(event.target.value)} rows={8} spellCheck={false} required />
          </label>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
          <button className="primary-button compact" disabled={busy || available.length === 0} type="submit">{busy ? t("Queuing...") : t("Configure")}</button>
        </div>
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
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <label className="field">
            <span>{t("Desired state")}</span>
            <select value={state} onChange={(event) => setState(event.target.value as DesiredState)}>
              <option value="running">{t("Running")}</option>
              <option value="stopped">{t("Stopped")}</option>
              <option value="absent">{t("Absent")}</option>
            </select>
          </label>
          <label className="field">
            <span>{t("Version")}</span>
            <input value={version} onChange={(event) => setVersion(event.target.value)} disabled={state === "absent"} required={state !== "absent"} />
          </label>
          <label className="field">
            <span>{t("Configuration override")}</span>
            <textarea value={configuration} onChange={(event) => setConfiguration(event.target.value)} disabled={state === "absent"} rows={8} spellCheck={false} />
          </label>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
          <button className="primary-button compact" disabled={busy} type="submit">{busy ? t("Queuing...") : t("Apply")}</button>
        </div>
      </form>
    </Modal>
  );
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
  return <span className="agent-update-cell" title={value.reason}><Status value={t(label(value.health))} tone={tone} /><small>{detail}</small></span>;
}

function DeliveryStatus({ value }: { value: NodePluginInstance }) {
  const { t } = useI18n();
  const tone = value.command_status === "succeeded" ? "ok" : value.command_status === "failed" ? "error" : value.command_status === "pending" ? "warning" : "muted";
  const attempts = t(value.command_attempts === 1 ? "1 delivery" : "{count} deliveries", { count: value.command_attempts });
  const detail = value.last_problem?.message ? t(value.last_problem.message) : value.command_status === "pending" && value.command_attempts === 0 ? t("Waiting") : attempts;
  return <span className="agent-update-cell" title={value.last_problem?.message ? t(value.last_problem.message) : undefined}><Status value={t(label(value.command_status))} tone={tone} />{detail ? <small>{detail}</small> : null}</span>;
}

function Status({ value, tone }: { value: string; tone: "ok" | "warning" | "error" | "muted" }) {
  return <span className="inline-status"><span className={`status-dot status-dot--${tone}`} />{value}</span>;
}

function label(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
