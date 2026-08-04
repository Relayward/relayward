import { type ReactNode, useEffect, useState } from "react";
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
import { cn } from "../lib/utils";
import { Field, FormError } from "./AuthScreen";
import { AuditView } from "./AuditView";
import { AuthorizationsView } from "./AuthorizationView";
import { Modal } from "./Modal";
import { PluginsView } from "./PluginsView";
import { RecentEventsView } from "./RecentEventsView";
import { NodesView, UsersView } from "./ResourceViews";
import { Button } from "./ui/button";
import { DialogFooter } from "./ui/dialog";
import { Textarea } from "./ui/textarea";

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
    <div className="min-h-screen">
      <header className="sticky top-0 z-10 flex h-[58px] items-center justify-between border-b border-border bg-card px-6 max-[700px]:px-3.5 max-[440px]:px-2.5">
        <div className="flex items-center gap-2.5"><span className="flex size-[30px] items-center justify-center rounded-md bg-foreground text-[15px] font-bold text-background">R</span><strong>Relayward</strong></div>
        <div className="flex items-center gap-2.5 max-[440px]:gap-1">
          <LanguageSwitcher />
          <span className="text-[13px] text-muted-foreground max-[700px]:hidden">{session.administrator.username}</span>
          <Button className="max-[440px]:size-9 max-[440px]:p-0" variant="ghost" size="sm" aria-label={t("Sign out")} title={t("Sign out")} disabled={busy} onClick={signOut}><LogOut size={16} /><span className="max-[440px]:hidden">{t("Sign out")}</span></Button>
        </div>
      </header>
      <div className="grid min-h-[calc(100vh-58px)] grid-cols-[190px_minmax(0,1fr)] max-[700px]:block">
        <nav className="flex flex-col gap-1 border-r border-border bg-secondary/70 px-3 py-[18px] max-[700px]:flex-row max-[700px]:overflow-x-auto max-[700px]:border-r-0 max-[700px]:border-b max-[700px]:px-3 max-[700px]:py-2" aria-label={t("Administration")}>
          <NavigationButton active={view === "system"} onClick={() => setView("system")}><Gauge size={17} />{t("System")}</NavigationButton>
          <NavigationButton active={view === "nodes"} onClick={() => setView("nodes")}><Server size={17} />{t("Nodes")}</NavigationButton>
          <NavigationButton active={view === "plugins"} onClick={() => setView("plugins")}><Plug size={17} />{t("Plugins")}</NavigationButton>
          <NavigationButton active={view === "users"} onClick={() => setView("users")}><Users size={17} />{t("Users")}</NavigationButton>
          <NavigationButton active={view === "authorizations"} onClick={() => setView("authorizations")}><BadgeCheck size={17} />{t("Authorizations")}</NavigationButton>
          <NavigationButton active={view === "events"} onClick={() => setView("events")}><Activity size={17} />{t("Recent events")}</NavigationButton>
          <NavigationButton active={view === "announcement"} onClick={() => setView("announcement")}><Megaphone size={17} />{t("Announcement")}</NavigationButton>
          <NavigationButton active={view === "audit"} onClick={() => setView("audit")}><ScrollText size={17} />{t("Audit")}</NavigationButton>
          <NavigationButton active={view === "security"} onClick={() => setView("security")}><Shield size={17} />{t("Security")}</NavigationButton>
        </nav>
        <main className="mx-auto w-full max-w-[1100px] px-[38px] py-[34px] max-[700px]:px-4 max-[700px]:py-6">
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

function NavigationButton({ active, onClick, children }: { active: boolean; onClick: () => void; children: ReactNode }) {
  return (
    <Button
      className={cn(
        "h-[38px] w-full justify-start px-3 font-normal max-[700px]:min-w-[90px] max-[700px]:justify-center",
        active && "bg-card font-semibold text-foreground shadow-[inset_3px_0_0_var(--primary)] hover:bg-card max-[700px]:shadow-[inset_0_-3px_0_var(--primary)]",
      )}
      variant="ghost"
      onClick={onClick}
      type="button"
    >
      {children}
    </Button>
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
      <div className="mb-6 flex items-end justify-between gap-4 max-[560px]:flex-col max-[560px]:items-start">
        <div><p className="m-0 text-xs font-semibold text-muted-foreground">{t("Subscriptions")}</p><h1 className="mt-0.5 mb-0 text-[25px] font-semibold" id="announcement-title">{t("Announcement")}</h1></div>
        <Button size="sm" disabled={loading || busy} onClick={save} type="button"><Save size={17} />{busy ? t("Saving...") : t("Save")}</Button>
      </div>
      <label className="grid gap-1.5">
        <span className="text-[13px] font-semibold text-foreground/80">{t("Content")}</span>
        <Textarea className="min-h-60" value={content} onChange={(event) => { setContent(event.target.value); setSaved(false); }} maxLength={4096} rows={10} disabled={loading} />
      </label>
      {saved ? <p className="mt-3 mb-0 text-[13px] font-semibold text-success" role="status">{t("Saved.")}</p> : null}
      {error ? <div className="mt-3"><FormError message={t(error)} /></div> : null}
    </section>
  );
}

