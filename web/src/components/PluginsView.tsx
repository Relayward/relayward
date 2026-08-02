import { type FormEvent, useEffect, useState } from "react";
import { ExternalLink, PackagePlus, RefreshCw, Trash2 } from "lucide-react";

import {
  APIError,
  inspectPluginRelease,
  installPlugin,
  listPluginInstallations,
  uninstallPlugin,
  upgradePlugin,
  type PluginInstallation,
  type PluginReleaseCandidate,
} from "../api";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { PluginFrame, type PluginNavigationTarget } from "./PluginFrame";
import { PluginInstancesView } from "./PluginInstancesView";

type PluginTab = "installations" | "instances";

export function PluginsView({ onNavigate }: { onNavigate: (target: PluginNavigationTarget) => void }) {
  const [tab, setTab] = useState<PluginTab>("installations");
  const [openedPlugin, setOpenedPlugin] = useState<PluginInstallation>();

  if (openedPlugin !== undefined) {
    return <PluginFrame plugin={openedPlugin} onClose={() => setOpenedPlugin(undefined)} onNavigate={onNavigate} />;
  }
  return (
    <section aria-labelledby="plugins-title">
      <div className="section-heading plugin-section-heading">
        <div><p className="eyebrow">Extensions</p><h1 id="plugins-title">Plugins</h1></div>
        <div className="segmented-control" aria-label="Plugin view">
          <button className={tab === "installations" ? "active" : ""} onClick={() => setTab("installations")} type="button">Installations</button>
          <button className={tab === "instances" ? "active" : ""} onClick={() => setTab("instances")} type="button">Node instances</button>
        </div>
      </div>
      {tab === "installations" ? <PluginInstallationsView onOpen={setOpenedPlugin} /> : <PluginInstancesView embedded />}
    </section>
  );
}

function PluginInstallationsView({ onOpen }: { onOpen: (plugin: PluginInstallation) => void }) {
  const [items, setItems] = useState<PluginInstallation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [installing, setInstalling] = useState(false);
  const [upgrading, setUpgrading] = useState<PluginInstallation>();
  const [removing, setRemoving] = useState<PluginInstallation>();

  useEffect(() => {
    let active = true;
    listPluginInstallations().then((values) => {
      if (active) {
        setItems(values);
        setLoading(false);
      }
    }, (cause) => {
      if (active) {
        setError(errorMessage(cause));
        setLoading(false);
      }
    });
    return () => { active = false; };
  }, []);

  function replace(value: PluginInstallation) {
    setItems((current) => {
      const found = current.some((item) => item.plugin_id === value.plugin_id);
      return (found ? current.map((item) => item.plugin_id === value.plugin_id ? value : item) : [...current, value])
        .sort((left, right) => left.manifest.name.localeCompare(right.manifest.name));
    });
  }

  return (
    <>
      <div className="subsection-actions">
        <FormError message={error} />
        <button className="primary-button compact button-with-icon" onClick={() => setInstalling(true)} type="button">
          <PackagePlus size={17} />Install plugin
        </button>
      </div>
      <div className="table-frame">
        <table className="resource-table plugin-installation-table">
          <thead><tr><th>Plugin</th><th>Kind</th><th>Version</th><th>State</th><th>Health</th><th>Permissions</th><th>Actions</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={7} className="empty-cell">Loading...</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={7} className="empty-cell">No plugins are installed.</td></tr> : null}
            {items.map((item) => {
              const hasUI = item.manifest.artifacts.some((artifact) => artifact.role === "ui");
              return (
                <tr key={item.plugin_id}>
                  <td><span className="plugin-identity"><strong>{item.manifest.name}</strong><small>{item.plugin_id}</small></span></td>
                  <td className="secondary-cell">{label(item.kind)}</td>
                  <td><span className="plugin-version-cell"><strong>{item.active_version}</strong>{item.previous_version ? <small>Previous {item.previous_version}</small> : null}</span></td>
                  <td><Status value={label(item.state)} tone={installationStateTone(item.state)} /></td>
                  <td><Status value={label(item.health)} tone={item.health === "healthy" ? "ok" : item.health === "unhealthy" ? "error" : "muted"} /></td>
                  <td className="secondary-cell">{item.approved_permissions.length}</td>
                  <td className="table-actions">
                    {hasUI ? (
                      <button className="icon-button" aria-label={`Open ${item.manifest.name}`} title="Open plugin" onClick={() => onOpen(item)} type="button">
                        <ExternalLink size={17} />
                      </button>
                    ) : null}
                    <button className="icon-button" aria-label={`Upgrade ${item.manifest.name}`} title="Check for upgrade" onClick={() => setUpgrading(item)} type="button">
                      <RefreshCw size={17} />
                    </button>
                    <button className="icon-button icon-button--danger" aria-label={`Uninstall ${item.manifest.name}`} title="Uninstall plugin" onClick={() => setRemoving(item)} type="button">
                      <Trash2 size={17} />
                    </button>
                  </td>
                </tr>
              );
            })}
          </tbody>
        </table>
      </div>
      {installing ? <PluginReleaseDialog onClose={() => setInstalling(false)} onSaved={(value) => { replace(value); setInstalling(false); }} /> : null}
      {upgrading ? <PluginReleaseDialog existing={upgrading} onClose={() => setUpgrading(undefined)} onSaved={(value) => { replace(value); setUpgrading(undefined); }} /> : null}
      {removing ? (
        <PluginUninstallDialog
          plugin={removing}
          onClose={() => setRemoving(undefined)}
          onRemoved={() => {
            setItems((current) => current.filter((item) => item.plugin_id !== removing.plugin_id));
            setRemoving(undefined);
          }}
        />
      ) : null}
    </>
  );
}

