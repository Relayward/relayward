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
  if (parsed.protocol === "https:") {
    return { serverURL: parsed.origin, insecure: false };
  }
  if (parsed.protocol === "http:" && isLoopback(parsed.hostname)) {
    return { serverURL: parsed.origin, insecure: true };
  }
  throw new Error("Set an HTTPS Public URL before registering an Agent.");
}

function isLoopback(hostname: string): boolean {
  const normalized = hostname.toLowerCase();
  return normalized === "localhost" || normalized === "127.0.0.1" || normalized === "[::1]";
}

function shellQuote(value: string): string {
  return `'${value.replaceAll("'", `'"'"'`)}'`;
}
