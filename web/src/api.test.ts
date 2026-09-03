import { afterEach, describe, expect, it, vi } from "vitest";

import {
  APIError,
  createDDNSRecord,
  createDNSProviderConnection,
  createNodeEndpoint,
  createPluginUISession,
  getAgentUpdateAvailability,
  getSystemSettings,
  getLatestAgentUpdate,
  getNode,
  listAdministratorSessions,
  listDDNSRecords,
  listNodeCommands,
  listNodeEndpoints,
  listPluginReleaseVersions,
  reconcileNodePlugin,
  requestAgentUpdate,
  requestLatestAgentUpdate,
  revokeAdministratorSession,
  revokeNodeCredential,
  updateAdministratorUsername,
  updateSystemSettings,
  type AgentUpdate,
  type SystemSettings,
} from "./api";

const settings: SystemSettings = {
  session_lifetime_minutes: 1440,
  timezone: "Asia/Shanghai",
  public_url: "https://panel.example.com",
  subscription_title: "Relayward Home",
  support_url: "https://support.example.com",
  profile_url: "https://example.com/account",
  subscription_refresh_hours: 12,
  updated_at: "2026-08-05T00:00:00Z",
};

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

  it("loads an encoded node and its official update availability", async () => {
    const node = { id: "node/id", agent_version: "0.1.0" };
    const availability = {
      current_version: "0.1.0",
      latest_release: {
        version: "0.2.0",
        tag: "v0.2.0",
        published_at: "2026-08-06T08:00:00Z",
        checked_at: "2026-08-06T08:01:00Z",
      },
      relation: "available",
    };
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse(node))
      .mockResolvedValueOnce(jsonResponse(availability));
    vi.stubGlobal("fetch", fetchMock);

    await expect(getNode("node/id")).resolves.toEqual(node);
    await expect(getAgentUpdateAvailability("node/id")).resolves.toEqual(availability);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/nodes/node%2Fid");
    expect(fetchMock.mock.calls[1][0]).toBe("/api/v1/nodes/node%2Fid/agent-updates/availability");
  });

  it("queues the latest official version with the CSRF token", async () => {
    const value = agentUpdate();
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(value, 202));
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(requestLatestAgentUpdate("node/id")).resolves.toEqual(value);
    const [path, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init?.headers);
    expect(path).toBe("/api/v1/nodes/node%2Fid/agent-updates/latest");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBeUndefined();
    expect(headers.get("X-CSRF-Token")).toBe("csrf-token");
  });
});

describe("Node command API", () => {
  it("loads bounded execution history for an encoded node ID", async () => {
    const commands = [{ id: "command-1", kind: "plugin.reconcile", status: "succeeded" }];
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({ items: commands }));
    vi.stubGlobal("fetch", fetchMock);

    await expect(listNodeCommands("node/id", 25)).resolves.toEqual(commands);
    expect(fetchMock).toHaveBeenCalledWith(
      "/api/v1/nodes/node%2Fid/commands?limit=25",
      expect.objectContaining({ credentials: "same-origin" }),
    );
  });
});

describe("Node credential API", () => {
  it("revokes the credential with the CSRF token", async () => {
    const node = { id: "node/id", registered_at: null };
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () => jsonResponse(node, 200),
    );
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(revokeNodeCredential("node/id")).resolves.toEqual(node);
    const [path, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init?.headers);
    expect(path).toBe("/api/v1/nodes/node%2Fid/agent-credential");
    expect(init?.method).toBe("DELETE");
    expect(headers.get("X-CSRF-Token")).toBe("csrf-token");
  });
});

