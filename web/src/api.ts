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
