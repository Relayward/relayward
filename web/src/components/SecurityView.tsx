import { useEffect, useState } from "react";
import { Database, KeyRound, MonitorSmartphone, UserRoundCog } from "lucide-react";

import {
  APIError,
  disableTOTP,
  enableTOTP,
  listAdministratorSessions,
  prepareTOTP,
  regenerateRecoveryCodes,
  revokeAdministratorSession,
  revokeOtherAdministratorSessions,
  updateAdministratorPassword,
  updateAdministratorUsername,
  type AdministratorSession,
  type SessionInfo,
  type TOTPPreparation,
} from "../api";
import { useI18n } from "../i18n";
import { Field, FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { PageHeader, StatusBadge } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { DialogFooter } from "./ui/dialog";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

interface SecurityViewProps {
  session: SessionInfo;
  onSessionChange: (session: SessionInfo) => void;
  onSessionRevoked: () => void;
}

export function SecurityView({ session, onSessionChange, onSessionRevoked }: SecurityViewProps) {
  const { t, formatDateTime } = useI18n();
  const [setup, setSetup] = useState<TOTPPreparation>();
  const [recoveryCodes, setRecoveryCodes] = useState<string[]>();
  const [sensitiveAction, setSensitiveAction] = useState<"regenerate" | "disable">();
  const [credentialAction, setCredentialAction] = useState<"username" | "password">();
  const [sessions, setSessions] = useState<AdministratorSession[]>([]);
  const [sessionsLoading, setSessionsLoading] = useState(true);
  const [sessionBusy, setSessionBusy] = useState<string>();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    listAdministratorSessions().then((items) => {
      if (active) setSessions(items);
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setSessionsLoading(false);
    });
    return () => { active = false; };
  }, []);

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

  async function revokeSession(id: string) {
    setSessionBusy(id);
    setError(undefined);
    try {
      await revokeAdministratorSession(id);
      setSessions((current) => current.filter((item) => item.id !== id));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSessionBusy(undefined);
    }
  }

  async function revokeOthers() {
    setSessionBusy("others");
    setError(undefined);
    try {
      await revokeOtherAdministratorSessions();
      setSessions((current) => current.filter((item) => item.current));
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setSessionBusy(undefined);
    }
  }

  return (
    <>
      <Tabs defaultValue="account">
        <section aria-labelledby="security-title">
          <PageHeader
            id="security-title"
            eyebrow={t("Administrator")}
            title={t("Security")}
            description={t("Manage administrator verification and protected secret availability.")}
            actions={<StatusBadge tone={session.administrator.totp_enabled ? "success" : "warning"}>{session.administrator.totp_enabled ? t("Protected") : t("Action required")}</StatusBadge>}
          />
          <div className="mb-4 overflow-x-auto pb-1">
            <TabsList className="min-w-max" aria-label={t("Security")}>
              <TabsTrigger value="account">{t("Administrator account")}</TabsTrigger>
              <TabsTrigger value="sessions">{t("Active sessions")}</TabsTrigger>
              <TabsTrigger value="two-factor">{t("Two-factor authentication")}</TabsTrigger>
              <TabsTrigger value="secrets">{t("Secret storage")}</TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="account">
            <Card>
              <CardHeader>
                <CardTitle>{t("Administrator account")}</CardTitle>
                <CardDescription>{t("Update the credentials used to access this control plane.")}</CardDescription>
                <CardAction><StatusBadge tone="muted">{session.administrator.username}</StatusBadge></CardAction>
              </CardHeader>
              <CardContent className="flex items-center justify-between gap-5 max-[700px]:flex-col max-[700px]:items-start">
                <div className="flex min-w-0 items-center gap-4"><span className="flex size-10 shrink-0 items-center justify-center rounded-md bg-muted text-foreground"><UserRoundCog size={19} /></span><span className="grid min-w-0 gap-1"><strong className="truncate text-sm">{session.administrator.username}</strong><small className="text-xs text-muted-foreground">{t("Single administrator account")}</small></span></div>
                <div className="flex flex-wrap gap-2 max-[700px]:w-full">
                  <Button className="max-[700px]:flex-1" variant="secondary" size="sm" onClick={() => setCredentialAction("username")} type="button">{t("Change username")}</Button>
                  <Button className="max-[700px]:flex-1" size="sm" onClick={() => setCredentialAction("password")} type="button"><KeyRound />{t("Change password")}</Button>
                </div>
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="sessions">
            <Card>
              <CardHeader>
                <CardTitle>{t("Active sessions")}</CardTitle>
                <CardDescription>{t("Review and revoke administrator sessions.")}</CardDescription>
                {sessions.length > 1 ? <CardAction><Button variant="secondary" size="sm" disabled={sessionBusy !== undefined} onClick={revokeOthers} type="button">{sessionBusy === "others" ? t("Revoking...") : t("Revoke other sessions")}</Button></CardAction> : null}
              </CardHeader>
              <CardContent className="divide-y divide-border">
                {sessionsLoading ? <p className="m-0 py-6 text-center text-sm text-muted-foreground">{t("Loading...")}</p> : null}
                {!sessionsLoading && sessions.length === 0 ? <p className="m-0 py-6 text-center text-sm text-muted-foreground">{t("No active sessions.")}</p> : null}
                {sessions.map((item) => (
                  <div className="flex min-h-16 items-center gap-4 py-3" key={item.id}>
                    <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-muted text-muted-foreground"><MonitorSmartphone size={18} /></span>
                    <span className="grid min-w-0 flex-1 gap-1">
                      <span className="flex min-w-0 items-center gap-2"><strong className="truncate text-sm" title={item.user_agent}>{item.user_agent || t("Unknown client")}</strong>{item.current ? <StatusBadge tone="success">{t("Current")}</StatusBadge> : null}</span>
                      <small className="text-xs text-muted-foreground">{t("Last active {time}; expires {expiry}", { time: formatDateTime(item.last_seen_at), expiry: formatDateTime(item.expires_at) })}</small>
                    </span>
                    {!item.current ? <Button className="shrink-0" variant="ghost" size="sm" disabled={sessionBusy !== undefined} onClick={() => revokeSession(item.id)} type="button">{sessionBusy === item.id ? t("Revoking...") : t("Revoke")}</Button> : null}
                  </div>
                ))}
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="two-factor">
            <Card>
              <CardHeader><CardTitle>{t("Two-factor authentication")}</CardTitle><CardDescription>{t("Require a password and time-based code when signing in.")}</CardDescription><CardAction><StatusBadge tone={session.administrator.totp_enabled ? "success" : "muted"}>{session.administrator.totp_enabled ? t("Enabled") : t("Disabled")}</StatusBadge></CardAction></CardHeader>
              <CardContent className="flex min-h-[112px] items-center justify-between gap-5 max-[700px]:flex-col max-[700px]:items-start">
                <div className="grid gap-2"><p className="m-0 text-xs font-semibold">{session.administrator.totp_enabled ? t("Authenticator configured") : t("Authenticator not configured")}</p><p className="m-0 text-xs text-muted-foreground">{session.administrator.totp_enabled ? t("Recovery codes can be regenerated at any time.") : t("Enable TOTP to protect the administrator account.")}</p></div>
                {session.administrator.totp_enabled ? (
                  <div className="flex flex-wrap items-center gap-2 max-[700px]:w-full">
                    <Button className="max-[700px]:flex-1" variant="secondary" size="sm" onClick={() => setSensitiveAction("regenerate")} type="button">{t("New recovery codes")}</Button>
                    <Button className="max-[700px]:flex-1" variant="destructive" size="sm" onClick={() => setSensitiveAction("disable")} type="button">{t("Disable")}</Button>
                  </div>
                ) : (
                  <Button className="max-[700px]:w-full" size="sm" disabled={busy || !session.secrets_available} onClick={startTOTPSetup} type="button">{t("Enable")}</Button>
                )}
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="secrets">
            <Card>
              <CardHeader><CardTitle>{t("Secret storage")}</CardTitle><CardDescription>{t("Protects plugin credentials and other sensitive configuration.")}</CardDescription><CardAction><StatusBadge tone={session.secrets_available ? "success" : "danger"}>{session.secrets_available ? t("Available") : t("Recovery required")}</StatusBadge></CardAction></CardHeader>
              <CardContent className="flex items-start gap-4"><span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary-soft text-primary"><Database size={18} /></span><span className="grid min-w-0 gap-1"><strong className="text-sm">{session.secrets_available ? t("Primary key loaded") : t("Primary key unavailable")}</strong><small className="break-words text-xs text-muted-foreground">{t("Secrets cannot be viewed or exported from the administration interface.")}</small></span></CardContent>
            </Card>
          </TabsContent>
          {error ? <div className="mt-4"><FormError message={t(error)} /></div> : null}
        </section>
      </Tabs>
      {credentialAction ? (
        <CredentialDialog
          action={credentialAction}
          username={session.administrator.username}
          secondFactorRequired={session.administrator.totp_enabled}
          onClose={() => setCredentialAction(undefined)}
          onUpdated={onSessionRevoked}
        />
      ) : null}
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
    </>
  );
}

