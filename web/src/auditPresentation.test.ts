import { describe, expect, it } from "vitest";

import {
  auditActionLabel,
  auditActorLabel,
  auditOutcomeLabel,
  auditTargetTypeLabel,
  shortAuditID,
} from "./auditPresentation";
import { translateMessage } from "./i18n";

const zhCN = (message: string, values?: Record<string, string | number>) => translateMessage("zh-CN", message, values);

describe("audit presentation", () => {
  it("localizes current audit actions", () => {
    expect(auditActionLabel("plugin.github_token.replace", zhCN)).toBe("插件 GitHub Token 已更新");
    expect(auditActionLabel("node.agent_update.complete", zhCN)).toBe("Agent 更新已完成");
    expect(auditActionLabel("service_binding.delete", zhCN)).toBe("服务绑定已删除");
  });

  it("localizes actors, target types, and outcomes", () => {
    expect(auditActorLabel("local_admin", zhCN)).toBe("本地管理员");
    expect(auditTargetTypeLabel("plugin_installation", zhCN)).toBe("插件安装");
    expect(auditOutcomeLabel("success", zhCN)).toBe("成功");
  });

  it("humanizes unknown identifiers without hiding them", () => {
    expect(auditActionLabel("future.action_added", zhCN)).toBe("Future action added");
    expect(auditTargetTypeLabel("future_target", zhCN)).toBe("Future target");
  });

  it("shortens long identifiers", () => {
    expect(shortAuditID("0123456789abcdef")).toBe("01234567...");
    expect(shortAuditID("short-id")).toBe("short-id");
  });
});
