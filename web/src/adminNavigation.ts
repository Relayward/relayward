import type { Locale } from "./i18n";
import type { PluginInstallation, PluginNavigationGroup, PluginNavigationIcon } from "./api";

export type CoreAdminView = "system" | "nodes" | "ddns" | "plugins" | "users" | "authorizations" | "events" | "announcement" | "audit" | "security" | "settings";
export type PluginAdminView = `plugin:${string}`;
export type AdminView = CoreAdminView | PluginAdminView;

export interface AdminHistoryState {
  relaywardView?: AdminView;
  relaywardNodeID?: string;
  relaywardReturnToNodes?: boolean;
}

export interface PluginNavigationPage {
  view: PluginAdminView;
  plugin: PluginInstallation;
  label: string;
  group: PluginNavigationGroup;
  icon: PluginNavigationIcon;
  order: number;
  unavailable: boolean;
}

export interface PluginNodeDetailPage {
  plugin: PluginInstallation;
  label: string;
  icon: PluginNavigationIcon;
  order: number;
  unavailable: boolean;
}

export function pluginAdminView(pluginID: string): PluginAdminView {
  return `plugin:${pluginID}`;
}

export function pluginIDFromAdminView(view: AdminView): string | undefined {
  return view.startsWith("plugin:") ? view.slice("plugin:".length) : undefined;
}

export function nodeDetailPath(nodeID: string): string {
  return `/nodes/${encodeURIComponent(nodeID)}`;
}

export function nodeIDFromAdminPath(pathname: string): string | undefined {
  const match = pathname.match(/^\/nodes\/([^/]+)\/?$/);
  if (match === null) return undefined;
  try {
    const nodeID = decodeURIComponent(match[1]);
    return nodeID === "" ? undefined : nodeID;
  } catch {
    return undefined;
  }
}

export function adminHistoryState(value: unknown): AdminHistoryState {
  if (typeof value !== "object" || value === null) return {};
  const candidate = value as Record<string, unknown>;
  return {
    ...(isAdminView(candidate.relaywardView) ? { relaywardView: candidate.relaywardView } : {}),
    ...(typeof candidate.relaywardNodeID === "string" ? { relaywardNodeID: candidate.relaywardNodeID } : {}),
    ...(candidate.relaywardReturnToNodes === true ? { relaywardReturnToNodes: true } : {}),
  };
}

function isAdminView(value: unknown): value is AdminView {
  return value === "system" || value === "nodes" || value === "ddns" || value === "plugins" || value === "users" ||
    value === "authorizations" || value === "events" || value === "announcement" || value === "audit" ||
    value === "security" || value === "settings" || (typeof value === "string" && value.startsWith("plugin:") && value.length > 7);
}

export function pluginNavigationPages(installations: PluginInstallation[], locale: Locale): PluginNavigationPage[] {
  return installations.flatMap((plugin) => {
    const contribution = plugin.manifest.ui?.navigation;
    const hasUIArtifact = plugin.manifest.artifacts.some((artifact) => artifact.role === "ui");
    if (plugin.kind !== "feature" || plugin.active_version === "" || contribution === undefined || !hasUIArtifact) return [];
    return [{
      view: pluginAdminView(plugin.plugin_id),
      plugin,
      label: contribution.label[locale],
      group: contribution.group,
      icon: contribution.icon,
      order: contribution.order,
      unavailable: plugin.state !== "active" || plugin.health === "unhealthy",
    }];
  }).sort((left, right) => left.order - right.order || left.plugin.plugin_id.localeCompare(right.plugin.plugin_id));
}

export function pluginNodeDetailPages(installations: PluginInstallation[], locale: Locale): PluginNodeDetailPage[] {
  return installations.flatMap((plugin) => {
    const contribution = plugin.manifest.ui?.node_detail;
    const hasUIArtifact = plugin.manifest.artifacts.some((artifact) => artifact.role === "ui");
    if (plugin.kind !== "runtime" || plugin.active_version === "" || contribution === undefined || !hasUIArtifact) return [];
    return [{
      plugin,
      label: contribution.label[locale],
      icon: contribution.icon,
      order: contribution.order,
      unavailable: plugin.state !== "active" || plugin.health === "unhealthy",
    }];
  }).sort((left, right) => left.order - right.order || left.plugin.plugin_id.localeCompare(right.plugin.plugin_id));
}