function CredentialDialog({ action, username, secondFactorRequired, onClose, onUpdated }: {
  action: "username" | "password";
  username: string;
  secondFactorRequired: boolean;
  onClose: () => void;
  onUpdated: () => void;
}) {
  const { t } = useI18n();
  const [nextUsername, setNextUsername] = useState(username);
  const [password, setPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [secondFactor, setSecondFactor] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit() {
    if (action === "password" && newPassword !== confirmation) {
      setError("Passwords do not match.");
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      if (action === "username") {
        await updateAdministratorUsername(nextUsername, password, secondFactor);
      } else {
        await updateAdministratorPassword(password, newPassword, secondFactor);
      }
      onUpdated();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={action === "username" ? t("Change username") : t("Change password")} onClose={onClose}>
      <div className="grid gap-4">
        {action === "username" ? <Field label={t("New username")} value={nextUsername} onChange={setNextUsername} autoComplete="username" autoFocus /> : null}
        <Field label={t("Current password")} value={password} onChange={setPassword} type="password" autoComplete="current-password" autoFocus={action === "password"} />
        {action === "password" ? <Field label={t("New password")} value={newPassword} onChange={setNewPassword} type="password" autoComplete="new-password" /> : null}
        {action === "password" ? <Field label={t("Confirm new password")} value={confirmation} onChange={setConfirmation} type="password" autoComplete="new-password" /> : null}
        {secondFactorRequired ? <Field label={t("Authentication or recovery code")} value={secondFactor} onChange={setSecondFactor} autoComplete="one-time-code" /> : null}
      </div>
      {error ? <FormError message={t(error)} /> : null}
      <DialogFooter className="mt-1">
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button disabled={busy || password === "" || (action === "username" ? nextUsername === "" : newPassword === "" || confirmation === "") || (secondFactorRequired && secondFactor === "")} onClick={submit} type="button">{busy ? t("Saving...") : t("Save changes")}</Button>
      </DialogFooter>
    </Modal>
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
    import("qrcode")
      .then(({ default: QRCode }) => QRCode.toDataURL(preparation.uri, { width: 192, margin: 1, errorCorrectionLevel: "M" }))
      .then((value) => {
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
        <div className="flex aspect-square w-[194px] items-center justify-center border border-border bg-white">{qrCode ? <img className="size-48" src={qrCode} alt={t("TOTP QR code")} /> : <span className="text-sm text-muted-foreground">{t("Generating...")}</span>}</div>
        <code className="max-w-full rounded-sm bg-muted px-2.5 py-2 text-center text-sm [overflow-wrap:anywhere]">{preparation.secret}</code>
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
      <div className="grid grid-cols-2 gap-x-5 gap-y-2 rounded-sm border border-border bg-muted p-4 max-[440px]:grid-cols-1">{codes.map((code) => <code className="text-sm [overflow-wrap:anywhere]" key={code}>{code}</code>)}</div>
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
