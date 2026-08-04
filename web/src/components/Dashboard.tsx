import { useEffect, useState } from "react";
import { Activity, BadgeCheck, Gauge, LogOut, Megaphone, Plug, Save, ScrollText, Server, Shield, Users } from "lucide-react";
import QRCode from "qrcode";

import {
  APIError,
  disableTOTP,
  enableTOTP,
  getAnnouncement,
  prepareTOTP,
  regenerateRecoveryCodes,
  updateAnnouncement,
  type SessionInfo,
  type TOTPPreparation,
} from "../api";
import type { SystemInfo } from "../system";
import { LanguageSwitcher, useI18n } from "../i18n";
import { Field, FormError } from "./AuthScreen";
import { AuditView } from "./AuditView";
import { AuthorizationsView } from "./AuthorizationView";
import { Modal } from "./Modal";
import { PluginsView } from "./PluginsView";
import { RecentEventsView } from "./RecentEventsView";
import { NodesView, UsersView } from "./ResourceViews";

interface DashboardProps {
  session: SessionInfo;
  system: SystemInfo;
  onLogout: () => Promise<void>;
  onSessionChange: (session: SessionInfo) => void;
  onSessionRevoked: () => void;
}

type View = "system" | "nodes" | "plugins" | "users" | "authorizations" | "events" | "announcement" | "audit" | "security";