describe("Node endpoint API", () => {
  it("loads endpoints for an encoded node and creates an endpoint with CSRF protection", async () => {
    const endpoint = { id: "endpoint-1", node_id: "node/id", display_name: "IPv4", kind: "direct" };
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({ items: [endpoint] }))
      .mockResolvedValueOnce(jsonResponse(endpoint, 201));
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(listNodeEndpoints("node/id")).resolves.toEqual([endpoint]);
    const input = {
      display_name: "IPv4", kind: "direct" as const, enabled: true, source_family: "ipv4" as const,
      address: "", public_port_overrides: { "io.relayward.test": { "vless-main": 45142 } },
    };
    await expect(createNodeEndpoint("node/id", input)).resolves.toEqual(endpoint);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/nodes/node%2Fid/endpoints");
    const [path, init] = fetchMock.mock.calls[1];
    expect(path).toBe("/api/v1/nodes/node%2Fid/endpoints");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(JSON.stringify(input));
    expect(new Headers(init?.headers).get("X-CSRF-Token")).toBe("csrf-token");
  });

  it("sends a Cloudflare token only in the request body", async () => {
    const connection = { id: "connection-1", name: "Cloudflare", provider: "cloudflare", has_token: true };
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse(connection, 201));
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(createDNSProviderConnection({ name: "Cloudflare", provider: "cloudflare", api_token: "secret-token" }))
      .resolves.toEqual(connection);
    const [path, init] = fetchMock.mock.calls[0];
    expect(path).toBe("/api/v1/dns-provider-connections");
    expect(String(path)).not.toContain("secret-token");
    expect(init?.body).toBe(JSON.stringify({ name: "Cloudflare", provider: "cloudflare", api_token: "secret-token" }));
  });

  it("manages DDNS records through the global resource API", async () => {
    const record = { id: "record/id", node_id: "node-1", node_name: "Edge", kind: "managed_ddns" };
    const fetchMock = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({ items: [record] }))
      .mockResolvedValueOnce(jsonResponse(record, 201));
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(listDDNSRecords()).resolves.toEqual([record]);
    const input = {
      node_id: "node-1", display_name: "IPv4", enabled: true, source_family: "ipv4" as const,
      public_port_overrides: {}, dns_provider_connection_id: "connection-1", zone_name: "example.com",
      record_name: "edge.example.com", ttl: 300, proxied: false,
    };
    await expect(createDDNSRecord(input)).resolves.toEqual(record);
    expect(fetchMock.mock.calls[0][0]).toBe("/api/v1/ddns-records");
    const [path, init] = fetchMock.mock.calls[1];
    expect(path).toBe("/api/v1/ddns-records");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(JSON.stringify(input));
    expect(new Headers(init?.headers).get("X-CSRF-Token")).toBe("csrf-token");
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

describe("Plugin UI API", () => {
  it("creates a sandbox asset session with a safely encoded plugin ID", async () => {
    const fetchMock = vi.fn<(input: RequestInfo | URL, init?: RequestInit) => Promise<Response>>(
      async () => jsonResponse({ url: "/plugin-ui/token/index.html" }, 201),
    );
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(createPluginUISession("io.relayward/plugin")).resolves.toBe("/plugin-ui/token/index.html");
    const [path, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init?.headers);
    expect(path).toBe("/api/v1/plugins/io.relayward%2Fplugin/ui-session");
    expect(init?.method).toBe("POST");
    expect(headers.get("X-CSRF-Token")).toBe("csrf-token");
  });
});

describe("Plugin release API", () => {
  it("lists repository versions without placing the GitHub token in the URL", async () => {
    const versions = [{ tag: "v1.2.3", version: "1.2.3", published_at: "2026-08-10T00:00:00Z" }];
    const fetchMock = vi.fn<typeof fetch>(async () => jsonResponse({ items: versions }));
    vi.stubGlobal("fetch", fetchMock);
    vi.stubGlobal("document", { cookie: "relayward_csrf=csrf-token" });

    await expect(listPluginReleaseVersions({
      repository: "https://github.com/Relayward/plugin",
      github_token: "private-token",
    })).resolves.toEqual(versions);
    const [path, init] = fetchMock.mock.calls[0];
    const headers = new Headers(init?.headers);
    expect(path).toBe("/api/v1/plugins/releases");
    expect(init?.method).toBe("POST");
    expect(init?.body).toBe(JSON.stringify({
      repository: "https://github.com/Relayward/plugin",
      github_token: "private-token",
    }));
    expect(headers.get("X-CSRF-Token")).toBe("csrf-token");
  });
});

describe("settings API", () => {
  it("loads the system settings", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => jsonResponse(settings));
    vi.stubGlobal("fetch", fetcher);

    await expect(getSystemSettings()).resolves.toEqual(settings);
    expect(fetcher).toHaveBeenCalledWith("/api/v1/settings", expect.objectContaining({ credentials: "same-origin" }));
  });

  it("updates every editable setting", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => jsonResponse(settings));
    vi.stubGlobal("fetch", fetcher);
    const { updated_at: _, ...input } = settings;

    await expect(updateSystemSettings(input)).resolves.toEqual(settings);
    const [, request] = fetcher.mock.calls[0];
    expect(request).toEqual(expect.objectContaining({ method: "PUT", body: JSON.stringify(input) }));
  });
});

describe("administrator API", () => {
  it("sends sensitive username updates without placing credentials in the URL", async () => {
    const fetcher = vi.fn<typeof fetch>(async () => new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetcher);

    await updateAdministratorUsername("operator", "current password", "123456");
    const [path, request] = fetcher.mock.calls[0];
    expect(path).toBe("/api/v1/auth/username");
    expect(request).toEqual(expect.objectContaining({
      method: "PUT",
      body: JSON.stringify({ username: "operator", password: "current password", second_factor: "123456" }),
    }));
  });

  it("lists sessions and safely encodes the revoked session ID", async () => {
    const sessions = [{
      id: "session/id",
      user_agent: "Browser",
      current: false,
      created_at: "2026-08-05T00:00:00Z",
      last_seen_at: "2026-08-05T00:01:00Z",
      expires_at: "2026-08-06T00:00:00Z",
    }];
    const fetcher = vi.fn<typeof fetch>()
      .mockResolvedValueOnce(jsonResponse({ items: sessions }))
      .mockResolvedValueOnce(new Response(null, { status: 204 }));
    vi.stubGlobal("fetch", fetcher);

    await expect(listAdministratorSessions()).resolves.toEqual(sessions);
    await revokeAdministratorSession("session/id");
    expect(fetcher.mock.calls[1][0]).toBe("/api/v1/auth/sessions/session%2Fid");
    expect(fetcher.mock.calls[1][1]).toEqual(expect.objectContaining({ method: "DELETE" }));
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

function jsonResponse(value: unknown, status = 200): Response {
  return new Response(JSON.stringify(value), {
    status,
    headers: { "Content-Type": "application/json" },
  });
}
