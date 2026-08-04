import { createContext, useCallback, useContext, useEffect, useMemo, useState, type ReactNode } from "react";
import { Languages } from "lucide-react";

export type Locale = "zh-CN" | "en";

type Values = Record<string, string | number>;
type Translate = (message: string, values?: Values) => string;

interface I18nValue {
  locale: Locale;
  setLocale: (locale: Locale) => void;
  t: Translate;
  formatDateTime: (value: string | Date) => string;
  formatDate: (value: string | Date) => string;
}

const localeStorageKey = "relayward.locale";

const zhCN: Record<string, string> = {
  "Language": "语言",
  "Simplified Chinese": "简体中文",
  "English": "English",
  "Initialize Relayward": "初始化 Relayward",
  "Username": "用户名",
  "Password": "密码",
  "Confirm password": "确认密码",
  "Passwords do not match.": "两次输入的密码不一致。",
  "Creating...": "正在创建...",
  "Create administrator": "创建管理员",
  "Sign in": "登录",
  "Authentication or recovery code": "验证码或恢复码",
  "Signing in...": "正在登录...",
  "Relayward unavailable": "Relayward 暂不可用",
  "Retry": "重试",
  "The control plane could not be reached.": "无法连接到控制中心。",
  "The server returned an invalid response.": "服务器返回了无效响应。",
  "The request could not be completed.": "请求未能完成。",
  "Administration": "管理后台",
  "System": "系统",
  "Nodes": "节点",
  "Plugins": "插件",
  "Users": "用户",
  "Authorizations": "授权",
  "Recent events": "最近事件",
  "Announcement": "公告",
  "Audit": "审计",
  "Security": "安全",
  "Sign out": "退出登录",
  "Subscriptions": "订阅",
  "Saving...": "正在保存...",
  "Save": "保存",
  "Content": "内容",
  "Saved.": "已保存。",
  "Overview": "概览",
  "Control plane": "控制中心",
  "Available": "可用",
  "Secret storage": "秘密存储",
  "Recovery required": "需要恢复",
  "Session expiry": "会话到期时间",
  "Administrator": "管理员",
  "Two-factor authentication": "双重身份验证",
  "Enabled": "已启用",
  "Disabled": "已禁用",
  "New recovery codes": "生成新恢复码",
  "Disable": "禁用",
  "Enable": "启用",
  "Could not render the QR code.": "无法生成二维码。",
  "Enable two-factor authentication": "启用双重身份验证",
  "TOTP QR code": "TOTP 二维码",
  "Generating...": "正在生成...",
  "Authentication code": "验证码",
  "Cancel": "取消",
  "Enabling...": "正在启用...",
  "Disable two-factor authentication": "禁用双重身份验证",
  "Generate new recovery codes": "生成新恢复码",
  "Generate": "生成",
  "Recovery codes": "恢复码",
  "Copied": "已复制",
  "Copy": "复制",
  "Done": "完成",
  "Close": "关闭",
  "Loading...": "正在加载...",
  "Subscription unavailable": "订阅暂不可用",
  "Subscription details": "订阅详情",
  "Traffic quota": "流量额度",
  "Unlimited": "不限",
  "Traffic used": "已用流量",
  "Unavailable": "不可用",
  "Reset": "重置周期",
  "Expires": "到期时间",
  "Never": "永不",
  "Services": "服务",
  "No services available.": "暂无可用服务。",
  "Downloads": "下载",
  "Active": "有效",
  "Expired": "已到期",
  "Node disabled": "节点已禁用",
  "Quota reached": "额度已用尽",
  "Monday": "星期一",
  "Tuesday": "星期二",
  "Wednesday": "星期三",
  "Thursday": "星期四",
  "Friday": "星期五",
  "Saturday": "星期六",
  "Sunday": "星期日",
  "Daily / {timezone}": "每天 / {timezone}",
  "{weekday} / {timezone}": "{weekday} / {timezone}",
  "Day {day} / {timezone}": "每月第 {day} 天 / {timezone}",
  "Every {days} days / {timezone}": "每 {days} 天 / {timezone}",
  "The subscription could not be loaded.": "无法加载订阅。",
  "Telemetry": "遥测",
  "Node": "节点",
  "All nodes": "全部节点",
  "Refresh events": "刷新事件",
  "Time": "时间",
  "User": "用户",
  "Source": "来源",
  "Destination": "目标",
  "Protocol": "协议",
  "Action": "动作",
  "No recent access events.": "暂无最近访问事件。",
  "Received {time}": "接收于 {time}",
  "Unknown node": "未知节点",
  "Unknown user": "未知用户",
  "Unknown authorization": "未知授权",
  "Not reported": "未上报",
  "Accepted": "已放行",
  "Blocked": "已阻断",
  "Load older": "加载更早记录",
  "Target": "目标",
  "Actor": "操作者",
  "Outcome": "结果",
  "No audit entries.": "暂无审计记录。",
  "Success": "成功",
  "Failure": "失败",
  "Plugin page could not be loaded.": "无法加载插件页面。",
  "{name} plugin": "{name} 插件",
  "Infrastructure": "基础设施",
  "Name": "名称",
  "Address": "地址",
  "Agent": "Agent",
  "Version": "版本",
  "Policy": "策略",
  "Update": "更新",
  "State": "状态",
  "Actions": "操作",
  "No nodes have been created.": "尚未创建节点。",
  "Not set": "未设置",
  "Online": "在线",
  "Offline": "离线",
  "Pending": "等待中",
  "Create registration token": "创建注册令牌",
  "Update Agent": "更新 Agent",
  "Revoke Agent credential": "吊销 Agent 凭据",
  "No active Agent credential": "没有有效的 Agent 凭据",
  "Edit node": "编辑节点",
  "Delete node": "删除节点",
  "Delete": "删除",
  "Revoke": "吊销",
  "Access": "访问控制",
  "Email": "邮箱",
  "Telegram": "Telegram",
  "No users have been created.": "尚未创建用户。",
  "Edit user": "编辑用户",
  "Delete user": "删除用户",
  "Add": "添加",
  "Add node": "添加节点",
  "Public address": "公开地址",
  "Add user": "添加用户",
  "Display name": "显示名称",
  "Note": "备注",
  "Working...": "正在处理...",
  "{name} registration token": "{name} 的注册令牌",
  "{name} Agent update": "更新 {name} 的 Agent",
  "Current version": "当前版本",
  "Target version": "目标版本",
  "Status": "状态",
  "Delivery attempts": "投递次数",
  "Last sent": "上次发送",
  "Completed": "完成时间",
  "Queuing...": "正在排队...",
  "Queue update": "提交更新",
  "Waiting": "等待中",
  "{count} sent": "已发送 {count} 次",
  "Not configured": "未配置",
  "Applied {generation}": "已应用 {generation}",
  "Pending {applied}/{desired}": "等待中 {applied}/{desired}",
  "Applied": "已应用",
  "Failed": "失败",
  "Unsupported": "不支持",
  "Succeeded": "成功",
  "Enable the node before updating its Agent": "请先启用节点，再更新 Agent",
  "Register the Agent before updating it": "请先注册 Agent，再执行更新",
  "The Agent does not support durable commands": "该 Agent 不支持持久化命令",
  "The Agent does not support self-update": "该 Agent 不支持自更新",
  "Not yet": "尚未",
  "Traffic": "流量",
  "Expiry": "到期",
  "Enforcement": "执行状态",
  "IP slots": "IP 槽位",
  "Add authorization": "添加授权",
  "A node and user are required": "需要先创建节点和用户",
  "No authorizations have been created.": "尚未创建授权。",
  "Manage services": "管理服务",
  "Rotate subscription token": "轮换订阅令牌",
  "Edit authorization": "编辑授权",
  "Delete authorization": "删除授权",
  "Subscription link": "订阅链接",
  "Rotate": "轮换",
  "New subscription link": "新订阅链接",
  "No services are available on this node.": "该节点暂无可用服务。",
  "Numeric values are invalid.": "数值格式无效。",
  "Traffic quota is too large.": "流量额度过大。",
  "Traffic quota (GiB)": "流量额度（GiB）",
  "Daily": "每天",
  "Weekly": "每周",
  "Monthly": "每月",
  "Every N days": "每 N 天",
  "Weekday": "星期",
  "Day of month": "每月日期",
  "Interval (days)": "间隔天数",
  "Timezone": "时区",
  "Period anchor": "周期锚点",
  "Soft IP limit": "软 IP 限制",
  "Activity window (minutes)": "活跃窗口（分钟）",
  "Block duration (minutes)": "阻断时长（分钟）",
  "Observed {time}": "观测于 {time}",
  "Generation {generation}": "代次 {generation}",
  "No data / {quota}": "暂无数据 / {quota}",
  "No end": "无结束时间",
  "{start} - {end}; observed {observed}": "{start} - {end}；观测于 {observed}",
  "of {quota}": "额度 {quota}",
  "Not limited": "不限制",
  "Not reported / {limit}": "未上报 / {limit}",
  "{count} blocked": "已阻断 {count} 个",
  "Extensions": "扩展",
  "Plugin view": "插件视图",
  "Installations": "已安装插件",
  "Node instances": "节点实例",
  "Install plugin": "安装插件",
  "Plugin": "插件",
  "Kind": "类型",
  "Health": "健康状态",
  "Permissions": "权限",
  "No plugins are installed.": "尚未安装插件。",
  "Runtime": "运行时",
  "Feature": "功能",
  "Installing": "安装中",
  "Healthy": "健康",
  "Unhealthy": "异常",
  "Unknown": "未知",
  "Previous {version}": "上一版本 {version}",
  "Open {name}": "打开 {name}",
  "Open plugin": "打开插件",
  "Upgrade {name}": "升级 {name}",
  "Check for upgrade": "检查升级",
  "Replace GitHub token for {name}": "替换 {name} 的 GitHub 令牌",
  "Replace GitHub token": "替换 GitHub 令牌",
  "Uninstall {name}": "卸载 {name}",
  "Uninstall plugin": "卸载插件",
  "Replace token for {name}": "替换 {name} 的令牌",
  "GitHub token": "GitHub 令牌",
  "Replace": "替换",
  "GitHub repository": "GitHub 仓库",
  "Latest stable release": "最新稳定版",
  "Public repository": "公开仓库",
  "Use saved token": "使用已保存的令牌",
  "Checking...": "正在检查...",
  "Check release": "检查版本",
  "Requested permissions": "请求的权限",
  "No kernel permissions requested.": "未请求中心权限。",
  "Install": "安装",
  "Upgrade": "升级",
  "Uninstalling...": "正在卸载...",
  "Uninstall": "卸载",
  "The release belongs to a different plugin.": "该版本属于另一个插件。",
  "Approve every requested permission before continuing.": "继续前请批准所有请求的权限。",
  "Node plugin instances": "节点插件实例",
  "Node plugins": "节点插件",
  "Configure plugin": "配置插件",
  "Desired": "期望状态",
  "Actual": "实际状态",
  "Generation": "代次",
  "Delivery": "投递状态",
  "No node plugins have been configured.": "尚未配置节点插件。",
  "Actual / desired": "实际 / 期望",
  "None": "无",
  "Configure node plugin": "配置节点插件",
  "A reconciliation is already pending": "已有配置任务等待执行",
  "Plugin and node": "插件和节点",
  "No compatible targets": "没有兼容的目标",
  "{plugin} on {node}": "{node} 上的 {plugin}",
  "Desired state": "期望状态",
  "Running": "运行中",
  "Stopped": "已停止",
  "Absent": "未安装",
  "Configuration": "配置",
  "Configure": "配置",
  "Configuration override": "覆盖配置",
  "Apply": "应用",
  "No compatible node and plugin combination is available.": "没有可用的兼容节点和插件组合。",
  "Configuration must be a JSON object.": "配置必须是 JSON 对象。",
  "1 restart": "重启 1 次",
  "{count} restarts": "重启 {count} 次",
  "1 delivery": "投递 1 次",
  "{count} deliveries": "投递 {count} 次",
};

