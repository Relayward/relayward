export interface SetupStatus {
  initialized: boolean;
  secrets_available: boolean;
}
export interface SessionInfo {
  administrator: {
    username: string;
    totp_enabled: boolean;
  };
  expires_at: string;
  secrets_available: boolean;
}

export interface TOTPPreparation {
  secret: string;
  uri: string;
}

export interface RecoveryCodes {
  recovery_codes: string[];
}

export interface AdministratorSession {
  id: string;
  user_agent: string;
  current: boolean;
  created_at: string;
  last_seen_at: string;
  expires_at: string;
}

export interface SystemSettings {
  session_lifetime_minutes: number;
  timezone: string;
  public_url: string;
  subscription_title: string;
  support_url: string;
  profile_url: string;
  subscription_refresh_hours: number;
  updated_at: string;
}

export interface Node {
  id: string;
  name: string;
  public_address: string;
  enabled: boolean;
  agent_status: "pending" | "online" | "offline" | "disabled";
  hostname: string;
  agent_version: string;
  agent_os: string;
  agent_arch: string;
  capabilities: string[];
  agent_started_at: string | null;
  policy: NodePolicyState | null;
  registered_at: string | null;
  last_seen_at: string | null;
  created_at: string;
  updated_at: string;
}

export interface NodePolicyState {
  desired_generation: number;
  applied_generation: number;
  status: "not_configured" | "pending" | "applied" | "failed" | "unsupported";
  last_problem?: Problem;
  updated_at: string;
}

export interface NodeInput {
  name: string;
  public_address: string;
  enabled: boolean;
}

export interface NodeRegistrationToken {
  token: string;
  expires_at: string;
}

export interface AgentUpdate {
  id: string;
  node_id: string;
  version: string;
  status: "pending" | "succeeded" | "failed" | "expired";
  attempts: number;
  last_sent_at: string | null;
  problem?: Problem;
  completed_at: string | null;
  expires_at: string;
  created_at: string;
  updated_at: string;
}

export interface AgentRelease {
  version: string;
  tag: string;
  published_at: string;
  checked_at: string;
}

export interface AgentUpdateAvailability {
  current_version: string;
  latest_release: AgentRelease;
  relation: "available" | "current" | "ahead" | "unknown";
}

export interface NodeCommand {
  id: string;
  node_id: string;
  kind: string;
  scope_key: string;
  status: "pending" | "succeeded" | "failed" | "expired";
  attempts: number;
  last_sent_at: string | null;
  problem?: Problem;
  completed_at: string | null;
  expires_at: string;
  created_at: string;
  updated_at: string;
}

export type PluginState = "running" | "stopped" | "absent" | "failed";

export interface NodePluginInstance {
  node_id: string;
  node_name: string;
  plugin_id: string;
  plugin_name: string;
  desired_version: string;
  active_version: string;
  desired_state: Exclude<PluginState, "failed">;
  actual_state: PluginState;
  generation: number;
  desired_configuration_sha256: string;
  artifact_size: number;
  artifact_sha256: string;
  actual_generation: number;
  actual_configuration_sha256: string;
  health: "healthy" | "unhealthy" | "unknown";
  reason: string;
  restart_count: number;
  capabilities: string[];
  reconcile_status: "pending" | "succeeded" | "failed" | "expired";
  last_problem?: Problem;
  last_command_id: string;
  command_status: "none" | "pending" | "succeeded" | "failed" | "expired";
  command_attempts: number;
  command_last_sent_at: string | null;
  command_completed_at: string | null;
  actual_observed_at: string | null;
  updated_at: string;
}

export interface NodePluginInput {
  desired_state: Exclude<PluginState, "failed">;
  version: string;
  configuration?: Record<string, unknown>;
}

export interface PluginPermission {
  name: string;
  reason: string;
}

export interface PluginArtifact {
  role: "center" | "node" | "ui";
  file: string;
  size: number;
  sha256: string;
  os?: string;
  arch?: string;
}

export interface PluginManifest {
  api_version: "relayward.plugin/v1";
  id: string;
  name: string;
  version: string;
  kind: "runtime" | "feature";
  requires: {
    control_api: number;
    agent_api?: number;
    ui_api?: number;
  };
  permissions: PluginPermission[];
  artifacts: PluginArtifact[];
}

export interface PluginReleaseCandidate {
  repository: string;
  release_id: number;
  tag: string;
  manifest: PluginManifest;
  update: boolean;
}

