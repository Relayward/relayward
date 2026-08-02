import { useEffect, useState } from "react";

import { APIError, getSession, getSetupStatus, initialize, login, logout, type SessionInfo } from "./api";
import { LoginScreen, SetupScreen } from "./components/AuthScreen";
import { Dashboard } from "./components/Dashboard";
import { SubscriptionPage } from "./components/SubscriptionPage";
import { loadSystemInfo, type SystemInfo } from "./system";

type AppState =
  | { phase: "loading" }
  | { phase: "setup"; busy: boolean; error?: string }
  | { phase: "login"; busy: boolean; secondFactorRequired: boolean; error?: string }
  | { phase: "dashboard"; session: SessionInfo; system: SystemInfo }
  | { phase: "error"; message: string };

export function App() {
  const subscriptionToken = subscriptionTokenFromPath();
  const [state, setState] = useState<AppState>({ phase: "loading" });

  useEffect(() => {
    if (subscriptionToken !== undefined) return;
    let active = true;
    bootstrap().then((next) => {
      if (active) setState(next);
    });
    return () => { active = false; };
  }, [subscriptionToken]);

  async function setup(username: string, password: string) {
    setState({ phase: "setup", busy: true });
    try {
      const [session, system] = await Promise.all([initialize(username, password), loadSystemInfo()]);
      setState({ phase: "dashboard", session, system });
    } catch (cause) {
      setState({ phase: "setup", busy: false, error: messageFor(cause) });
    }
  }

  async function signIn(username: string, password: string, secondFactor: string) {
    const requiresSecondFactor = state.phase === "login" && state.secondFactorRequired;
    setState({ phase: "login", busy: true, secondFactorRequired: requiresSecondFactor });
    try {
      const [session, system] = await Promise.all([login(username, password, secondFactor), loadSystemInfo()]);
      setState({ phase: "dashboard", session, system });
    } catch (cause) {
      const needsSecondFactor = cause instanceof APIError && cause.hasViolation("second_factor");
      setState({ phase: "login", busy: false, secondFactorRequired: requiresSecondFactor || needsSecondFactor, error: needsSecondFactor ? undefined : messageFor(cause) });
    }
  }

  async function signOut() {
    await logout();
    setState({ phase: "login", busy: false, secondFactorRequired: false });
  }

  if (subscriptionToken !== undefined) {
    return <SubscriptionPage token={subscriptionToken} />;
  }
  if (state.phase === "loading") {
    return <div className="loading-screen"><span className="brand-mark">R</span><span>Relayward</span></div>;
  }
  if (state.phase === "setup") {
    return <SetupScreen busy={state.busy} error={state.error} onSubmit={setup} />;
  }
  if (state.phase === "login") {
    return <LoginScreen busy={state.busy} error={state.error} secondFactorRequired={state.secondFactorRequired} onSubmit={signIn} />;
  }
  if (state.phase === "error") {
    return (
      <main className="error-page">
        <span className="brand-mark">R</span>
        <h1>Relayward unavailable</h1>
        <p>{state.message}</p>
        <button className="primary-button compact" onClick={() => window.location.reload()}>Retry</button>
      </main>
    );
  }
  return (
    <Dashboard
      session={state.session}
      system={state.system}
      onLogout={signOut}
      onSessionChange={(session) => setState({ ...state, session })}
      onSessionRevoked={() => setState({ phase: "login", busy: false, secondFactorRequired: false })}
    />
  );
}

function subscriptionTokenFromPath(): string | undefined {
  const match = window.location.pathname.match(/^\/s\/([^/]+)\/?$/);
  if (!match) return undefined;
  try {
    return decodeURIComponent(match[1]);
  } catch {
    return "";
  }
}
async function bootstrap(): Promise<AppState> {
  try {
    const [setup, system] = await Promise.all([getSetupStatus(), loadSystemInfo()]);
    if (!setup.initialized) {
      return { phase: "setup", busy: false };
    }
    try {
      const session = await getSession();
      return { phase: "dashboard", session, system };
    } catch (cause) {
      if (cause instanceof APIError && cause.status === 401) {
        return { phase: "login", busy: false, secondFactorRequired: false };
      }
      throw cause;
    }
  } catch (cause) {
    return { phase: "error", message: messageFor(cause) };
  }
}

function messageFor(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The control plane could not be reached.";
}
