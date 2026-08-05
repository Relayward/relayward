import { lazy, Suspense, useEffect, useState } from "react";

import { APIError, getSession, getSetupStatus, initialize, login, logout, type SessionInfo } from "./api";
import { Button } from "./components/ui/button";
import { LanguageSwitcher, useI18n } from "./i18n";
import { loadSystemInfo, type SystemInfo } from "./system";

const Dashboard = lazy(() => import("./components/Dashboard").then((module) => ({ default: module.Dashboard })));
const LoginScreen = lazy(() => import("./components/AuthScreen").then((module) => ({ default: module.LoginScreen })));
const SetupScreen = lazy(() => import("./components/AuthScreen").then((module) => ({ default: module.SetupScreen })));
const SubscriptionPage = lazy(() => import("./components/SubscriptionPage").then((module) => ({ default: module.SubscriptionPage })));

type AppState =
  | { phase: "loading" }
  | { phase: "setup"; busy: boolean; error?: string }
  | { phase: "login"; busy: boolean; secondFactorRequired: boolean; error?: string }
  | { phase: "dashboard"; session: SessionInfo; system: SystemInfo }
  | { phase: "error"; message: string };

export function App() {
  const { t } = useI18n();
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
    return <Suspense fallback={<AppLoading />}><SubscriptionPage token={subscriptionToken} /></Suspense>;
  }
  if (state.phase === "loading") {
    return <AppLoading />;
  }
  if (state.phase === "setup") {
    return <Suspense fallback={<AppLoading />}><SetupScreen busy={state.busy} error={state.error} onSubmit={setup} /></Suspense>;
  }
  if (state.phase === "login") {
    return <Suspense fallback={<AppLoading />}><LoginScreen busy={state.busy} error={state.error} secondFactorRequired={state.secondFactorRequired} onSubmit={signIn} /></Suspense>;
  }
  if (state.phase === "error") {
    return (
      <main className="relative flex min-h-screen flex-col items-center justify-center gap-3.5 p-6 text-center">
        <LanguageSwitcher className="absolute top-5 right-5" />
        <span className="flex size-11 items-center justify-center rounded-md bg-foreground text-xl font-bold text-background">R</span>
        <h1 className="m-0 text-2xl font-semibold">{t("Relayward unavailable")}</h1>
        <p className="m-0 text-sm text-muted-foreground">{t(state.message)}</p>
        <Button size="sm" onClick={() => window.location.reload()}>{t("Retry")}</Button>
      </main>
    );
  }
  return <Suspense fallback={<AppLoading />}>
    <Dashboard
      session={state.session}
      system={state.system}
      onLogout={signOut}
      onSessionChange={(session) => setState({ ...state, session })}
      onSessionRevoked={() => setState({ phase: "login", busy: false, secondFactorRequired: false })}
    />
  </Suspense>;
}

function AppLoading() {
  return <div className="flex min-h-screen flex-col items-center justify-center gap-3.5 p-6" role="status"><span className="flex size-11 items-center justify-center rounded-md bg-foreground text-xl font-bold text-background">R</span><span>Relayward</span></div>;
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
