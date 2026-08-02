import { type FormEvent, type ReactNode, useState } from "react";

interface SetupProps {
  busy: boolean;
  error?: string;
  onSubmit: (username: string, password: string) => Promise<void>;
}

export function SetupScreen({ busy, error, onSubmit }: SetupProps) {
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
    <AuthLayout title="Initialize Relayward">
      <form className="auth-form" onSubmit={submit}>
        <Field label="Username" value={username} onChange={setUsername} autoComplete="username" />
        <Field label="Password" value={password} onChange={setPassword} type="password" autoComplete="new-password" />
        <Field label="Confirm password" value={confirmation} onChange={setConfirmation} type="password" autoComplete="new-password" />
        <FormError message={validation ?? error} />
        <button className="primary-button" disabled={busy} type="submit">
          {busy ? "Creating..." : "Create administrator"}
        </button>
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
  const [username, setUsername] = useState("admin");
  const [password, setPassword] = useState("");
  const [secondFactor, setSecondFactor] = useState("");

  async function submit(event: FormEvent) {
    event.preventDefault();
    await onSubmit(username, password, secondFactor);
  }

  return (
    <AuthLayout title="Sign in">
      <form className="auth-form" onSubmit={submit}>
        <Field label="Username" value={username} onChange={setUsername} autoComplete="username" />
        <Field label="Password" value={password} onChange={setPassword} type="password" autoComplete="current-password" />
        {secondFactorRequired ? (
          <Field
            label="Authentication or recovery code"
            value={secondFactor}
            onChange={setSecondFactor}
            autoComplete="one-time-code"
            autoFocus
          />
        ) : null}
        <FormError message={error} />
        <button className="primary-button" disabled={busy} type="submit">
          {busy ? "Signing in..." : "Sign in"}
        </button>
      </form>
    </AuthLayout>
  );
}

function AuthLayout({ title, children }: { title: string; children: ReactNode }) {
  return (
    <main className="auth-page">
      <section className="auth-panel" aria-labelledby="auth-title">
        <div className="brand-mark">R</div>
        <p className="product-label">Relayward</p>
        <h1 id="auth-title">{title}</h1>
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
}

export function Field({ label, value, onChange, type = "text", autoComplete, autoFocus }: FieldProps) {
  return (
    <label className="field">
      <span>{label}</span>
      <input
        type={type}
        value={value}
        onChange={(event) => onChange(event.target.value)}
        autoComplete={autoComplete}
        autoFocus={autoFocus}
        required
      />
    </label>
  );
}

export function FormError({ message }: { message?: string }) {
  return message ? <p className="form-error" role="alert">{message}</p> : null;
}
