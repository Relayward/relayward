import { lazy, Suspense, useCallback, useEffect, useMemo, useState } from "react";

import { APIError, listPluginInstallations, type PluginInstallation, type SessionInfo } from "../api";
import {
  adminHistoryState,
  nodeDetailPath,
  nodeIDFromAdminPath,
  pluginIDFromAdminView,
  pluginNavigationPages,
  pluginNodeDetailPages,
  type AdminView,
} from "../adminNavigation";
import { useI18n } from "../i18n";
import type { SystemInfo } from "../system";
import { AdminShell } from "./AdminShell";
import { FormError } from "./AuthScreen";
import { Card, CardContent, CardHeader } from "./ui/card";
import { Skeleton } from "./ui/skeleton";

const AnnouncementView = lazy(() => import("./AnnouncementView").then((module) => ({ default: module.AnnouncementView })));
const AuditView = lazy(() => import("./AuditView").then((module) => ({ default: module.AuditView })));
const AuthorizationsView = lazy(() => import("./AuthorizationView").then((module) => ({ default: module.AuthorizationsView })));
const DDNSView = lazy(() => import("./DDNSView").then((module) => ({ default: module.DDNSView })));
const NodeDetailsView = lazy(() => import("./NodeDetailsView").then((module) => ({ default: module.NodeDetailsView })));
const NodesView = lazy(() => import("./ResourceViews").then((module) => ({ default: module.NodesView })));
const OverviewView = lazy(() => import("./OverviewView").then((module) => ({ default: module.OverviewView })));
const PluginsView = lazy(() => import("./PluginsView").then((module) => ({ default: module.PluginsView })));
const PluginFrame = lazy(() => import("./PluginFrame").then((module) => ({ default: module.PluginFrame })));
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
  const { locale, t } = useI18n();
  const [location, setLocation] = useState(() => dashboardLocation());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();
  const [plugins, setPlugins] = useState<PluginInstallation[]>([]);
  const [pluginsLoading, setPluginsLoading] = useState(true);
  const [pluginsError, setPluginsError] = useState<string>();

  const loadPlugins = useCallback(async () => {
    setPluginsLoading(true);
    setPluginsError(undefined);
    try {
      setPlugins(await listPluginInstallations());
    } catch (cause) {
      setPluginsError(errorMessage(cause));
    } finally {
      setPluginsLoading(false);
    }
  }, []);

  useEffect(() => { void loadPlugins(); }, [loadPlugins]);

  const pluginPages = useMemo(() => pluginNavigationPages(plugins, locale), [locale, plugins]);
  const pluginNodePages = useMemo(() => pluginNodeDetailPages(plugins, locale), [locale, plugins]);

  useEffect(() => {
    const currentState = adminHistoryState(window.history.state);
    window.history.replaceState({
      ...currentState,
      relaywardView: location.view,
      ...(location.nodeID === undefined ? {} : { relaywardNodeID: location.nodeID }),
    }, "", location.nodeID === undefined ? "/" : nodeDetailPath(location.nodeID));

    const restore = () => setLocation(dashboardLocation());
    window.addEventListener("popstate", restore);
    return () => window.removeEventListener("popstate", restore);
  }, []);

  function navigateView(nextView: AdminView) {
    const next = { view: nextView };
    window.history.pushState({ relaywardView: nextView }, "", "/");
    setLocation(next);
  }

  function openNode(nodeID: string) {
    window.history.pushState({ relaywardView: "nodes", relaywardNodeID: nodeID, relaywardReturnToNodes: true }, "", nodeDetailPath(nodeID));
    setLocation({ view: "nodes", nodeID });
  }

  function returnToNodes() {
    if (adminHistoryState(window.history.state).relaywardReturnToNodes) {
      window.history.back();
      return;
    }
    window.history.replaceState({ relaywardView: "nodes" }, "", "/");
    setLocation({ view: "nodes" });
  }

  function leaveDeletedNode() {
    window.history.replaceState({ relaywardView: "nodes" }, "", "/");
    setLocation({ view: "nodes" });
  }

  useEffect(() => {
    const pluginID = pluginIDFromAdminView(location.view);
    if (!pluginsLoading && pluginID !== undefined && !pluginPages.some((page) => page.plugin.plugin_id === pluginID)) {
      navigateView("plugins");
    }
  }, [location.view, pluginPages, pluginsLoading]);

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
    <AdminShell view={location.view} onViewChange={navigateView} session={session} system={system} busy={busy} onLogout={signOut} pluginPages={pluginPages}>
      <div className={pluginIDFromAdminView(location.view) === undefined ? "px-4 lg:px-6" : "px-0"}>
        <Suspense fallback={<PageViewFallback />}>
          <ViewContent
            view={location.view}
            nodeID={location.nodeID}
            session={session}
            system={system}
            onViewChange={navigateView}
            onOpenNode={openNode}
            onBackFromNode={returnToNodes}
            onNodeDeleted={leaveDeletedNode}
            onSessionChange={onSessionChange}
            onSessionRevoked={onSessionRevoked}
            plugins={plugins}
            pluginsLoading={pluginsLoading}
            pluginsError={pluginsError}
            pluginPages={pluginPages}
            pluginNodePages={pluginNodePages}
            onReloadPlugins={() => { void loadPlugins(); }}
            onPluginsChange={(items) => { setPlugins(items); setPluginsError(undefined); }}
          />
        </Suspense>
        <FormError message={error !== undefined ? t(error) : undefined} />
      </div>
    </AdminShell>
  );
}