export interface PluginInstallation {
  plugin_id: string;
  repository: string;
  kind: "runtime" | "feature";
  desired_version: string;
  active_version: string;
  previous_version?: string;
  manifest: PluginManifest;
  approved_permissions: string[];
  release_id: number;
  state: "pending" | "installing" | "active" | "failed";
  health: "healthy" | "unhealthy" | "unknown";
  restart_count: number;
  last_problem?: Problem;
  last_started_at?: string;
  created_at: string;
  updated_at: string;
}

export interface PluginReleaseInput {
  repository: string;
  version: string;
  github_token?: string;
  approved_permissions?: string[];
}

export interface PluginUpgradeInput {
  version: string;
  github_token?: string;
  approved_permissions: string[];
}

export interface User {
  id: string;
  display_name: string;
  email: string | null;
  telegram: string | null;
  note: string;
  created_at: string;
  updated_at: string;
}

export interface UserInput {
  display_name: string;
  email: string | null;
  telegram: string | null;
  note: string;
}

export type ResetKind = "never" | "daily" | "weekly" | "monthly" | "interval_days";

export interface ResetRule {
  kind: ResetKind;
  value: number | null;
  timezone: string;
  period_anchor: string | null;
}

export interface Authorization {
  id: string;
  user_id: string;
  node_id: string;
  enabled: boolean;
  traffic_limit_bytes: number | null;
  reset: ResetRule;
  expires_at: string | null;
  soft_ip_limit: number | null;
  activity_window_seconds: number;
  block_duration_seconds: number;
  current_traffic: TrafficPeriod | null;
  enforcement: AuthorizationEnforcement | null;
  created_at: string;
  updated_at: string;
}

export interface Period {
  id: string;
  starts_at: string;
  ends_at: string | null;
}

export interface TrafficPeriod {
  period: Period;
  revision: number;
  upload_bytes: number;
  download_bytes: number;
  observed_at: string;
}

export interface AuthorizationEnforcement {
  generation: number;
  period: Period;
  upload_bytes: number;
  download_bytes: number;
  services_enabled: boolean;
  reason: "active" | "administrator_disabled" | "expired" | "quota_exceeded";
  active_ip_count: number;
  blocked_ip_count: number;
  observed_at: string;
}

export interface AccessEvent {
  id: number;
  node_id: string;
  plugin_id: string;
  service_id: string;
  authorization_id: string;
  source_ip?: string;
  destination?: string;
  destination_port?: number;
  network?: string;
  protocol?: string;
  action: "accepted" | "blocked";
  observed_at: string;
  received_at: string;
}

export interface AuthorizationInput {
  user_id: string;
  node_id: string;
  enabled: boolean;
  traffic_limit_bytes: number | null;
  reset: ResetRule;
  expires_at: string | null;
  soft_ip_limit: number | null;
  activity_window_seconds: number;
  block_duration_seconds: number;
}

export interface SubscriptionToken {
  subscription_token: string;
  rotated_at?: string;
}

export interface ServiceBinding {
  id: string;
  authorization_id: string;
  plugin_id: string;
  service_id: string;
  enabled: boolean;
  created_at: string;
  updated_at: string;
}

export interface PluginService {
  node_id: string;
  plugin_id: string;
  service_id: string;
  display_name: string;
  enabled: boolean;
  capabilities: string[];
}

export interface SubscriptionService {
  plugin_id: string;
  service_id: string;
  display_name: string;
  capabilities: string[];
}

export interface AuditEntry {
  id: number;
  occurred_at: string;
  actor_type: string;
  actor_id: string;
  action: string;
  target_type: string;
  target_id: string;
  outcome: string;
  metadata: Record<string, unknown>;
}

export interface SubscriptionInfo {
  title: string;
  support_url: string;
  profile_url: string;
  refresh_hours: number;
  status: "active" | "disabled" | "expired" | "node_disabled" | "quota_exceeded";
  user_name: string;
  node_name: string;
  node_address: string;
  traffic_limit_bytes: number | null;
  traffic_used_bytes: number | null;
  reset: ResetRule;
  expires_at: string | null;
  services: SubscriptionService[];
  announcement: string | null;
}

interface FieldViolation {
  field: string;
  description: string;
}

export interface Problem {
  code: string;
  message: string;
  retryable: boolean;
  violations?: FieldViolation[];
}

export class APIError extends Error {
  readonly status: number;
  readonly code: string;
  readonly violations: FieldViolation[];

