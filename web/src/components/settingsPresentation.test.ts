import { describe, expect, it } from "vitest";

import { buildSubscriptionLink } from "./AuthorizationView";
import { defaultPublicURL } from "./SettingsView";
import { refreshLabel } from "./SubscriptionPage";

describe("settings presentation", () => {
  it("uses the configured public origin for subscription links", () => {
    expect(buildSubscriptionLink("token/value", "https://panel.example.com", "https://internal.example.net"))
      .toBe("https://panel.example.com/s/token%2Fvalue");
  });

  it("falls back to the browser origin when no public URL is configured", () => {
    expect(buildSubscriptionLink("token", "", "https://internal.example.net"))
      .toBe("https://internal.example.net/s/token");
  });

  it("prefills an empty Public URL without replacing a configured value", () => {
    expect(defaultPublicURL("", "http://192.0.2.10:8080")).toBe("http://192.0.2.10:8080");
    expect(defaultPublicURL("https://panel.example.com", "http://192.0.2.10:8080"))
      .toBe("https://panel.example.com");
  });

  it("formats subscription refresh hints", () => {
    const translate = (message: string, values?: Record<string, string | number>) => (
      message.replace("{count}", String(values?.count ?? ""))
    );
    expect(refreshLabel(1, translate)).toBe("Refresh every hour");
    expect(refreshLabel(24, translate)).toBe("Refresh every 24 hours");
  });
});