function ViewContent({ view, nodeID, session, system, onViewChange, onOpenNode, onBackFromNode, onNodeDeleted, onSessionChange, onSessionRevoked, plugins, pluginsLoading, pluginsError, pluginPages, pluginNodePages, onReloadPlugins, onPluginsChange }: {
  view: AdminView;
  nodeID?: string;
  session: SessionInfo;
  system: SystemInfo;
  onViewChange: (view: AdminView) => void;
  onOpenNode: (nodeID: string) => void;
  onBackFromNode: () => void;
  onNodeDeleted: () => void;
  onSessionChange: (session: SessionInfo) => void;
  onSessionRevoked: () => void;
  plugins: PluginInstallation[];
  pluginsLoading: boolean;
  pluginsError?: string;
  pluginPages: ReturnType<typeof pluginNavigationPages>;
  pluginNodePages: ReturnType<typeof pluginNodeDetailPages>;
  onReloadPlugins: () => void;
  onPluginsChange: (plugins: PluginInstallation[]) => void;
}) {
  const { t } = useI18n();
  if (nodeID !== undefined) {
    return <NodeDetailsView nodeID={nodeID} pluginPages={pluginNodePages} onBack={onBackFromNode} onDeleted={onNodeDeleted} onOpenDDNS={() => onViewChange("ddns")} onNavigate={onViewChange} />;
  }
  switch (view) {
    case "system": return <OverviewView system={system} session={session} onNavigate={(target) => onViewChange(target)} />;
    case "nodes": return <NodesView onOpenNode={onOpenNode} />;
    case "ddns": return <DDNSView />;
    case "plugins": return <PluginsView installations={plugins} loading={pluginsLoading} loadError={pluginsError} onReload={onReloadPlugins} onInstallationsChange={onPluginsChange} />;
    case "users": return <UsersView />;
    case "authorizations": return <AuthorizationsView />;
    case "events": return <RecentEventsView />;
    case "announcement": return <AnnouncementView />;
    case "audit": return <AuditView />;
    case "security": return <SecurityView session={session} onSessionChange={onSessionChange} onSessionRevoked={onSessionRevoked} />;
    case "settings": return <SettingsView />;
  }
  const pluginID = pluginIDFromAdminView(view);
  const page = pluginPages.find((candidate) => candidate.plugin.plugin_id === pluginID);
  return page === undefined
    ? <FormError message={t("Plugin page is unavailable.")} />
    : <PluginFrame plugin={page.plugin} title={page.label} onNavigate={onViewChange} />;
}

function dashboardLocation(): { view: AdminView; nodeID?: string } {
  const state = adminHistoryState(window.history.state);
  const nodeID = nodeIDFromAdminPath(window.location.pathname);
  if (nodeID !== undefined) return { view: "nodes", nodeID };
  return { view: state.relaywardView ?? "system" };
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
