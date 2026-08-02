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

export interface Node {
  id: string;
  name: string;
  public_address: string;
  enabled: boolean;
  registered_at: string | null;
  last_seen_at: string | null;
  created_at: string;
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
  created_at: string;
  updated_at: string;
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
  status: "active" | "disabled" | "expired" | "node_disabled";
  user_name: string;
  node_name: string;
  node_address: string;
  traffic_limit_bytes: number | null;
  traffic_used_bytes: number | null;
  reset: ResetRule;
  expires_at: string | null;
  services: unknown[];
  announcement: string | null;
}

interface FieldViolation {
  field: string;
  description: string;
}

interface Problem {
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

export async function listNodes(): Promise<Node[]> {
  const response = await request<{ items: Node[] }>("/api/v1/nodes");
  return response.items;
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

export async function createNodeRegistrationToken(id: string): Promise<NodeRegistrationToken> {
  return request(`/api/v1/nodes/${encodeURIComponent(id)}/registration-tokens`, { method: "POST" });
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
