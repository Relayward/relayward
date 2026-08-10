export interface PluginPermissionPresentation {
  title: string;
  description: string;
}

const kernelPermissionMessages: Readonly<Record<string, PluginPermissionPresentation>> = {
  "core.events.read": {
    title: "Read standard events",
    description: "Consume standard events, including source IP and destination data when available.",
  },
  "core.events.write": {
    title: "Publish structured events",
    description: "Publish bounded structured events and notification requests to the center.",
  },
  "core.node_plugins.configure": {
    title: "Configure managed node plugins",
    description: "Read and publish this plugin's configuration on managed nodes.",
  },
  "core.nodes.read": {
    title: "Read managed nodes",
    description: "View managed node names, enabled state, and connection status.",
  },
  "core.services.write": {
    title: "Publish runtime services",
    description: "Publish this plugin's services so they can be assigned to authorizations.",
  },
};

export function pluginPermissionPresentation(name: string, manifestReason: string): PluginPermissionPresentation {
  return kernelPermissionMessages[name] ?? { title: name, description: manifestReason };
}
