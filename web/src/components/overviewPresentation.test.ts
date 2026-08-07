import { describe, expect, it } from "vitest";

import type { Authorization, Node, NodePluginInstance, User } from "../api";
import {
  buildAuthorizationRisks,
  buildNames,
  buildOperationalIssues,
  instanceHealthy,
  type OverviewData,
} from "./OverviewView";

const now = new Date("2026-08-05T12:00:00Z");
const translate = (message: string, values?: Record<string, string | number>) => (
  Object.entries(values ?? {}).reduce((result, [key, value]) => result.replaceAll(`{${key}}`, String(value)), message)
);

describe("overview presentation", () => {
  it("treats a converged stopped runtime as healthy", () => {
    expect(instanceHealthy(pluginInstance({
      desired_state: "stopped",
      actual_state: "stopped",
      health: "unknown",
      reconcile_status: "succeeded",
      command_status: "succeeded",
    }))).toBe(true);
  });

  it("only reports a pending runtime after the convergence grace period", () => {
    const recent = overview({ instances: [pluginInstance({ updated_at: "2026-08-05T11:59:00Z" })] });
    const stale = overview({ instances: [pluginInstance({ updated_at: "2026-08-05T11:50:00Z" })] });

    expect(buildOperationalIssues(recent, true, now, translate, String).some((issue) => issue.key.includes("pending"))).toBe(false);
    expect(buildOperationalIssues(stale, true, now, translate, String).some((issue) => issue.key.includes("pending"))).toBe(true);
  });

  it("reports only the highest-priority risk for each authorization", () => {
    const data = overview({
      authorizations: [authorization({
        expires_at: "2026-08-04T00:00:00Z",
        traffic_limit_bytes: 100,
        current_traffic: {
          period: { id: "period", starts_at: "2026-08-01T00:00:00Z", ends_at: null },
          revision: 1,
          upload_bytes: 80,
          download_bytes: 30,
          observed_at: "2026-08-05T11:59:00Z",
        },
      })],
    });

    const risks = buildAuthorizationRisks(data, now, buildNames(data), translate);
    expect(risks).toHaveLength(1);
    expect(risks[0]?.title).toBe("Traffic quota exceeded");
  });
});

function overview(overrides: Partial<OverviewData> = {}): OverviewData {
  return {
    nodes: [node()],
    users: [user()],
    authorizations: [],
    instances: [],
    audit: [],
    ...overrides,
  };
}

function node(overrides: Partial<Node> = {}): Node {
  return {
    id: "node-1",
    name: "Edge",
    enabled: true,
    agent_status: "online",
    hostname: "edge",
    agent_version: "0.1.0",
    agent_os: "linux",
    agent_arch: "amd64",
    capabilities: [],
    agent_started_at: "2026-08-05T10:00:00Z",
    policy: null,
    registered_at: "2026-08-05T10:00:00Z",
    last_seen_at: "2026-08-05T11:59:50Z",
    created_at: "2026-08-05T10:00:00Z",
    updated_at: "2026-08-05T11:59:50Z",
    ...overrides,
  };
}

function user(overrides: Partial<User> = {}): User {
  return {
    id: "user-1",
    display_name: "Alice",
    email: null,
    telegram: null,
    note: "",
    created_at: "2026-08-05T10:00:00Z",
    updated_at: "2026-08-05T10:00:00Z",
    ...overrides,
  };
}

function pluginInstance(overrides: Partial<NodePluginInstance> = {}): NodePluginInstance {
  return {
    node_id: "node-1",
    node_name: "Edge",
    plugin_id: "io.relayward.runtime",
    plugin_name: "Runtime",
    desired_version: "1.0.0",
    active_version: "1.0.0",
    desired_state: "running",
    actual_state: "running",
    generation: 2,
    desired_configuration_sha256: "a".repeat(64),
    artifact_size: 1,
    artifact_sha256: "b".repeat(64),
    actual_generation: 2,
    actual_configuration_sha256: "a".repeat(64),
    health: "healthy",
    reason: "",
    restart_count: 0,
    capabilities: [],
    reconcile_status: "pending",
    last_command_id: "command-1",
    command_status: "pending",
    command_attempts: 1,
    command_last_sent_at: null,
    command_completed_at: null,
    actual_observed_at: "2026-08-05T11:59:00Z",
    updated_at: "2026-08-05T11:59:00Z",
    ...overrides,
  };
}

function authorization(overrides: Partial<Authorization> = {}): Authorization {
  return {
    id: "authorization-1",
    user_id: "user-1",
    node_id: "node-1",
    enabled: true,
    traffic_limit_bytes: null,
    reset: { kind: "monthly", value: 1, timezone: "UTC", period_anchor: null },
    expires_at: null,
    soft_ip_limit: null,
    activity_window_seconds: 600,
    block_duration_seconds: 1800,
    current_traffic: null,
    enforcement: null,
    created_at: "2026-08-05T10:00:00Z",
    updated_at: "2026-08-05T10:00:00Z",
    ...overrides,
  };
}