function SystemView({ system, session }: { system: SystemInfo; session: SessionInfo }) {
  const { t, formatDateTime } = useI18n();
  return (
    <section aria-labelledby="system-title">
      <div className="mb-6 flex items-end justify-between">
        <div><p className="m-0 text-xs font-semibold text-muted-foreground">{t("Overview")}</p><h1 className="mt-0.5 mb-0 text-[25px] font-semibold" id="system-title">{t("System")}</h1></div>
        <span className="text-xs font-semibold text-muted-foreground">{system.version}</span>
      </div>
      <dl className="m-0 divide-y divide-border rounded-md border border-border bg-card">
        <Detail label={t("Control plane")} value={t("Available")} status="ok" />
        <Detail label={t("Secret storage")} value={session.secrets_available ? t("Available") : t("Recovery required")} status={session.secrets_available ? "ok" : "warning"} />
        <Detail label={t("Session expiry")} value={formatDateTime(session.expires_at)} />
      </dl>
    </section>
  );
}

function Detail({ label, value, status }: { label: string; value: string; status?: "ok" | "warning" }) {
  return (
    <div className="flex min-h-[58px] items-center justify-between px-4 py-3 max-[440px]:flex-col max-[440px]:items-start max-[440px]:gap-1.5">
      <dt className="text-foreground/80">{label}</dt>
      <dd className="m-0 flex items-center gap-2 text-right max-[440px]:text-left">{status ? <span className={cn("size-2 rounded-full", status === "ok" ? "bg-success" : "bg-warning")} /> : null}{value}</dd>
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
      <div className="mb-6"><p className="m-0 text-xs font-semibold text-muted-foreground">{t("Administrator")}</p><h1 className="mt-0.5 mb-0 text-[25px] font-semibold" id="security-title">{t("Security")}</h1></div>
      <div className="rounded-md border border-border bg-card">
        <div className="flex min-h-[68px] items-center justify-between gap-5 px-4 py-3.5 max-[560px]:flex-col max-[560px]:items-start">
          <div className="grid gap-1"><h2 className="m-0 text-[15px] font-semibold">{t("Two-factor authentication")}</h2><p className="m-0 inline-flex items-center gap-2 text-[13px] text-muted-foreground"><span className={cn("size-2 rounded-full", session.administrator.totp_enabled ? "bg-success" : "bg-muted-foreground")} />{session.administrator.totp_enabled ? t("Enabled") : t("Disabled")}</p></div>
          {session.administrator.totp_enabled ? (
            <div className="flex flex-wrap items-center gap-2 max-[560px]:w-full">
              <Button className="max-[560px]:flex-1" variant="secondary" size="sm" onClick={onRegenerate} type="button">{t("New recovery codes")}</Button>
              <Button className="max-[560px]:flex-1" variant="destructive" size="sm" onClick={onDisable} type="button">{t("Disable")}</Button>
            </div>
          ) : (
            <Button className="max-[560px]:w-full" size="sm" disabled={busy || !session.secrets_available} onClick={onEnable} type="button">{t("Enable")}</Button>
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
      <div className="mb-1 flex flex-col items-center gap-3">
        <div className="flex aspect-square w-[194px] items-center justify-center border border-border bg-white">{qrCode ? <img className="size-48" src={qrCode} alt={t("TOTP QR code")} /> : <span className="text-[13px] text-muted-foreground">{t("Generating...")}</span>}</div>
        <code className="max-w-full rounded-sm bg-muted px-2.5 py-2 text-center text-[13px] [overflow-wrap:anywhere]">{preparation.secret}</code>
      </div>
      <Field label={t("Authentication code")} value={code} onChange={setCode} autoComplete="one-time-code" autoFocus />
      {error ? <FormError message={t(error)} /> : null}
      <DialogFooter className="mt-1">
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button disabled={busy || code.length !== 6} onClick={submit} type="button">{busy ? t("Enabling...") : t("Enable")}</Button>
      </DialogFooter>
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
      <div className="grid gap-4">
        <Field label={t("Password")} value={password} onChange={setPassword} type="password" autoComplete="current-password" />
        <Field label={t("Authentication or recovery code")} value={secondFactor} onChange={setSecondFactor} autoComplete="one-time-code" />
      </div>
      {error ? <FormError message={t(error)} /> : null}
      <DialogFooter className="mt-1">
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button variant={action === "disable" ? "destructive" : "default"} disabled={busy} onClick={submit} type="button">
          {busy ? t("Saving...") : action === "disable" ? t("Disable") : t("Generate")}
        </Button>
      </DialogFooter>
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
      <div className="grid grid-cols-2 gap-x-5 gap-y-2 rounded-sm border border-border bg-muted p-4 max-[440px]:grid-cols-1">{codes.map((code) => <code className="text-[13px] [overflow-wrap:anywhere]" key={code}>{code}</code>)}</div>
      <DialogFooter className="mt-1">
        <Button variant="secondary" onClick={copy} type="button">{copied ? t("Copied") : t("Copy")}</Button>
        <Button onClick={onClose} type="button">{t("Done")}</Button>
      </DialogFooter>
    </Modal>
  );
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
