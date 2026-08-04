import { type FormEvent, useEffect, useState } from "react";
import { ExternalLink, KeyRound, PackagePlus, RefreshCw, Trash2 } from "lucide-react";

import {
  APIError,
  inspectPluginRelease,
  installPlugin,
  listPluginInstallations,
  replacePluginGitHubToken,
  uninstallPlugin,
  upgradePlugin,
  type PluginInstallation,
  type PluginReleaseCandidate,
} from "../api";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { PluginFrame, type PluginNavigationTarget } from "./PluginFrame";
import { PluginInstancesView } from "./PluginInstancesView";

type PluginTab = "installations" | "instances";

export function PluginsView({ onNavigate }: { onNavigate: (target: PluginNavigationTarget) => void }) {
  const { t } = useI18n();
  const [tab, setTab] = useState<PluginTab>("installations");
  const [openedPlugin, setOpenedPlugin] = useState<PluginInstallation>();

  if (openedPlugin !== undefined) {
    return <PluginFrame plugin={openedPlugin} onClose={() => setOpenedPlugin(undefined)} onNavigate={onNavigate} />;
  }
  return (
    <section aria-labelledby="plugins-title">
      <div className="section-heading plugin-section-heading">
        <div><p className="eyebrow">{t("Extensions")}</p><h1 id="plugins-title">{t("Plugins")}</h1></div>
        <div className="segmented-control" aria-label={t("Plugin view")}>
          <button className={tab === "installations" ? "active" : ""} onClick={() => setTab("installations")} type="button">{t("Installations")}</button>
          <button className={tab === "instances" ? "active" : ""} onClick={() => setTab("instances")} type="button">{t("Node instances")}</button>
        </div>
      </div>
      {tab === "installations" ? <PluginInstallationsView onOpen={setOpenedPlugin} /> : <PluginInstancesView embedded />}
    </section>
  );
}

