export type AuditTranslate = (message: string, values?: Record<string, string | number>) => string;

const actionLabels: Record<string, string> = {
  "administrator.initialize": "Administrator initialized",
  "administrator.setup": "Administrator setup completed",
  "administrator.login": "Administrator signed in",
  "administrator.logout": "Administrator signed out",
  "administrator.session.revoke": "Administrator session revoked",
  "administrator.sessions.revoke_others": "Other administrator sessions revoked",
  "administrator.username.update": "Administrator username changed",
  "administrator.password.update": "Administrator password changed",
  "administrator.password.reset": "Administrator password reset",
  "administrator.totp.enable": "Two-factor authentication enabled",
  "administrator.totp.reset": "Two-factor authentication reset",
  "administrator.recovery_codes.replace": "Recovery codes regenerated",
  "node.create": "Node created",
  "node.update": "Node updated",
  "node.delete": "Node deleted",
  "node.register": "Agent registered",
  "node.reregister": "Agent re-registered",
  "node.credential.rotate": "Agent credential rotated",
  "node.credential.revoke": "Agent credential revoked",
  "node.registration_token.create": "Node registration token created",
  "node.command.create": "Node command requested",
  "node.command.complete": "Node command completed",
  "node.agent_update.request": "Agent update requested",
  "node.agent_update.complete": "Agent update completed",
  "node.plugin_reconcile.request": "Node plugin configuration requested",
  "node.plugin_reconcile.complete": "Node plugin configuration completed",
  "node.policy_reconcile.request": "Node authorization policy requested",
  "node.policy_reconcile.complete": "Node authorization policy completed",
  "plugin.install": "Plugin installed",
  "plugin.upgrade": "Plugin upgraded",
  "plugin.rollback": "Plugin rolled back",
  "plugin.uninstall": "Plugin uninstalled",
  "plugin.github_token.replace": "Plugin GitHub token updated",
  "plugin_services.replace": "Plugin services updated",
  "user.create": "User created",
  "user.update": "User updated",
  "user.delete": "User deleted",
  "authorization.create": "Authorization created",
  "authorization.update": "Authorization updated",
  "authorization.delete": "Authorization deleted",
  "authorization.subscription_token.rotate": "Subscription token rotated",
  "service_binding.create": "Service binding created",
  "service_binding.update": "Service binding updated",
  "service_binding.delete": "Service binding deleted",
  "announcement.update": "Announcement updated",
  "system.settings.update": "System settings updated",
  "system.secrets.recover": "Secret storage recovered",
};

const actorLabels: Record<string, string> = {
  administrator: "Administrator",
  agent: "Agent",
  plugin: "Plugin",
  system: "System",
  local_admin: "Local administrator",
  anonymous: "Anonymous",
};

const targetTypeLabels: Record<string, string> = {
  administrator: "Administrator",
  session: "Session",
  node: "Node",
  user: "User",
  authorization: "Authorization",
  plugin_installation: "Plugin installation",
  node_plugin_instance: "Node plugin instance",
  agent_command: "Agent command",
  service_binding: "Service binding",
  announcement: "Announcement",
  system: "System",
  system_settings: "System settings",
};

export function auditActionLabel(action: string, t: AuditTranslate): string {
  return t(actionLabels[action] ?? humanizeIdentifier(action));
}

export function auditActorLabel(actorType: string, t: AuditTranslate): string {
  return t(actorLabels[actorType] ?? humanizeIdentifier(actorType));
}

export function auditTargetTypeLabel(targetType: string, t: AuditTranslate): string {
  return t(targetTypeLabels[targetType] ?? humanizeIdentifier(targetType));
}

export function auditOutcomeLabel(outcome: string, t: AuditTranslate): string {
  return t(humanizeIdentifier(outcome));
}

export function shortAuditID(value: string): string {
  return value.length > 12 ? `${value.slice(0, 8)}...` : value;
}

function humanizeIdentifier(value: string): string {
  const words = value.replace(/[._-]+/g, " ").trim();
  return words === "" ? value : words.charAt(0).toUpperCase() + words.slice(1);
}