  constructor(status: number, problem: Problem) {
    super(problem.message);
    this.name = "APIError";
    this.status = status;
    this.code = problem.code;
    this.violations = problem.violations ?? [];
  }

  hasViolation(field: string): boolean {
    return this.violations.some((violation) => violation.field === field);
  }
}

export async function getSetupStatus(): Promise<SetupStatus> {
  return request("/api/v1/setup");
}

export async function initialize(username: string, password: string): Promise<SessionInfo> {
  return request("/api/v1/setup", { method: "POST", body: JSON.stringify({ username, password }) });
}

export async function login(username: string, password: string, secondFactor?: string): Promise<SessionInfo> {
  return request("/api/v1/auth/login", {
    method: "POST",
    body: JSON.stringify({ username, password, second_factor: secondFactor ?? "" }),
  });
}

export async function getSession(): Promise<SessionInfo> {
  return request("/api/v1/auth/session");
}

export async function logout(): Promise<void> {
  return request("/api/v1/auth/logout", { method: "POST" });
}

export async function updateAdministratorUsername(username: string, password: string, secondFactor: string): Promise<void> {
  return request("/api/v1/auth/username", {
    method: "PUT", body: JSON.stringify({ username, password, second_factor: secondFactor }),
  });
}

export async function updateAdministratorPassword(password: string, newPassword: string, secondFactor: string): Promise<void> {
  return request("/api/v1/auth/password", {
    method: "PUT", body: JSON.stringify({ password, new_password: newPassword, second_factor: secondFactor }),
  });
}

export async function listAdministratorSessions(): Promise<AdministratorSession[]> {
  const response = await request<{ items: AdministratorSession[] }>("/api/v1/auth/sessions");
  return response.items;
}

export async function revokeAdministratorSession(id: string): Promise<void> {
  return request(`/api/v1/auth/sessions/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function revokeOtherAdministratorSessions(): Promise<number> {
  const response = await request<{ revoked: number }>("/api/v1/auth/sessions/revoke-others", { method: "POST" });
  return response.revoked;
}

export async function prepareTOTP(): Promise<TOTPPreparation> {
  return request("/api/v1/auth/totp/prepare", { method: "POST" });
}

export async function enableTOTP(code: string): Promise<RecoveryCodes> {
  return request("/api/v1/auth/totp/enable", { method: "POST", body: JSON.stringify({ code }) });
}

export async function disableTOTP(password: string, secondFactor: string): Promise<void> {
  return request("/api/v1/auth/totp/disable", {
    method: "POST",
    body: JSON.stringify({ password, second_factor: secondFactor }),
  });
}

export async function regenerateRecoveryCodes(password: string, secondFactor: string): Promise<RecoveryCodes> {
  return request("/api/v1/auth/recovery-codes/regenerate", {
    method: "POST",
    body: JSON.stringify({ password, second_factor: secondFactor }),
  });
}

export async function getSystemSettings(): Promise<SystemSettings> {
  return request("/api/v1/settings");
}

export async function updateSystemSettings(input: Omit<SystemSettings, "updated_at">): Promise<SystemSettings> {
  return request("/api/v1/settings", { method: "PUT", body: JSON.stringify(input) });
}

export async function listNodes(): Promise<Node[]> {
  const response = await request<{ items: Node[] }>("/api/v1/nodes");
  return response.items;
}

export async function getNode(id: string): Promise<Node> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}`);
}

export async function createNode(input: NodeInput): Promise<Node> {
  return request("/api/v1/nodes", { method: "POST", body: JSON.stringify(input) });
}

export async function updateNode(id: string, input: NodeInput): Promise<Node> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(input) });
}

export async function deleteNode(id: string): Promise<void> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function revokeNodeCredential(id: string): Promise<Node> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}/agent-credential`, { method: "DELETE" });
}

export async function createNodeRegistrationToken(id: string): Promise<NodeRegistrationToken> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}/registration-tokens`, { method: "POST" });
}

export async function requestAgentUpdate(id: string, version: string): Promise<AgentUpdate> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}/agent-updates`, {
    method: "POST",
    body: JSON.stringify({ version }),
  });
}

export async function getLatestAgentUpdate(id: string): Promise<AgentUpdate | null> {
  try {
    return await request(`/api/v1/nodes/${encodeURIComponent(id)}/agent-updates/latest`);
  } catch (cause) {
    if (cause instanceof APIError && cause.status === 404) return null;
    throw cause;
  }
}

export async function getAgentUpdateAvailability(id: string): Promise<AgentUpdateAvailability> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}/agent-updates/availability`);
}

