import { type FormEvent, type ReactNode, useState } from "react";

import { LanguageSwitcher, useI18n } from "../i18n";
import { BrandMark } from "./PageLayout";
import { InsecureTransportWarning } from "./InsecureTransportWarning";
import { Button } from "./ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { Input } from "./ui/input";
import { Label } from "./ui/label";

interface SetupProps {
  busy: boolean;
  error?: string;
  onSubmit: (username: string, password: string) => Promise<void>;
}

export function SetupScreen({ busy, error, onSubmit }: SetupProps) {
  const { t } = useI18n();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [confirmation, setConfirmation] = useState("");
  const [validation, setValidation] = useState<string>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    if (password !== confirmation) {
      setValidation("Passwords do not match.");
      return;
    }
    setValidation(undefined);
    await onSubmit(username, password);
  }

  return (
    <AuthLayout title={t("Initialize Relayward")}>
      <form className="grid gap-4" onSubmit={submit}>
        <Field label={t("Username")} value={username} onChange={setUsername} autoComplete="username" />
        <Field label={t("Password")} value={password} onChange={setPassword} type="password" autoComplete="new-password" />
        <Field label={t("Confirm password")} value={confirmation} onChange={setConfirmation} type="password" autoComplete="new-password" />
        <FormError message={validation !== undefined ? t(validation) : error !== undefined ? t(error) : undefined} />
        <Button className="mt-1 w-full" disabled={busy} type="submit">
          {busy ? t("Creating...") : t("Create administrator")}
        </Button>
      </form>
    </AuthLayout>
  );
}

interface LoginProps {
  busy: boolean;
  error?: string;
  secondFactorRequired: boolean;
  onSubmit: (username: string, password: string, secondFactor: string) => Promise<void>;
}

export function LoginScreen({ busy, error, secondFactorRequired, onSubmit }: LoginProps) {
  const { t } = useI18n();
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [secondFactor, setSecondFactor] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    await onSubmit(username, password, secondFactor);
  }

  return (
    <AuthLayout title={t("Sign in")}>
      <form className="grid gap-4" onSubmit={submit}>
        <Field label={t("Username")} value={username} onChange={setUsername} autoComplete="username" />
        <Field label={t("Password")} value={password} onChange={setPassword} type="password" autoComplete="current-password" />
        {secondFactorRequired ? (
          <Field
            label={t("Authentication or recovery code")}
            value={secondFactor}
            onChange={setSecondFactor}
            autoComplete="one-time-code"
            autoFocus
          />
        ) : null}
        <FormError message={error !== undefined ? t(error) : undefined} />
        <Button className="mt-1 w-full" disabled={busy} type="submit">
          {busy ? t("Signing in...") : t("Sign in")}
        </Button>
      </form>
    </AuthLayout>
  );
}

function AuthLayout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="relative flex min-h-svh flex-col items-center justify-center gap-6 bg-muted p-6 md:p-10">
      <LanguageSwitcher className="absolute top-5 right-5" />
      <BrandMark />
      <div className="grid w-full max-w-sm gap-4">
        <InsecureTransportWarning />
        <Card>
          <CardHeader className="text-center">
            <CardTitle className="text-xl"><h1 id="auth-title">{title}</h1></CardTitle>
            <CardDescription>Relayward Control Plane</CardDescription>
          </CardHeader>
          <CardContent>{children}</CardContent>
        </Card>
      </div>
    </main>
  );
}

interface FieldProps {
  label: string;
  value: string;
  onChange: (value: string) => void;
  type?: string;
  autoComplete?: string;
  autoFocus?: boolean;
  disabled?: boolean;
  required?: boolean;
}

export function Field({ label, value, onChange, type = "text", autoComplete, autoFocus, disabled = false, required = true }: FieldProps) {
  return (
    <Label className="grid gap-2">
      <span>{label}</span>
      <Input
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoComplete={autoComplete}
        autoFocus={autoFocus}
        disabled={disabled}
        required={required}
      />
    </Label>
  );
}

export function FormError({ message }: { message?: string }) {
  return message ? <p className="m-0 text-sm text-destructive" role="alert">{message}</p> : null;
}