const I18nContext = createContext<I18nValue | undefined>(undefined);

export function I18nProvider({ children }: { children: ReactNode }) {
  const [locale, setLocaleState] = useState<Locale>(readInitialLocale);

  useEffect(() => {
    document.documentElement.lang = locale;
    try {
      window.localStorage.setItem(localeStorageKey, locale);
    } catch {
      // The selected locale still applies for this page when storage is unavailable.
    }
  }, [locale]);

  const t = useCallback<Translate>((message, values) => {
    const translated = locale === "zh-CN" ? (zhCN[message] ?? message) : message;
    return interpolate(translated, values);
  }, [locale]);

  const formatDateTime = useCallback((value: string | Date) => (
    new Date(value).toLocaleString(locale)
  ), [locale]);

  const formatDate = useCallback((value: string | Date) => (
    new Date(value).toLocaleDateString(locale)
  ), [locale]);

  const value = useMemo<I18nValue>(() => ({
    locale,
    setLocale: setLocaleState,
    t,
    formatDate,
    formatDateTime,
  }), [formatDate, formatDateTime, locale, t]);

  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

export function useI18n(): I18nValue {
  const value = useContext(I18nContext);
  if (value === undefined) throw new Error("useI18n must be used within I18nProvider");
  return value;
}

export function LanguageSwitcher({ className = "" }: { className?: string }) {
  const { locale, setLocale, t } = useI18n();
  return (
    <label className={`language-switcher${className ? ` ${className}` : ""}`}>
      <Languages size={16} aria-hidden="true" />
      <span className="visually-hidden">{t("Language")}</span>
      <select value={locale} onChange={(event) => setLocale(event.target.value as Locale)} aria-label={t("Language")}>
        <option value="zh-CN">简体中文</option>
        <option value="en">English</option>
      </select>
    </label>
  );
}

export function translateMessage(locale: Locale, message: string, values?: Values): string {
  return interpolate(locale === "zh-CN" ? (zhCN[message] ?? message) : message, values);
}

export function resolveLocale(value: string | null | undefined): Locale {
  return value === "en" ? "en" : "zh-CN";
}

function readInitialLocale(): Locale {
  try {
    return resolveLocale(window.localStorage.getItem(localeStorageKey));
  } catch {
    return "zh-CN";
  }
}

function interpolate(message: string, values?: Values): string {
  if (values === undefined) return message;
  return message.replace(/\{([a-zA-Z0-9_]+)\}/g, (match, key: string) => (
    Object.hasOwn(values, key) ? String(values[key]) : match
  ));
}
