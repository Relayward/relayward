import { APIError, type Problem } from "./api";

export const UI_BRIDGE_API_VERSION = "relayward.plugin-ui/v1" as const;

export type PluginBridgeMethod = "context" | "rpc" | "navigate" | "confirm";

export interface PluginUIRequest {
  api_version: typeof UI_BRIDGE_API_VERSION;
  direction: "plugin-to-host";
  id: string;
  method: PluginBridgeMethod;
  payload: unknown;
}

export interface PluginUIResponse {
  api_version: typeof UI_BRIDGE_API_VERSION;
  direction: "host-to-plugin";
  id: string;
  ok: boolean;
  result?: unknown;
  problem?: Problem;
}

export function parsePluginUIRequest(value: unknown): PluginUIRequest | undefined {
  if (!isRecord(value) || value.api_version !== UI_BRIDGE_API_VERSION || value.direction !== "plugin-to-host" ||
      typeof value.id !== "string" || value.id.length === 0 || value.id.length > 160 ||
      !isBridgeMethod(value.method) || !("payload" in value)) {
    return undefined;
  }
  return value as unknown as PluginUIRequest;
}

export function bridgeSuccess(id: string, result: unknown): PluginUIResponse {
  return { api_version: UI_BRIDGE_API_VERSION, direction: "host-to-plugin", id, ok: true, result };
}

export function bridgeFailure(id: string, cause: unknown): PluginUIResponse {
  const problem: Problem = cause instanceof APIError
    ? { code: cause.code, message: cause.message, retryable: cause.status >= 500, violations: cause.violations }
    : { code: "internal", message: "The plugin request could not be completed.", retryable: false };
  return { api_version: UI_BRIDGE_API_VERSION, direction: "host-to-plugin", id, ok: false, problem };
}

export function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === "object" && value !== null && !Array.isArray(value);
}

function isBridgeMethod(value: unknown): value is PluginBridgeMethod {
  return value === "context" || value === "rpc" || value === "navigate" || value === "confirm";
}
