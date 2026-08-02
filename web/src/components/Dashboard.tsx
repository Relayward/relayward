import { type ReactNode, useEffect, useState } from "react";
import QRCode from "qrcode";

import {
  APIError,
  disableTOTP,
  enableTOTP,
  prepareTOTP,
  regenerateRecoveryCodes,
  type SessionInfo,
  type TOTPPreparation,
} from "../api";
import type { SystemInfo } from "../system";
import { Field, FormError } from "./AuthScreen";

interface DashboardProps {
  session: SessionInfo;
  system: SystemInfo;
  onLogout: () => Promise<void>;
  onSessionChange: (session: SessionInfo) => void;
  onSessionRevoked: () => void;
}

type View = "system" | "security";

export function Dashboard({ session, system, onLogout, onSessionChange, onSessionRevoked }: DashboardProps) {
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
          <span className="admin-name">{session.administrator.username}</span>
          <button className="quiet-button" disabled={busy} onClick={signOut}>Sign out</button>
        </div>
      </header>
      <div className="dashboard-body">
        <nav className="side-nav" aria-label="Administration">
          <button className={view === "system" ? "active" : ""} onClick={() => setView("system")}>System</button>
          <button className={view === "security" ? "active" : ""} onClick={() => setView("security")}>Security</button>
        </nav>
        <main className="dashboard-main">
          {view === "system" ? (
            <SystemView system={system} session={session} />
          ) : (
            <SecurityView
              session={session}
              busy={busy}
              onEnable={startTOTPSetup}
              onRegenerate={() => setSensitiveAction("regenerate")}
              onDisable={() => setSensitiveAction("disable")}
            />
          )}
          <FormError message={error} />
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

function SystemView({ system, session }: { system: SystemInfo; session: SessionInfo }) {
  return (
    <section aria-labelledby="system-title">
      <div className="section-heading">
        <div><p className="eyebrow">Overview</p><h1 id="system-title">System</h1></div>
        <span className="version-label">{system.version}</span>
      </div>
      <dl className="detail-list">
        <Detail label="Control plane" value="Available" status="ok" />
        <Detail label="Secret storage" value={session.secrets_available ? "Available" : "Recovery required"} status={session.secrets_available ? "ok" : "warning"} />
        <Detail label="Session expiry" value={new Date(session.expires_at).toLocaleString()} />
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
  return (
    <section aria-labelledby="security-title">
      <div className="section-heading"><div><p className="eyebrow">Administrator</p><h1 id="security-title">Security</h1></div></div>
      <div className="settings-list">
        <div className="setting-row">
          <div><h2>Two-factor authentication</h2><p>{session.administrator.totp_enabled ? "Enabled" : "Disabled"}</p></div>
          {session.administrator.totp_enabled ? (
            <div className="row-actions">
              <button className="secondary-button" onClick={onRegenerate}>New recovery codes</button>
              <button className="danger-button" onClick={onDisable}>Disable</button>
            </div>
          ) : (
            <button className="primary-button compact" disabled={busy || !session.secrets_available} onClick={onEnable}>Enable</button>
          )}
        </div>
      </div>
    </section>
  );
}

function TOTPSetupDialog({ preparation, onClose, onEnabled }: { preparation: TOTPPreparation; onClose: () => void; onEnabled: (codes: string[]) => void }) {
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
    <Modal title="Enable two-factor authentication" onClose={onClose}>
      <div className="totp-layout">
        <div className="qr-frame">{qrCode ? <img src={qrCode} alt="TOTP QR code" /> : <span>Generating...</span>}</div>
        <code className="secret-value">{preparation.secret}</code>
      </div>
      <Field label="Authentication code" value={code} onChange={setCode} autoComplete="one-time-code" autoFocus />
      <FormError message={error} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose}>Cancel</button>
        <button className="primary-button compact" disabled={busy || code.length !== 6} onClick={submit}>{busy ? "Enabling..." : "Enable"}</button>
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
    <Modal title={action === "disable" ? "Disable two-factor authentication" : "Generate new recovery codes"} onClose={onClose}>
      <div className="dialog-fields">
        <Field label="Password" value={password} onChange={setPassword} type="password" autoComplete="current-password" />
        <Field label="Authentication or recovery code" value={secondFactor} onChange={setSecondFactor} autoComplete="one-time-code" />
      </div>
      <FormError message={error} />
      <div className="dialog-actions">
        <button className="quiet-button" onClick={onClose}>Cancel</button>
        <button className={action === "disable" ? "danger-button" : "primary-button compact"} disabled={busy} onClick={submit}>
          {busy ? "Saving..." : action === "disable" ? "Disable" : "Generate"}
        </button>
      </div>
    </Modal>
  );
}

function RecoveryCodesDialog({ codes, onClose }: { codes: string[]; onClose: () => void }) {
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
    <Modal title="Recovery codes" onClose={onClose} dismissible={false}>
      <div className="recovery-grid">{codes.map((code) => <code key={code}>{code}</code>)}</div>
      <div className="dialog-actions">
        <button className="secondary-button" onClick={copy}>{copied ? "Copied" : "Copy"}</button>
        <button className="primary-button compact" onClick={onClose}>Done</button>
      </div>
    </Modal>
  );
}

function Modal({ title, children, onClose, dismissible = true }: { title: string; children: ReactNode; onClose: () => void; dismissible?: boolean }) {
  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => {
      if (dismissible && event.target === event.currentTarget) onClose();
    }}>
      <section className="modal" role="dialog" aria-modal="true" aria-labelledby="modal-title">
        <div className="modal-heading"><h2 id="modal-title">{title}</h2>{dismissible ? <button className="close-button" onClick={onClose} aria-label="Close">×</button> : null}</div>
        {children}
      </section>
    </div>
  );
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
