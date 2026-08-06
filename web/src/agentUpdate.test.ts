import { describe, expect, it } from "vitest";

import type { AgentUpdate } from "./api";
import { agentUpdatePresentation } from "./agentUpdate";

describe("agentUpdatePresentation", () => {
  it("distinguishes queued, processing, and reconnecting commands", () => {
    expect(agentUpdatePresentation(update({ attempts: 0 }), "offline").label).toBe("Waiting for delivery");
    expect(agentUpdatePresentation(update({ attempts: 1 }), "online").label).toBe("Agent is processing the update");
    expect(agentUpdatePresentation(update({ attempts: 1 }), "offline").label).toBe("Waiting for Agent to reconnect");
  });

  it("distinguishes success, rollback, failure, and expiry", () => {
    expect(agentUpdatePresentation(update({ status: "succeeded" }), "online").label).toBe("Update succeeded");
    expect(agentUpdatePresentation(update({
      status: "failed",
      problem: { code: "unavailable", message: "Agent update failed health validation and was rolled back", retryable: false },
    }), "online").label).toBe("Update failed and was rolled back");
    expect(agentUpdatePresentation(update({
      status: "failed",
      problem: { code: "unavailable", message: "Release unavailable", retryable: true },
    }), "online").label).toBe("Update failed");
    expect(agentUpdatePresentation(update({ status: "expired" }), "offline").label).toBe("Update expired");
  });
});

function update(overrides: Partial<AgentUpdate>): AgentUpdate {
  return {
    id: "update-id",
    node_id: "node-id",
    version: "0.2.0",
    status: "pending",
    attempts: 1,
    last_sent_at: "2026-08-06T08:00:00Z",
    completed_at: null,
    expires_at: "2026-08-06T08:30:00Z",
    created_at: "2026-08-06T08:00:00Z",
    updated_at: "2026-08-06T08:00:00Z",
    ...overrides,
  };
}
