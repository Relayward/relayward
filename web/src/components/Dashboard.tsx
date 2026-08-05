import { lazy, Suspense, useState } from "react";

import { APIError, type SessionInfo } from "../api";
import { useI18n } from "../i18n";
import type { SystemInfo } from "../system";
import { AdminShell, type AdminView } from "./AdminShell";
import { FormError } from "./AuthScreen";
import { Card, CardContent, CardHeader } from "./ui/card";
import { Skeleton } from "./ui/skeleton";

const AnnouncementView = lazy(() => import("./AnnouncementView").then((module) => ({ default: module.AnnouncementView })));
const AuditView = lazy(() => import("./AuditView").then((module) => ({ default: module.AuditView })));
const AuthorizationsView = lazy(() => import("./AuthorizationView").then((module) => ({ default: module.AuthorizationsView })));
const NodesView = lazy(() => import("./ResourceViews").then((module) => ({ default: module.NodesView })));
const OverviewView = lazy(() => import("./OverviewView").then((module) => ({ default: module.OverviewView })));
const PluginsView = lazy(() => import("./PluginsView").then((module) => ({ default: module.PluginsView })));
const RecentEventsView = lazy(() => import("./RecentEventsView").then((module) => ({ default: module.RecentEventsView })));
const SecurityView = lazy(() => import("./SecurityView").then((module) => ({ default: module.SecurityView })));
const SettingsView = lazy(() => import("./SettingsView").then((module) => ({ default: module.SettingsView })));
const UsersView = lazy(() => import("./ResourceViews").then((module) => ({ default: module.UsersView })));

interface DashboardProps {
  session: SessionInfo;
  system: SystemInfo;
  onLogout: () => Promise<void>;
  onSessionChange: (session: SessionInfo) => void;
  onSessionRevoked: () => void;
}

export function Dashboard({ session, system, onLogout, onSessionChange, onSessionRevoked }: DashboardProps) {
  const { t } = useI18n();
  const [view, setView] = useState<AdminView>("system");
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

  return (
    <AdminShell view={view} onViewChange={setView} session={session} system={system} busy={busy} onLogout={signOut}>
      <div className="px-4 lg:px-6">
        <Suspense fallback={<PageViewFallback />}>
          <ViewContent
            view={view}
            session={session}
            system={system}
            onViewChange={setView}
            onSessionChange={onSessionChange}
            onSessionRevoked={onSessionRevoked}
          />
        </Suspense>
        <FormError message={error !== undefined ? t(error) : undefined} />
      </div>
    </AdminShell>
  );
}

function ViewContent({ view, session, system, onViewChange, onSessionChange, onSessionRevoked }: {
  view: AdminView;
  session: SessionInfo;
  system: SystemInfo;
  onViewChange: (view: AdminView) => void;
  onSessionChange: (session: SessionInfo) => void;
  onSessionRevoked: () => void;
}) {
  switch (view) {
    case "system": return <OverviewView system={system} session={session} onNavigate={(target) => onViewChange(target)} />;
    case "nodes": return <NodesView />;
    case "plugins": return <PluginsView onNavigate={onViewChange} />;
    case "users": return <UsersView />;
    case "authorizations": return <AuthorizationsView />;
    case "events": return <RecentEventsView />;
    case "announcement": return <AnnouncementView />;
    case "audit": return <AuditView />;
    case "security": return <SecurityView session={session} onSessionChange={onSessionChange} onSessionRevoked={onSessionRevoked} />;
    case "settings": return <SettingsView />;
  }
}

function PageViewFallback() {
  const { t } = useI18n();
  return (
    <div className="space-y-6" role="status" aria-label={t("Loading...")}>
      <span className="sr-only">{t("Loading...")}</span>
      <div className="space-y-2">
        <Skeleton className="h-4 w-24" />
        <Skeleton className="h-8 w-56 max-w-full" />
        <Skeleton className="h-4 w-96 max-w-full" />
      </div>
      <div className="grid gap-4 sm:grid-cols-2 xl:grid-cols-4">
        {[0, 1, 2, 3].map((item) => (
          <Card key={item}>
            <CardHeader><Skeleton className="h-4 w-28" /><Skeleton className="h-9 w-20" /></CardHeader>
          </Card>
        ))}
      </div>
      <Card>
        <CardHeader><Skeleton className="h-5 w-40" /><Skeleton className="h-4 w-64 max-w-full" /></CardHeader>
        <CardContent><Skeleton className="h-40 w-full" /></CardContent>
      </Card>
    </div>
  );
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
