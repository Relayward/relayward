import { describe, expect, it } from "vitest";

import { loadSystemInfo } from "./system";

describe("loadSystemInfo", () => {
  it("returns a validated response", async () => {
    const fetcher: typeof fetch = async () =>
      new Response(JSON.stringify({ name: "relayward", version: "dev" }), {
        status: 200,
        headers: { "Content-Type": "application/json" },
      });

    await expect(loadSystemInfo(undefined, fetcher)).resolves.toEqual({
      name: "relayward",
      version: "dev",
    });
  });

  it("rejects a misleading success body", async () => {
    const fetcher: typeof fetch = async () =>
      new Response(JSON.stringify({ status: "ready" }), { status: 200 });

    await expect(loadSystemInfo(undefined, fetcher)).rejects.toThrow("invalid");
  });
});
