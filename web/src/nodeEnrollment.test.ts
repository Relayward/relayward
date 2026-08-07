import { describe, expect, it } from "vitest";

import { buildAgentInstallCommand } from "./nodeEnrollment";

describe("Agent enrollment command", () => {
  it("uses the configured HTTPS public URL", () => {
    const result = buildAgentInstallCommand("rwr_example", "https://panel.example.com", "https://ignored.example.com");

    expect(result).toEqual({
      command: "curl --proto '=https' --tlsv1.2 -fsSL 'https://github.com/Relayward/relayward-agent/releases/latest/download/install.sh' | env RELAYWARD_REGISTRATION_TOKEN='rwr_example' sh -s -- --server-url 'https://panel.example.com'",
      serverURL: "https://panel.example.com",
      insecure: false,
    });
  });

  it("falls back to the current HTTPS browser origin", () => {
    const result = buildAgentInstallCommand("rwr_example", "", "https://panel.example.com:8443");

    expect(result.serverURL).toBe("https://panel.example.com:8443");
    expect(result.command).toContain("--server-url 'https://panel.example.com:8443'");
    expect(result.command).not.toContain("--allow-insecure");
  });

  it("adds explicit insecure transport permission for an HTTP center", () => {
    const result = buildAgentInstallCommand("rwr_example", "http://192.0.2.10:8080", "https://ignored.example.com");

    expect(result.insecure).toBe(true);
    expect(result.command).toContain("--server-url 'http://192.0.2.10:8080' --allow-insecure");
  });

  it("rejects unsupported URL schemes", () => {
    expect(() => buildAgentInstallCommand("rwr_example", "ftp://panel.example.com", ""))
      .toThrow("The Agent server URL must use HTTP or HTTPS.");
  });

  it("rejects a public URL with a path", () => {
    expect(() => buildAgentInstallCommand("rwr_example", "https://panel.example.com/admin", ""))
      .toThrow("The Agent server URL must be an origin without credentials, path, query, or fragment.");
  });

  it("quotes opaque registration tokens", () => {
    const result = buildAgentInstallCommand("token'$(touch /tmp/nope)", "https://panel.example.com", "");

    expect(result.command).toContain("RELAYWARD_REGISTRATION_TOKEN='token'\"'\"'$(touch /tmp/nope)'");
  });
});
