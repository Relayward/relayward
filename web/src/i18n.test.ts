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

  it("translates settings and subscription profile messages", () => {
    expect(translateMessage("zh-CN", "Settings saved.")).toBe("设置已保存。");
    expect(translateMessage("zh-CN", "Refresh every {count} hours", { count: 24 })).toBe("每 24 小时刷新");
  });

  it("translates kernel permission copy", () => {
    expect(translateMessage("zh-CN", "Read node authorizations")).toBe("读取节点授权");
    expect(translateMessage("zh-CN", "Configure managed node plugins")).toBe("配置受管节点插件");
    expect(translateMessage("zh-CN", "Read and publish this plugin's configuration on managed nodes."))
      .toBe("读取并下发此插件在受管节点上的配置。");
    expect(translateMessage("zh-CN", "Diagnose node ports")).toBe("诊断节点端口");
    expect(translateMessage("zh-CN", "Invoke node plugin diagnostics")).toBe("调用节点插件诊断");
  });
});
