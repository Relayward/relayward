import { describe, expect, it } from "vitest";

import { pluginPermissionPresentation } from "./pluginPermissions";

describe("plugin permission presentation", () => {
  it("uses stable copy for supported kernel permissions", () => {
    expect(pluginPermissionPresentation("core.nodes.read", "Plugin-provided reason.")).toEqual({
      title: "Read managed nodes",
      description: "View managed node names, enabled state, and connection status.",
    });
  });

  it("keeps unknown permission details from the manifest", () => {
    expect(pluginPermissionPresentation("plugin.example.read", "Read example data.")).toEqual({
      title: "plugin.example.read",
      description: "Read example data.",
    });
  });
});