function PluginReleaseDialog({ existing, onClose, onSaved }: {
  existing?: PluginInstallation;
  onClose: () => void;
  onSaved: (plugin: PluginInstallation) => void;
}) {
  const [repository, setRepository] = useState(existing?.repository ?? "");
  const [version, setVersion] = useState("");
  const [token, setToken] = useState("");
  const [candidate, setCandidate] = useState<PluginReleaseCandidate>();
  const [approved, setApproved] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  function changeSource(change: () => void) {
    change();
    setCandidate(undefined);
    setApproved(new Set());
  }

  async function inspect(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const value = await inspectPluginRelease({
        repository,
        version,
        ...(token === "" ? {} : { github_token: token }),
      });
      if (existing !== undefined && value.manifest.id !== existing.plugin_id) {
        throw new Error("The release belongs to a different plugin.");
      }
      setCandidate(value);
      setApproved(new Set());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    if (candidate === undefined) return;
    const permissions = candidate.manifest.permissions.map((permission) => permission.name).sort();
    if (permissions.some((permission) => !approved.has(permission))) {
      setError("Approve every requested permission before continuing.");
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const input = {
        version: candidate.manifest.version,
        approved_permissions: permissions,
        ...(token === "" ? {} : { github_token: token }),
      };
      const saved = existing === undefined
        ? await installPlugin({ repository: candidate.repository, ...input })
        : await upgradePlugin(existing.plugin_id, input);
      onSaved(saved);
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={existing === undefined ? "Install plugin" : `Upgrade ${existing.manifest.name}`} onClose={onClose} width="wide">
      <form onSubmit={inspect}>
        <div className="dialog-fields">
          <label className="field">
            <span>GitHub repository</span>
            <input value={repository} onChange={(event) => changeSource(() => setRepository(event.target.value))} disabled={existing !== undefined} placeholder="https://github.com/owner/repository" required />
          </label>
          <div className="form-grid">
            <label className="field">
              <span>Version</span>
              <input value={version} onChange={(event) => changeSource(() => setVersion(event.target.value))} placeholder="Latest stable release" />
            </label>
            <label className="field">
              <span>GitHub token</span>
              <input value={token} onChange={(event) => changeSource(() => setToken(event.target.value))} type="password" autoComplete="off" placeholder={existing === undefined ? "Public repository" : "Use saved token"} />
            </label>
          </div>
        </div>
        <div className="dialog-actions plugin-inspect-actions">
          <button className="secondary-button button-with-icon" disabled={busy} type="submit"><RefreshCw size={16} />{busy ? "Checking..." : "Check release"}</button>
        </div>
      </form>
      {candidate ? (
        <div className="plugin-candidate">
          <div className="plugin-candidate-summary">
            <div><strong>{candidate.manifest.name}</strong><small>{candidate.manifest.id}</small></div>
            <span>v{candidate.manifest.version}</span>
          </div>
          <div className="permission-list" aria-label="Requested permissions">
            {candidate.manifest.permissions.length === 0 ? <p>No kernel permissions requested.</p> : null}
            {candidate.manifest.permissions.map((permission) => (
              <label key={permission.name} className="permission-row">
                <input
                  type="checkbox"
                  checked={approved.has(permission.name)}
                  onChange={(event) => setApproved((current) => {
                    const next = new Set(current);
                    if (event.target.checked) next.add(permission.name); else next.delete(permission.name);
                    return next;
                  })}
                />
                <span><strong>{permission.name}</strong><small>{permission.reason}</small></span>
              </label>
            ))}
          </div>
        </div>
      ) : null}
      <FormError message={error} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">Cancel</button>
        <button className="primary-button compact" disabled={busy || candidate === undefined} onClick={save} type="button">
          {busy ? "Saving..." : existing === undefined ? "Install" : "Upgrade"}
        </button>
      </div>
    </Modal>
  );
}

function PluginUninstallDialog({ plugin, onClose, onRemoved }: {
  plugin: PluginInstallation;
  onClose: () => void;
  onRemoved: () => void;
}) {
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function remove() {
    setBusy(true);
    setError(undefined);
    try {
      await uninstallPlugin(plugin.plugin_id);
      onRemoved();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title="Uninstall plugin" onClose={onClose}>
      <p className="confirmation-name">{plugin.manifest.name}</p>
      <FormError message={error} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">Cancel</button>
        <button className="danger-button" disabled={busy} onClick={remove} type="button">{busy ? "Uninstalling..." : "Uninstall"}</button>
      </div>
    </Modal>
  );
}

function Status({ value, tone }: { value: string; tone: "ok" | "warning" | "error" | "muted" }) {
  return <span className="inline-status"><span className={`status-dot status-dot--${tone}`} />{value}</span>;
}

function installationStateTone(value: PluginInstallation["state"]): "ok" | "warning" | "error" {
  if (value === "active") return "ok";
  if (value === "failed") return "error";
  return "warning";
}

function label(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  if (cause instanceof Error) return cause.message;
  return "The request could not be completed.";
}
