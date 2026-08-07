const agentInstallScriptURL = "https://github.com/Relayward/relayward-agent/releases/latest/download/install.sh";

export interface AgentInstallCommand {
  command: string;
  serverURL: string;
  insecure: boolean;
}

export function buildAgentInstallCommand(
  token: string,
  publicURL: string,
  currentOrigin: string,
): AgentInstallCommand {
  const target = resolveAgentServerURL(publicURL || currentOrigin);
  const insecureFlag = target.insecure ? " --allow-insecure" : "";
  return {
    command: `curl --proto '=https' --tlsv1.2 -fsSL ${shellQuote(agentInstallScriptURL)} | env RELAYWARD_REGISTRATION_TOKEN=${shellQuote(token)} sh -s -- --server-url ${shellQuote(target.serverURL)}${insecureFlag}`,
    ...target,
  };
}

function resolveAgentServerURL(value: string): Pick<AgentInstallCommand, "serverURL" | "insecure"> {
  let parsed: URL;
  try {
    parsed = new URL(value);
  } catch {
    throw new Error("The Agent server URL is invalid.");
  }
  if (parsed.username || parsed.password || parsed.pathname !== "/" || parsed.search || parsed.hash) {
    throw new Error("The Agent server URL must be an origin without credentials, path, query, or fragment.");
  }
  if (parsed.protocol !== "http:" && parsed.protocol !== "https:") {
    throw new Error("The Agent server URL must use HTTP or HTTPS.");
  }
  return { serverURL: parsed.origin, insecure: parsed.protocol === "http:" };
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}
