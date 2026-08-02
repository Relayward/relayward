import { describe, expect, it } from "vitest";

import { APIError } from "./api";
import { bridgeFailure, bridgeSuccess, parsePluginUIRequest, UI_BRIDGE_API_VERSION } from "./pluginBridge";

describe("plugin UI host bridge", () => {
  it("accepts only versioned bounded plugin requests", () => {
    const request = {
      api_version: UI_BRIDGE_API_VERSION,
      direction: "plugin-to-host",
      id: "ui-1",
      method: "rpc",
      payload: { method: "nodes.summary", parameters: {} },
    };
    expect(parsePluginUIRequest(request)).toEqual(request);
    expect(parsePluginUIRequest({ ...request, direction: "host-to-plugin" })).toBeUndefined();
    expect(parsePluginUIRequest({ ...request, method: "../admin" })).toBeUndefined();
    expect(parsePluginUIRequest({ ...request, id: "x".repeat(161) })).toBeUndefined();
  });

  it("returns correlated results and bounded API problems", () => {
    expect(bridgeSuccess("ui-1", { count: 2 })).toMatchObject({ id: "ui-1", ok: true, result: { count: 2 } });
    expect(bridgeFailure("ui-2", new APIError(503, {
      code: "unavailable", message: "Plugin unavailable.", retryable: true,
    }))).toMatchObject({ id: "ui-2", ok: false, problem: { code: "unavailable", retryable: true } });
  });
});