export function Dashboard({ session, system, onLogout, onSessionChange, onSessionRevoked }: DashboardProps) {
  const { t } = useI18n();
  const [view, setView] = useState<View>("system");
  const [setup, setSetup] = useState<TOTPPreparation>();
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>();
  const [sensitiveAction, setSensitiveAction] = useState<"regenerate" | "disable">();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function signOut() {
    setBusy(true);
    setError(undefined);
    try {
      await onLogout();
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function startTOTPSetup() {
    setBusy(true);
    setError(undefined);
    try {
      setSetup(await prepareTOTP());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <div className="dashboard-shell">
      <header className="dashboard-header">
        <div className="brand-lockup"><span className="brand-mark brand-mark--small">R</span><strong>Relayward</strong></div>
        <div className="header-actions">
          <LanguageSwitcher />
          <span className="admin-name">{session.administrator.username}</span>
          <button className="quiet-button button-with-icon dashboard-sign-out" aria-label={t("Sign out")} title={t("Sign out")} disabled={busy} onClick={signOut}><LogOut size={16} /><span>{t("Sign out")}</span></button>
        </div>
      </header>
      <div className="dashboard-body">
        <nav className="side-nav" aria-label={t("Administration")}>
          <button className={view === "system" ? "active" : ""} onClick={() => setView("system")}><Gauge size={17} />{t("System")}</button>
          <button className={view === "nodes" ? "active" : ""} onClick={() => setView("nodes")}><Server size={17} />{t("Nodes")}</button>
          <button className={view === "plugins" ? "active" : ""} onClick={() => setView("plugins")}><Plug size={17} />{t("Plugins")}</button>
          <button className={view === "users" ? "active" : ""} onClick={() => setView("users")}><Users size={17} />{t("Users")}</button>
          <button className={view === "authorizations" ? "active" : ""} onClick={() => setView("authorizations")}><BadgeCheck size={17} />{t("Authorizations")}</button>
          <button className={view === "events" ? "active" : ""} onClick={() => setView("events")}><Activity size={17} />{t("Recent events")}</button>
          <button className={view === "announcement" ? "active" : ""} onClick={() => setView("announcement")}><Megaphone size={17} />{t("Announcement")}</button>
          <button className={view === "audit" ? "active" : ""} onClick={() => setView("audit")}><ScrollText size={17} />{t("Audit")}</button>
          <button className={view === "security" ? "active" : ""} onClick={() => setView("security")}><Shield size={17} />{t("Security")}</button>
        </nav>
        <main className="dashboard-main">
          {view === "system" ? <SystemView system={system} session={session} /> : null}
          {view === "nodes" ? <NodesView /> : null}
          {view === "plugins" ? <PluginsView onNavigate={(target) => setView(target)} /> : null}
          {view === "users" ? <UsersView /> : null}
          {view === "authorizations" ? <AuthorizationsView /> : null}
          {view === "events" ? <RecentEventsView /> : null}
          {view === "announcement" ? <AnnouncementView /> : null}
          {view === "audit" ? <AuditView /> : null}
          {view === "security" ? (
            <SecurityView
              session={session}
              busy={busy}
              onEnable={startTOTPSetup}
              onRegenerate={() => setSensitiveAction("regenerate")}
              onDisable={() => setSensitiveAction("disable")}
            />
          ) : null}
          <FormError message={error !== undefined ? t(error) : undefined} />
        </main>
      </div>
      {setup ? (
        <TOTPSetupDialog
          preparation={setup}
          onClose={() => setSetup(undefined)}
          onEnabled={(codes) => {
            setSetup(undefined);
            setRecoveryCodes(codes);
            onSessionChange({ ...session, administrator: { ...session.administrator, totp_enabled: true } });
          }}
        />
      ) : null}
      {sensitiveAction ? (
        <SensitiveActionDialog
          action={sensitiveAction}
          onClose={() => setSensitiveAction(undefined)}
          onRecoveryCodes={(codes) => {
            setSensitiveAction(undefined);
            setRecoveryCodes(codes);
          }}
          onDisabled={onSessionRevoked}
        />
      ) : null}
      {recoveryCodes ? <RecoveryCodesDialog codes={recoveryCodes} onClose={() => setRecoveryCodes(undefined)} /> : null}
    </div>
  );
}

function AnnouncementView() {
  const { t } = useI18n();
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    getAnnouncement().then((value) => {
      if (active) setContent(value ?? "");
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, []);

  async function save() {
    setBusy(true);
    setSaved(false);
    setError(undefined);
    try {
      setContent((await updateAnnouncement(content)) ?? "");
      setSaved(true);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby="announcement-title">
      <div className="section-heading">
        <div><p className="eyebrow">{t("Subscriptions")}</p><h1 id="announcement-title">{t("Announcement")}</h1></div>
        <button className="primary-button compact button-with-icon" disabled={loading || busy} onClick={save} type="button"><Save size={17} />{busy ? t("Saving...") : t("Save")}</button>
      </div>
      <label className="field announcement-editor"><span>{t("Content")}</span><textarea value={content} onChange={(event) => { setContent(event.target.value); setSaved(false); }} maxLength={4096} rows={10} disabled={loading} /></label>
      {saved ? <p className="form-success">{t("Saved.")}</p> : null}
      <FormError message={error !== undefined ? t(error) : undefined} />
    </section>
  );
}

function SystemView({ system, session }: { system: SystemInfo; session: SessionInfo }) {
  const { t, formatDateTime } = useI18n();
  return (
    <section aria-labelledby="system-title">
      <div className="section-heading">
        <div><p className="eyebrow">{t("Overview")}</p><h1 id="system-title">{t("System")}</h1></div>
        <span className="version-label">{system.version}</span>
      </div>
      <dl className="detail-list">
        <Detail label={t("Control plane")} value={t("Available")} status="ok" />
        <Detail label={t("Secret storage")} value={session.secrets_available ? t("Available") : t("Recovery required")} status={session.secrets_available ? "ok" : "warning"} />
        <Detail label={t("Session expiry")} value={formatDateTime(session.expires_at)} />
      </dl>
    </section>
  );
}

function Detail({ label, value, status }: { label: string; value: string; status?: "ok" | "warning" }) {
  return (
    <div>
      <dt>{label}</dt>
      <dd>{status ? <span className={`status-dot status-dot--${status}`} /> : null}{value}</dd>
    </div>
  );
}

interface SecurityViewProps {
  session: SessionInfo;
  busy: boolean;
  onEnable: () => void;
  onRegenerate: () => void;
  onDisable: () => void;
}

function SecurityView({ session, busy, onEnable, onRegenerate, onDisable }: SecurityViewProps) {
  const { t } = useI18n();
  return (
    <section aria-labelledby="security-title">
      <div className="section-heading"><div><p className="eyebrow">{t("Administrator")}</p><h1 id="security-title">{t("Security")}</h1></div></div>
      <div className="settings-list">
        <div className="setting-row">
          <div><h2>{t("Two-factor authentication")}</h2><p>{session.administrator.totp_enabled ? t("Enabled") : t("Disabled")}</p></div>
          {session.administrator.totp_enabled ? (
            <div className="row-actions">
              <button className="secondary-button" onClick={onRegenerate}>{t("New recovery codes")}</button>
              <button className="danger-button" onClick={onDisable}>{t("Disable")}</button>
            </div>
          ) : (
            <button className="primary-button compact" disabled={busy || !session.secrets_available} onClick={onEnable}>{t("Enable")}</button>
          )}
        </div>
      </div>
    </section>
  );
}

function TOTPSetupDialog({ preparation, onClose, onEnabled }: { preparation: TOTPPreparation; onClose: () => void; onEnabled: (codes: string[]) => void }) {
  const { t } = useI18n();
  const [qrCode, setQRCode] = useState<string>();
  const [code, setCode] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    QRCode.toDataURL(preparation.uri, { width: 192, margin: 1, errorCorrectionLevel: "M" }).then((value) => {
      if (active) setQRCode(value);
    }, () => {
      if (active) setError("Could not render the QR code.");
    });
    return () => { active = false; };
  }, [preparation.uri]);

  async function submit() {
    setBusy(true);
    setError(undefined);
    try {
      const result = await enableTOTP(code);
      onEnabled(result.recovery_codes);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={t("Enable two-factor authentication")} onClose={onClose}>
      <div className="totp-layout">
        <div className="qr-frame">{qrCode ? <img src={qrCode} alt={t("TOTP QR code")} /> : <span>{t("Generating...")}</span>}</div>
        <code className="secret-value">{preparation.secret}</code>
      </div>
      <Field label={t("Authentication code")} value={code} onChange={setCode} autoComplete="one-time-code" autoFocus />
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose}>{t("Cancel")}</button>
        <button className="primary-button compact" disabled={busy || code.length !== 6} onClick={submit}>{busy ? t("Enabling...") : t("Enable")}</button>
      </div>
    </Modal>
  );
}

function SensitiveActionDialog({ action, onClose, onRecoveryCodes, onDisabled }: {
  action: "regenerate" | "disable";
  onClose: () => void;
  onRecoveryCodes: (codes: string[]) => void;
  onDisabled: () => void;
}) {
  const { t } = useI18n();
  const [password, setPassword] = useState("");
  const [secondFactor, setSecondFactor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit() {
    setBusy(true);
    setError(undefined);
    try {
      if (action === "disable") {
        await disableTOTP(password, secondFactor);
        onDisabled();
      } else {
        const result = await regenerateRecoveryCodes(password, secondFactor);
        onRecoveryCodes(result.recovery_codes);
      }
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal title={action === "disable" ? t("Disable two-factor authentication") : t("Generate new recovery codes")} onClose={onClose}>
      <div className="dialog-fields">
        <Field label={t("Password")} value={password} onChange={setPassword} type="password" autoComplete="current-password" />
        <Field label={t("Authentication or recovery code")} value={secondFactor} onChange={setSecondFactor} autoComplete="one-time-code" />
      </div>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose}>{t("Cancel")}</button>
        <button className={action === "disable" ? "danger-button" : "primary-button compact"} disabled={busy} onClick={submit}>
          {busy ? t("Saving...") : action === "disable" ? t("Disable") : t("Generate")}
        </button>
      </div>
    </Modal>
  );
}

function RecoveryCodesDialog({ codes, onClose }: { codes: string[]; onClose: () => void }) {
  const { t } = useI18n();
  const [copied, setCopied] = useState(false);

  async function copy() {
    try {
      await navigator.clipboard.writeText(codes.join("\n"));
      setCopied(true);
    } catch {
      setCopied(false);
    }
  }

  return (
    <Modal title={t("Recovery codes")} onClose={onClose} dismissible={false}>
      <div className="recovery-grid">{codes.map((code) => <code key={code}>{code}</code>)}</div>
      <div className="dialog-actions">
        <button className="secondary-button" onClick={copy}>{copied ? t("Copied") : t("Copy")}</button>
        <button className="primary-button compact" onClick={onClose}>{t("Done")}</button>
      </div>
    </Modal>
  );
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
