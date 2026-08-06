import type { AgentUpdate, Node } from "./api";

export interface AgentUpdatePresentation {
  label: string;
  detail?: string;
  tone: "success" | "warning" | "danger" | "muted";
  active: boolean;
}

export function agentUpdatePresentation(
  update: AgentUpdate | null | undefined,
  agentStatus: Node["agent_status"],
): AgentUpdatePresentation {
  if (!update) {
    return { label: "No update history", tone: "muted", active: false };
  }
  if (update.status === "pending" && update.attempts === 0) {
    return {
      label: "Waiting for delivery",
      detail: "The update is queued until the Agent connects.",
      tone: "warning",
      active: true,
    };
  }
  if (update.status === "pending" && agentStatus !== "online") {
    return {
      label: "Waiting for Agent to reconnect",
      detail: "The update was delivered; waiting for the Agent to reconnect and report the result.",
      tone: "warning",
      active: true,
    };
  }
  if (update.status === "pending") {
    return {
      label: "Agent is processing the update",
      detail: "The update was delivered and has not reported a final result yet.",
      tone: "warning",
      active: true,
    };
  }
  if (update.status === "succeeded") {
    return { label: "Update succeeded", tone: "success", active: false };
  }
  if (update.status === "failed" && update.problem?.message.toLocaleLowerCase().includes("rolled back")) {
    return {
      label: "Update failed and was rolled back",
      detail: update.problem.message,
      tone: "danger",
      active: false,
    };
  }
  if (update.status === "failed") {
    return {
      label: "Update failed",
      detail: update.problem?.message,
      tone: "danger",
      active: false,
    };
  }
  return { label: "Update expired", tone: "muted", active: false };
}
