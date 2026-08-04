import { describe, expect, it } from "vitest";

import { resolveLocale, translateMessage } from "./i18n";

describe("locale resolution", () => {
  it("defaults to Simplified Chinese", () => {
    expect(resolveLocale(undefined)).toBe("zh-CN");
    expect(resolveLocale(null)).toBe("zh-CN");
    expect(resolveLocale("unsupported")).toBe("zh-CN");
  });

  it("keeps an explicit English selection", () => {
    expect(resolveLocale("en")).toBe("en");
  });
});

describe("message translation", () => {
  it("translates known messages to Simplified Chinese", () => {
    expect(translateMessage("zh-CN", "Nodes")).toBe("节点");
  });

  it("uses the source message for English", () => {
    expect(translateMessage("en", "Nodes")).toBe("Nodes");
  });

  it("interpolates translated messages", () => {
    expect(translateMessage("zh-CN", "{plugin} on {node}", { plugin: "Xray", node: "Tokyo" }))
      .toBe("Tokyo 上的 Xray");
  });

  it("falls back to unknown source messages", () => {
    expect(translateMessage("zh-CN", "Plugin-provided message")).toBe("Plugin-provided message");
  });
});