function PluginInstallationsView({ onOpen }: { onOpen: (plugin: PluginInstallation) => void }) {
  const { t } = useI18n();
  const [items, setItems] = useState<PluginInstallation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [installing, setInstalling] = useState(false);
  const [upgrading, setUpgrading] = useState<PluginInstallation>();
  const [removing, setRemoving] = useState<PluginInstallation>();
  const [replacingToken, setReplacingToken] = useState<PluginInstallation>();

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
        <FormError message={error !== undefined ? t(error) : undefined} />
        <button className="primary-button compact button-with-icon" onClick={() => setInstalling(true)} type="button">
          <PackagePlus size={17} />{t("Install plugin")}
        </button>
      </div>
      <div className="table-frame">
        <table className="resource-table plugin-installation-table">
          <thead><tr><th>{t("Plugin")}</th><th>{t("Kind")}</th><th>{t("Version")}</th><th>{t("State")}</th><th>{t("Health")}</th><th>{t("Permissions")}</th><th>{t("Actions")}</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={7} className="empty-cell">{t("Loading...")}</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={7} className="empty-cell">{t("No plugins are installed.")}</td></tr> : null}
            {items.map((item) => {
              const hasUI = item.manifest.artifacts.some((artifact) => artifact.role === "ui");
              return (
                <tr key={item.plugin_id}>
                  <td><span className="plugin-identity"><strong>{item.manifest.name}</strong><small>{item.plugin_id}</small></span></td>
                  <td className="secondary-cell">{t(label(item.kind))}</td>
                  <td><span className="plugin-version-cell"><strong>{item.active_version}</strong>{item.previous_version ? <small>{t("Previous {version}", { version: item.previous_version })}</small> : null}</span></td>
                  <td><Status value={t(label(item.state))} tone={installationStateTone(item.state)} /></td>
                  <td><Status value={t(label(item.health))} tone={item.health === "healthy" ? "ok" : item.health === "unhealthy" ? "error" : "muted"} /></td>
                  <td className="secondary-cell">{item.approved_permissions.length}</td>
                  <td className="table-actions">
                    {hasUI ? (
                      <button className="icon-button" aria-label={t("Open {name}", { name: item.manifest.name })} title={t("Open plugin")} onClick={() => onOpen(item)} type="button">
                        <ExternalLink size={17} />
                      </button>
                    ) : null}
                    <button className="icon-button" aria-label={t("Upgrade {name}", { name: item.manifest.name })} title={t("Check for upgrade")} onClick={() => setUpgrading(item)} type="button">
                      <RefreshCw size={17} />
                    </button>
                    <button className="icon-button" aria-label={t("Replace GitHub token for {name}", { name: item.manifest.name })} title={t("Replace GitHub token")} onClick={() => setReplacingToken(item)} type="button">
                      <KeyRound size={17} />
                    </button>
                    <button className="icon-button icon-button--danger" aria-label={t("Uninstall {name}", { name: item.manifest.name })} title={t("Uninstall plugin")} onClick={() => setRemoving(item)} type="button">
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
      {replacingToken ? <PluginTokenDialog plugin={replacingToken} onClose={() => setReplacingToken(undefined)} /> : null}
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

function PluginTokenDialog({ plugin, onClose }: { plugin: PluginInstallation; onClose: () => void }) {
  const { t } = useI18n();
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function save(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await replacePluginGitHubToken(plugin.plugin_id, token);
      onClose();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={t("Replace token for {name}", { name: plugin.manifest.name })} onClose={onClose}>
      <form onSubmit={save}>
        <label className="field">
          <span>{t("GitHub token")}</span>
          <input value={token} onChange={(event) => setToken(event.target.value)} type="password" autoComplete="off" required autoFocus />
        </label>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <div className="dialog-actions">
          <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
          <button className="primary-button compact" disabled={busy || token.trim() === ""} type="submit">{busy ? t("Saving...") : t("Replace")}</button>
        </div>
      </form>
    </Modal>
  );
}

function PluginReleaseDialog({ existing, onClose, onSaved }: {
  existing?: PluginInstallation;
  onClose: () => void;
  onSaved: (plugin: PluginInstallation) => void;
}) {
  const { t } = useI18n();
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
    <Modal title={existing === undefined ? t("Install plugin") : t("Upgrade {name}", { name: existing.manifest.name })} onClose={onClose} width="wide">
      <form onSubmit={inspect}>
        <div className="dialog-fields">
          <label className="field">
            <span>{t("GitHub repository")}</span>
            <input value={repository} onChange={(event) => changeSource(() => setRepository(event.target.value))} disabled={existing !== undefined} placeholder="https://github.com/owner/repository" required />
          </label>
          <div className="form-grid">
            <label className="field">
              <span>{t("Version")}</span>
              <input value={version} onChange={(event) => changeSource(() => setVersion(event.target.value))} placeholder={t("Latest stable release")} />
            </label>
            <label className="field">
              <span>{t("GitHub token")}</span>
              <input value={token} onChange={(event) => changeSource(() => setToken(event.target.value))} type="password" autoComplete="off" placeholder={existing === undefined ? t("Public repository") : t("Use saved token")} />
            </label>
          </div>
        </div>
        <div className="dialog-actions plugin-inspect-actions">
          <button className="secondary-button button-with-icon" disabled={busy} type="submit"><RefreshCw size={16} />{busy ? t("Checking...") : t("Check release")}</button>
        </div>
      </form>
      {candidate ? (
        <div className="plugin-candidate">
          <div className="plugin-candidate-summary">
            <div><strong>{candidate.manifest.name}</strong><small>{candidate.manifest.id}</small></div>
            <span>v{candidate.manifest.version}</span>
          </div>
          <div className="permission-list" aria-label={t("Requested permissions")}>
            {candidate.manifest.permissions.length === 0 ? <p>{t("No kernel permissions requested.")}</p> : null}
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
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
        <button className="primary-button compact" disabled={busy || candidate === undefined} onClick={save} type="button">
          {busy ? t("Saving...") : existing === undefined ? t("Install") : t("Upgrade")}
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
  const { t } = useI18n();
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
    <Modal title={t("Uninstall plugin")} onClose={onClose}>
      <p className="confirmation-name">{plugin.manifest.name}</p>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose} type="button">{t("Cancel")}</button>
        <button className="danger-button" disabled={busy} onClick={remove} type="button">{busy ? t("Uninstalling...") : t("Uninstall")}</button>
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
