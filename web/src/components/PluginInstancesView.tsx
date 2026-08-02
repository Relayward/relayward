import { type FormEvent, useEffect, useState } from "react";
import { Settings2 } from "lucide-react";

import {
  APIError,
  listNodePluginInstances,
  reconcileNodePlugin,
  type NodePluginInstance,
  type PluginState,
} from "../api";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";

type DesiredState = Exclude<PluginState, "failed">;

export function PluginInstancesView() {
  const [items, setItems] = useState<NodePluginInstance[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [editing, setEditing] = useState<NodePluginInstance>();

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const values = await listNodePluginInstances();
        if (active) {
          setItems(values);
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
    <section aria-labelledby="plugins-title">
      <div className="section-heading">
        <div><p className="eyebrow">Runtime</p><h1 id="plugins-title">Node plugins</h1></div>
      </div>
      <FormError message={error} />
      <div className="table-frame">
        <table className="resource-table plugin-table">
          <thead><tr><th>Plugin</th><th>Node</th><th>Desired</th><th>Actual</th><th>Generation</th><th>Health</th><th>Version</th><th>Delivery</th><th>Actions</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={9} className="empty-cell">Loading...</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={9} className="empty-cell">No node plugins have been configured.</td></tr> : null}
            {items.map((item) => (
              <tr key={`${item.node_id}:${item.plugin_id}`}>
                <td data-label="Plugin"><span className="plugin-identity"><strong>{item.plugin_name}</strong><small>{item.plugin_id}</small></span></td>
                <td data-label="Node">{item.node_name}</td>
                <td data-label="Desired"><StateStatus value={item.desired_state} /></td>
                <td data-label="Actual"><StateStatus value={item.actual_state} /></td>
                <td data-label="Generation" className="secondary-cell" title="Actual / desired">{item.actual_generation} / {item.generation}</td>
                <td data-label="Health"><HealthStatus value={item} /></td>
                <td data-label="Version" className="secondary-cell">{item.active_version || item.desired_version || "None"}</td>
                <td data-label="Delivery"><DeliveryStatus value={item} /></td>
                <td className="table-actions">
                  <button
                    className="icon-button"
                    aria-label="Configure node plugin"
                    title={item.command_status === "pending" ? "A reconciliation is already pending" : "Configure node plugin"}
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
    </section>
  );
}

function PluginConfigurationDialog({ value, onClose, onSaved }: {
  value: NodePluginInstance;
  onClose: () => void;
  onSaved: (value: NodePluginInstance) => void;
}) {
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
      try {
        const candidate: unknown = JSON.parse(configuration);
        if (typeof candidate !== "object" || candidate === null || Array.isArray(candidate)) {
          throw new Error("configuration must be an object");
        }
        parsed = candidate as Record<string, unknown>;
      } catch {
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
    <Modal title={`${value.plugin_name} on ${value.node_name}`} onClose={onClose}>
      <form onSubmit={submit}>
        <div className="dialog-fields">
          <label className="field">
            <span>Desired state</span>
            <select value={state} onChange={(event) => setState(event.target.value as DesiredState)}>
              <option value="running">Running</option>
              <option value="stopped">Stopped</option>
              <option value="absent">Absent</option>
            </select>
          </label>
          <label className="field">
            <span>Version</span>
            <input value={version} onChange={(event) => setVersion(event.target.value)} disabled={state === "absent"} required={state !== "absent"} />
          </label>
          <label className="field">
            <span>Configuration override</span>
            <textarea value={configuration} onChange={(event) => setConfiguration(event.target.value)} disabled={state === "absent"} rows={8} spellCheck={false} />
          </label>
        </div>
        <FormError message={error} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">Cancel</button>
          <button className="primary-button compact" disabled={busy} type="submit">{busy ? "Queuing..." : "Apply"}</button>
        </div>
      </form>
    </Modal>
  );
}

function StateStatus({ value }: { value: PluginState }) {
  const tone = value === "running" ? "ok" : value === "failed" ? "error" : value === "stopped" ? "warning" : "muted";
  return <Status value={label(value)} tone={tone} />;
}

function HealthStatus({ value }: { value: NodePluginInstance }) {
  const tone = value.health === "healthy" ? "ok" : value.health === "unhealthy" ? "error" : "muted";
  const detail = value.reason || (value.restart_count === 1 ? "1 restart" : `${value.restart_count} restarts`);
  return <span className="agent-update-cell" title={value.reason}><Status value={label(value.health)} tone={tone} /><small>{detail}</small></span>;
}

function DeliveryStatus({ value }: { value: NodePluginInstance }) {
  const tone = value.command_status === "succeeded" ? "ok" : value.command_status === "failed" ? "error" : value.command_status === "pending" ? "warning" : "muted";
  const attempts = value.command_attempts === 1 ? "1 delivery" : `${value.command_attempts} deliveries`;
  const detail = value.last_problem?.message ?? (value.command_status === "pending" && value.command_attempts === 0 ? "Waiting" : attempts);
  return <span className="agent-update-cell" title={value.last_problem?.message}><Status value={label(value.command_status)} tone={tone} />{detail ? <small>{detail}</small> : null}</span>;
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