export async function requestLatestAgentUpdate(id: string): Promise<AgentUpdate> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}/agent-updates/latest`, { method: "POST" });
}

export async function listNodeCommands(id: string, limit = 50): Promise<NodeCommand[]> {
  const response = await request<{ items: NodeCommand[] }>(
    `/api/v1/nodes/${encodeURIComponent(id)}/commands?limit=${encodeURIComponent(String(limit))}`,
  );
  return response.items;
}

export async function listNodePluginInstances(): Promise<NodePluginInstance[]> {
  const response = await request<{ items: NodePluginInstance[] }>("/api/v1/node-plugin-instances");
  return response.items;
}

export async function reconcileNodePlugin(nodeID: string, pluginID: string, input: NodePluginInput): Promise<NodePluginInstance> {
  return request(`/api/v1/nodes/${encodeURIComponent(nodeID)}/plugins/${encodeURIComponent(pluginID)}`, {
    method: "PUT",
    body: JSON.stringify(input),
  });
}

export async function inspectPluginRelease(input: Omit<PluginReleaseInput, "approved_permissions">): Promise<PluginReleaseCandidate> {
  return request("/api/v1/plugins/inspect", { method: "POST", body: JSON.stringify(input) });
}

export async function listPluginInstallations(): Promise<PluginInstallation[]> {
  const response = await request<{ items: PluginInstallation[] }>("/api/v1/plugins");
  return response.items;
}

export async function installPlugin(input: PluginReleaseInput): Promise<PluginInstallation> {
  return request("/api/v1/plugins", { method: "POST", body: JSON.stringify(input) });
}

export async function upgradePlugin(pluginID: string, input: PluginUpgradeInput): Promise<PluginInstallation> {
  return request(`/api/v1/plugins/${encodeURIComponent(pluginID)}/upgrade`, {
    method: "POST",
    body: JSON.stringify(input),
  });
}

export async function replacePluginGitHubToken(pluginID: string, githubToken: string): Promise<void> {
  return request(`/api/v1/plugins/${encodeURIComponent(pluginID)}/github-token`, {
    method: "PUT",
    body: JSON.stringify({ github_token: githubToken }),
  });
}

export async function uninstallPlugin(pluginID: string): Promise<void> {
  return request(`/api/v1/plugins/${encodeURIComponent(pluginID)}`, { method: "DELETE" });
}

export async function createPluginUISession(pluginID: string): Promise<string> {
  const response = await request<{ url: string }>(`/api/v1/plugins/${encodeURIComponent(pluginID)}/ui-session`, {
    method: "POST",
  });
  return response.url;
}

export async function invokePluginUI<T>(pluginID: string, method: string, parameters: Record<string, unknown>): Promise<T> {
  return request(`/api/v1/plugins/${encodeURIComponent(pluginID)}/ui/${encodeURIComponent(method)}`, {
    method: "POST",
    body: JSON.stringify(parameters),
  });
}

export async function listUsers(): Promise<User[]> {
  const response = await request<{ items: User[] }>("/api/v1/users");
  return response.items;
}

export async function createUser(input: UserInput): Promise<User> {
  return request("/api/v1/users", { method: "POST", body: JSON.stringify(input) });
}

export async function updateUser(id: string, input: UserInput): Promise<User> {
  return request(`/api/v1/users/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(input) });
}

