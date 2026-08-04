import { type FormEvent, type ReactNode, useState } from "react";

import { LanguageSwitcher, useI18n } from "../i18n";
import { Button } from "./ui/button";
import { Input } from "./ui/input";

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
    <main className="flex min-h-screen items-center justify-center px-5 py-8">
      <section className="relative w-full max-w-[420px] rounded-md border border-border bg-card p-8 shadow-[0_14px_34px_rgba(25,32,38,0.08)] max-[440px]:px-5 max-[440px]:py-6" aria-labelledby="auth-title">
        <LanguageSwitcher className="absolute top-5 right-5" />
        <div className="flex size-11 items-center justify-center rounded-md bg-foreground text-[22px] font-bold text-background">R</div>
        <p className="mt-3.5 mb-1 text-[13px] font-semibold text-muted-foreground">Relayward</p>
        <h1 className="mt-0 mb-6.5 text-2xl font-semibold" id="auth-title">{title}</h1>
        {children}
      </section>
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
    <label className="grid gap-1.5">
      <span className="text-[13px] font-semibold text-foreground/80">{label}</span>
      <Input
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoComplete={autoComplete}
        autoFocus={autoFocus}
        disabled={disabled}
        required={required}
      />
    </label>
  );
}

export function FormError({ message }: { message?: string }) {
  return message ? <p className="m-0 text-[13px] leading-relaxed text-destructive" role="alert">{message}</p> : null;
}
