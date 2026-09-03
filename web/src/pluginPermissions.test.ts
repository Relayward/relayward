import { describe, expect, it } from "vitest";

import { pluginPermissionPresentation } from "./pluginPermissions";

describe("plugin permission presentation", () => {
  it("uses stable copy for supported kernel permissions", () => {
    expect(pluginPermissionPresentation("core.nodes.read", "Plugin-provided reason.")).toEqual({
      title: "Read managed nodes",
      description: "View managed node names, enabled state, and connection status.",
    });
    expect(pluginPermissionPresentation("core.authorizations.read", "Plugin-provided reason.")).toEqual({
      title: "Read node authorizations",
      description: "View authorization identifiers, enabled state, and bound services on a managed node.",
    });
    expect(pluginPermissionPresentation("core.network_diagnostics.read", "Plugin-provided reason.")).toEqual({
      title: "Diagnose node ports",
      description: "Read local listener status and test configured subscription endpoint ports without changing firewall rules.",
    });
    expect(pluginPermissionPresentation("core.node_plugins.diagnose", "Plugin-provided reason.")).toEqual({
      title: "Invoke node plugin diagnostics",
      description: "Run bounded diagnostics exposed by this plugin on managed nodes.",
    });
  });

  it("keeps unknown permission details from the manifest", () => {
    expect(pluginPermissionPresentation("plugin.example.read", "Read example data.")).toEqual({
      title: "plugin.example.read",
      description: "Read example data.",
    });
  });
});