export async function deleteUser(id: string): Promise<void> {
  return request(`/api/v1/users/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function listAuthorizations(filters: { userID?: string; nodeID?: string } = {}): Promise<Authorization[]> {
  const query = new URLSearchParams();
  if (filters.userID) query.set("user_id", filters.userID);
  if (filters.nodeID) query.set("node_id", filters.nodeID);
  const suffix = query.size > 0 ? `?${query}` : "";
  const response = await request<{ items: Authorization[] }>(`/api/v1/authorizations${suffix}`);
  return response.items;
}

export async function listAccessEvents(filters: { nodeID?: string; beforeID?: number; limit?: number } = {}): Promise<AccessEvent[]> {
  const query = new URLSearchParams();
  if (filters.nodeID) query.set("node_id", filters.nodeID);
  if (filters.beforeID !== undefined) query.set("before_id", String(filters.beforeID));
  query.set("limit", String(filters.limit ?? 100));
  const response = await request<{ items: AccessEvent[] }>(`/api/v1/events/access?${query}`);
  return response.items;
}

export async function createAuthorization(input: AuthorizationInput): Promise<{ authorization: Authorization; subscription_token: string }> {
  return request("/api/v1/authorizations", { method: "POST", body: JSON.stringify(input) });
}

export async function updateAuthorization(id: string, input: AuthorizationInput): Promise<Authorization> {
  return request(`/api/v1/authorizations/${encodeURIComponent(id)}`, { method: "PUT", body: JSON.stringify(input) });
}

export async function deleteAuthorization(id: string): Promise<void> {
  return request(`/api/v1/authorizations/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function rotateSubscriptionToken(id: string): Promise<SubscriptionToken> {
  return request(`/api/v1/authorizations/${encodeURIComponent(id)}/subscription-token`, { method: "POST" });
}

export async function listServiceBindings(authorizationID: string): Promise<ServiceBinding[]> {
  const response = await request<{ items: ServiceBinding[] }>(
    `/api/v1/authorizations/${encodeURIComponent(authorizationID)}/service-bindings`,
  );
  return response.items;
}

export async function createServiceBinding(authorizationID: string, input: { plugin_id: string; service_id: string; enabled: boolean }): Promise<ServiceBinding> {
  return request(`/api/v1/authorizations/${encodeURIComponent(authorizationID)}/service-bindings`, {
    method: "POST", body: JSON.stringify(input),
  });
}

export async function updateServiceBinding(id: string, enabled: boolean): Promise<ServiceBinding> {
  return request(`/api/v1/service-bindings/${encodeURIComponent(id)}`, {
    method: "PUT", body: JSON.stringify({ enabled }),
  });
}

export async function deleteServiceBinding(id: string): Promise<void> {
  return request(`/api/v1/service-bindings/${encodeURIComponent(id)}`, { method: "DELETE" });
}

export async function listPluginServices(nodeID?: string): Promise<PluginService[]> {
  const query = nodeID ? `?node_id=${encodeURIComponent(nodeID)}` : "";
  const response = await request<{ items: PluginService[] }>(`/api/v1/plugin-services${query}`);
  return response.items;
}

export async function getAnnouncement(): Promise<string | null> {
  const response = await request<{ content: string | null }>("/api/v1/announcement");
  return response.content;
}

export async function updateAnnouncement(content: string): Promise<string | null> {
  const response = await request<{ content: string | null }>("/api/v1/announcement", {
    method: "PUT", body: JSON.stringify({ content }),
  });
  return response.content;
}

export async function listAudit(beforeID?: number, limit = 100): Promise<AuditEntry[]> {
  const query = new URLSearchParams({ limit: String(limit) });
  if (beforeID !== undefined) query.set("before_id", String(beforeID));
  const response = await request<{ items: AuditEntry[] }>(`/api/v1/audit?${query}`);
  return response.items;
}

export async function getSubscription(token: string): Promise<SubscriptionInfo> {
  return request(`/api/v1/subscriptions/${encodeURIComponent(token)}`);
}

async function request<T>(path: string, init: RequestInit = {}): Promise<T> {
  const headers = new Headers(init.headers);
  headers.set("Accept", "application/json");
  if (init.body !== undefined) {
    headers.set("Content-Type", "application/json");
  }
  if (init.method !== undefined && init.method !== "GET") {
    const csrf = readCookie("relayward_csrf");
    if (csrf !== undefined) {
      headers.set("X-CSRF-Token", csrf);
    }
  }

  const response = await fetch(path, { ...init, headers, credentials: "same-origin" });
  if (response.status === 204) {
    return undefined as T;
  }
  const contentType = response.headers.get("Content-Type") ?? "";
  const body: unknown = contentType.includes("application/json") ? await response.json() : undefined;
  if (!response.ok) {
    if (isProblem(body)) {
      throw new APIError(response.status, body);
    }
    throw new APIError(response.status, {
      code: "internal",
      message: "The server returned an invalid response.",
      retryable: response.status >= 500,
    });
  }
  return body as T;
}

function readCookie(name: string): string | undefined {
  if (typeof document === "undefined") {
    return undefined;
  }
  const prefix = `${name}=`;
  for (const part of document.cookie.split(";")) {
    const value = part.trim();
    if (value.startsWith(prefix)) {
      return decodeURIComponent(value.slice(prefix.length));
    }
  }
  return undefined;
}

function isProblem(value: unknown): value is Problem {
  if (typeof value !== "object" || value === null) {
    return false;
  }
  const record = value as Record<string, unknown>;
  return typeof record.code === "string" && typeof record.message === "string" && typeof record.retryable === "boolean";
}
