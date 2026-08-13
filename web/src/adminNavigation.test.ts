import { describe, expect, it } from "vitest";

import type { PluginInstallation } from "./api";
import {
  adminHistoryState,
  nodeDetailPath,
  nodeIDFromAdminPath,
  pluginAdminView,
  pluginIDFromAdminView,
  pluginNavigationPages,
  pluginNodeDetailPages,
} from "./adminNavigation";

function installation(overrides: Partial<PluginInstallation> = {}): PluginInstallation {
  return {
    plugin_id: "io.relayward.xray",
    repository: "https://github.com/Relayward/relayward-plugin-xray",
    kind: "runtime",
    desired_version: "0.5.0",
    active_version: "0.5.0",
    manifest: {
      api_version: "relayward.plugin/v2",
      id: "io.relayward.xray",
      name: "Relayward Xray",
      version: "0.5.0",
      kind: "runtime",
      requires: { control_api: 1, agent_api: 1, ui_api: 1 },
      permissions: [],
      ui: {
        node_detail: {
          label: { "zh-CN": "Xray", en: "Xray" },
          icon: "server-cog",
          order: 400,
        },
      },
      artifacts: [
        { role: "center", file: "center", size: 1, sha256: "a".repeat(64), os: "linux", arch: "amd64" },
        { role: "ui", file: "ui.tar.gz", size: 1, sha256: "b".repeat(64) },
      ],
    },
    approved_permissions: [],
    release_id: 1,
    state: "active",
    health: "healthy",
    restart_count: 0,
    created_at: "2026-08-11T00:00:00Z",
    updated_at: "2026-08-11T00:00:00Z",
    ...overrides,
  };
}

function featureInstallation(overrides: Partial<PluginInstallation> = {}): PluginInstallation {
  const base = installation();
  return {
    ...base,
    plugin_id: "io.relayward.reports",
    kind: "feature",
    manifest: {
      ...base.manifest,
      id: "io.relayward.reports",
      kind: "feature",
      ui: {
        navigation: {
          label: { "zh-CN": "报告", en: "Reports" },
          icon: "activity",
          group: "observability",
          order: 400,
        },
      },
      artifacts: base.manifest.artifacts.filter((artifact) => artifact.role !== "node"),
    },
    ...overrides,
  };
}

describe("plugin administration navigation", () => {
  it("uses the host locale and stable plugin view", () => {
    const pages = pluginNavigationPages([featureInstallation()], "zh-CN");
    expect(pages).toHaveLength(1);
    expect(pages[0]).toMatchObject({
      view: "plugin:io.relayward.reports",
      label: "报告",
      group: "observability",
      icon: "activity",
      unavailable: false,
    });
    expect(pluginIDFromAdminView(pluginAdminView("io.relayward.reports"))).toBe("io.relayward.reports");
  });

  it("keeps an unhealthy active plugin visible", () => {
    const pages = pluginNavigationPages([featureInstallation({ state: "failed", health: "unhealthy" })], "en");
    expect(pages).toHaveLength(1);
    expect(pages[0]).toMatchObject({ label: "Reports", unavailable: true });
  });

  it("does not expose legacy, inactive, or UI-less installations", () => {
    const legacy = featureInstallation({ manifest: { ...featureInstallation().manifest, api_version: "relayward.plugin/v1", ui: undefined } });
    const inactive = featureInstallation({ active_version: "" });
    const uiLess = featureInstallation({ manifest: { ...featureInstallation().manifest, artifacts: featureInstallation().manifest.artifacts.filter((artifact) => artifact.role !== "ui") } });
    expect(pluginNavigationPages([legacy, inactive, uiLess], "zh-CN")).toEqual([]);
  });

  it("orders contributed pages deterministically", () => {
    const later = featureInstallation();
    const earlier = featureInstallation({
      plugin_id: "io.relayward.earlier",
      manifest: {
        ...featureInstallation().manifest,
        id: "io.relayward.earlier",
        ui: { navigation: { ...featureInstallation().manifest.ui!.navigation!, order: 100 } },
      },
    });
    expect(pluginNavigationPages([later, earlier], "en").map((page) => page.plugin.plugin_id))
      .toEqual(["io.relayward.earlier", "io.relayward.reports"]);
  });
});

describe("plugin node detail pages", () => {
  it("exposes runtime plugins only in node details", () => {
    const pages = pluginNodeDetailPages([installation(), featureInstallation()], "zh-CN");
    expect(pages).toHaveLength(1);
    expect(pages[0]).toMatchObject({ label: "Xray", icon: "server-cog", unavailable: false });
    expect(pluginNavigationPages([installation()], "zh-CN")).toEqual([]);
  });

  it("keeps unhealthy runtimes visible and excludes unavailable UI artifacts", () => {
    const unhealthy = installation({ state: "failed", health: "unhealthy" });
    const inactive = installation({ active_version: "" });
    const uiLess = installation({ manifest: { ...installation().manifest, artifacts: installation().manifest.artifacts.filter((artifact) => artifact.role !== "ui") } });
    expect(pluginNodeDetailPages([unhealthy], "en")[0]).toMatchObject({ unavailable: true });
    expect(pluginNodeDetailPages([inactive, uiLess], "en")).toEqual([]);
  });
});

describe("node detail navigation", () => {
  it("round-trips encoded node identifiers", () => {
    expect(nodeIDFromAdminPath(nodeDetailPath("node/edge one"))).toBe("node/edge one");
  });

  it("rejects unrelated or malformed paths", () => {
    expect(nodeIDFromAdminPath("/nodes")).toBeUndefined();
    expect(nodeIDFromAdminPath("/nodes/one/extra")).toBeUndefined();
    expect(nodeIDFromAdminPath("/nodes/%E0%A4%A")).toBeUndefined();
  });

  it("accepts only known dashboard history state", () => {
    expect(adminHistoryState({ relaywardView: "nodes", relaywardNodeID: "node-1", relaywardReturnToNodes: true }))
      .toEqual({ relaywardView: "nodes", relaywardNodeID: "node-1", relaywardReturnToNodes: true });
    expect(adminHistoryState({ relaywardView: "unknown", relaywardNodeID: 1, relaywardReturnToNodes: false }))
      .toEqual({});
  });
});
