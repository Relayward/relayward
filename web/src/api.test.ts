import { afterEach, describe, expect, it, vi } from "vitest";

import { APIError, getLatestAgentUpdate, reconcileNodePlugin, requestAgentUpdate, type AgentUpdate } from "./api";

afterEach(() => {
  vi.unstubAllGlobals();
});

describe("APIError", () => {
  it("exposes field violations without parsing message text", () => {
    const error = new APIError(401, {
      code: "unauthenticated",
      message: "A second factor is required.",
      retryable: false,
      violations: [{ field: "second_factor", description: "required" }],
    });
    expect(error.hasViolation("second_factor")).toBe(true);
    expect(error.hasViolation("password")).toBe(false);
  });
});

describe("Agent update API", () => {
  it("queues a version with the CSRF token", async () => {
    const update = agentUpdate();
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () => jsonResponse(update, 202),
    );
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(requestAgentUpdate("node/id", "0.2.0")).resolves.toEqual(update);
    const [path, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init?.headers);
    expect(path).toBe("/api/v1/nodes/node%2Fid/agent-updates");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(JSON.stringify({ version: "0.2.0" }));
    expect(headers.get("X-CSRF-Token")).toBe("csrf-token");
  });

  it("treats a missing latest command as no update history", async () => {
    vi.stubGlobal("fetch", vi.fn(async () => jsonResponse({
      code: "not_found",
      message: "Agent update not found.",
      retryable: false,
    }, 404)));

    await expect(getLatestAgentUpdate("node-id")).resolves.toBeNull();
  });
});

describe("Node plugin API", () => {
  it("queues a desired state without inventing a configuration override", async () => {
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () => jsonResponse({}, 200),
    );
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await reconcileNodePlugin("node/id", "io.relayward/plugin", {
      desired_state: "stopped",
      version: "1.2.3",
    });
    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe("/api/v1/nodes/node%2Fid/plugins/io.relayward%2Fplugin");
    expect(init?.method).toBe("PUT");
    expect(init?.body).toBe(JSON.stringify({ desired_state: "stopped", version: "1.2.3" }));
  });
});

function agentUpdate(): AgentUpdate {
  return {
    id: "update-id",
    node_id: "node-id",
    version: "0.2.0",
    status: "pending",
    attempts: 0,
    last_sent_at: null,
    completed_at: null,
    expires_at: "2026-08-02T12:30:00Z",
    created_at: "2026-08-02T12:00:00Z",
    updated_at: "2026-08-02T12:00:00Z",
  };
}

function jsonResponse(body: unknown, status: number): Response {
  return new Response(JSON.stringify(body), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
