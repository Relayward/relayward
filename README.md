# Relayward

[简体中文](README.md) | [English](README.en.md)

[![CI](https://github.com/Relayward/relayward/actions/workflows/ci.yml/badge.svg)](https://github.com/Relayward/relayward/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Relayward/relayward)](https://github.com/Relayward/relayward/releases)
[![License](https://img.shields.io/github/license/Relayward/relayward)](LICENSE)

Relayward 是一个面向单管理员场景的轻量代理节点控制中心，通过原生 Agent 和可安装的运行时插件管理节点。中心负责用户、授权、流量配额、订阅、审计记录和插件生命周期，代理核心则独立运行在各个节点上。

Relayward 目前仍在积极开发中。当前版本面向自托管 Linux AMD64 环境，不支持导入 3x-ui 或已退役 xui-stack 的数据。

## 功能

- 原生支持 Debian/systemd 和 Alpine/OpenRC 的 Agent，控制连接仅由节点主动向外建立
- 可靠的命令与事件投递、Agent 更新、插件监管和自动回滚
- 按用户和节点授权，支持流量配额、到期时间、重置周期和软性来源 IP 限制
- 可安装的运行时插件，具有明确的权限、沙箱化管理页面和节点本地代理核心
- 通过官方 [Relayward Xray 插件](https://github.com/Relayward/relayward-plugin-xray) 支持 VLESS + REALITY
- 支持输出 VLESS URI、Mihomo 和 sing-box 订阅
- 管理界面支持简体中文、English、浅色和深色主题
- 使用 SQLite 保存状态，并支持秘密加密、审计记录、热事件和压缩事件归档

## 架构

```text
浏览器
   |
 HTTPS
   |
Relayward 中心（Docker）
   |
   | Agent 主动建立的 WebSocket 控制连接
   |
Relayward Agent（原生系统服务）
   |
   | 受监管的本地插件进程
   |
Xray 运行时插件 -> 官方 Xray 进程
```

中心不运行或打包 Xray。Agent 不暴露管理端口，而是主动连接中心。只有通过运行时插件配置的代理服务端口需要允许客户端访问。

## 环境要求

| 组件 | 支持的环境 |
| --- | --- |
| 中心 | Linux AMD64、Docker Engine、Docker Compose v2 |
| 公网访问 | 域名以及支持 WebSocket 的 HTTPS 反向代理 |
| 节点 | 运行 Debian/systemd 或 Alpine/OpenRC 的 Linux AMD64 |
| 网络 | 节点需要通过 HTTPS 访问中心、GitHub Releases 和运行时发布源 |

示例部署将 Relayward 绑定到 `127.0.0.1:8080`，请勿将这个纯 HTTP 监听地址直接暴露到互联网。

## 快速开始

### 1. 部署中心

在中心服务器上克隆仓库并创建部署环境：

```bash
git clone --depth 1 https://github.com/Relayward/relayward.git
cd relayward
cp .env.example .env
```

启动前检查 `.env`。`RELAYWARD_VERSION` 必须是 [Relayward Releases](https://github.com/Relayward/relayward/releases) 中已经存在的镜像标签；生产环境应使用明确的版本号，不要使用 `latest`。

```bash
docker compose config --quiet
docker compose pull
docker compose up -d
docker compose exec relayward relayward healthcheck
docker compose logs --tail=100 relayward
```

确认健康检查成功后再继续。所有持久化状态都保存在 `relayward-data` Docker 数据卷中。

### 2. 配置 HTTPS

使用反向代理终止 TLS，并将所有路径（包括 WebSocket 升级请求）转发到 `127.0.0.1:8080`。最小 Caddy 配置如下：

```caddyfile
relayward.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

使用 Nginx 或主机管理面板时，需要保留原始 `Host` 请求头并启用 HTTP/1.1 WebSocket 转发。Relayward 的生产环境会话 Cookie 要求使用 HTTPS。

### 3. 初始化管理员

在浏览器中打开 `https://relayward.example.com` 并创建唯一的管理员账号。密码至少需要 12 个字符。

登录后：

1. 打开**设置**，将**中心公开 URL** 设置为 HTTPS 源地址，例如 `https://relayward.example.com`。不要包含路径、查询参数、凭据或应用路由。
2. 设置部署所在时区。
3. 打开**安全**，启用 TOTP，并将生成的恢复代码保存在 Relayward 数据卷之外。

### 4. 注册节点

进入**节点**，选择**添加节点**，填写节点名称以及订阅中使用的公网域名或 IP，然后选择**注册 Agent**。Relayward 会生成一条短期有效且只能使用一次的 root 安装命令。

在节点上运行生成的命令。安装程序会下载最新的官方 [Relayward Agent](https://github.com/Relayward/relayward-agent) 版本、校验文件摘要、安装对应的 systemd 或 OpenRC 服务、注册节点并启动 Agent。

注册对话框应变为 **Agent 已注册并在线**。也可以使用以下命令检查系统服务：

```bash
# Debian/systemd
systemctl status relayward-agent.service
journalctl -u relayward-agent.service -n 100 --no-pager

# Alpine/OpenRC
rc-service relayward-agent status
tail -n 100 /var/log/messages
```

Agent 不需要入站防火墙规则，它会通过 HTTPS 主动连接中心。

### 5. 安装 Xray 插件

打开**插件**，选择**安装插件**并填写：

```text
GitHub 仓库： https://github.com/Relayward/relayward-plugin-xray
版本：        0.4.1
GitHub 令牌： 公开仓库无需填写
```

选择**检查版本**，检查并批准插件申请的每一项权限，然后安装插件。打开已安装的 Xray 插件，选择节点，检查监听地址、公开端口、REALITY 目标、路由和 DNS 设置，然后保存配置。

首次保存节点配置后，系统会安装节点侧插件、下载官方 Xray 版本，并将配置的服务发布到 Relayward。等待**插件 > 节点实例**显示目标配置代次已经应用，并且运行时处于运行状态。

在节点防火墙、服务商防火墙和 NAT 端口映射中放行已配置的 TCP 和/或 UDP 代理端口。Relayward 不会修改宿主机防火墙规则。

### 6. 创建访问权限

1. 打开**用户**并创建用户。
2. 打开**授权**，为该用户和节点添加授权。
3. 设置流量配额、重置周期、到期时间、时区以及可选的软性 IP 限制。
4. 使用**管理服务**将授权绑定到运行时插件发布的一个或多个服务。
5. 复制订阅链接并使用客户端验证。

订阅端点可以输出已安装插件提供的 VLESS URI、Mihomo 和 sing-box 格式。

## 部署验收

部署完成后，逐项确认：

- `docker compose exec relayward relayward healthcheck` 执行成功。
- 管理页面只能通过 HTTPS 访问。
- 节点显示在线，并能看到 Agent 版本。
- Xray 节点实例显示目标配置代次已经应用，运行时处于运行状态。
- 策略下发后，授权显示为有效状态。
- 订阅包含预期的服务和公网端点。
- 真实客户端可以通过配置的代理端口建立连接。

## 运维

`relayward-data` 数据卷包含主数据库、热事件数据库、实例加密密钥、插件制品、插件状态和事件归档。备份和恢复时必须将其作为一个整体处理。

有关备份、升级、回滚、管理员恢复、实例密钥恢复和节点凭据恢复流程，请参阅[运维文档](docs/operations.md)。数据库迁移只支持向前执行，升级正式版本前必须创建完整的数据卷快照。

## 开发

后端检查：

```bash
go test ./...
go vet ./...
go build ./cmd/relayward
```

前端检查：

```bash
cd web
npm ci
npm run typecheck
npm test
npm run build
```

在本地运行后端和 Vite 开发服务器：

```bash
go run ./cmd/relayward serve -listen 127.0.0.1:8080 -data ./data -insecure-cookie

cd web
npm run dev
```

`-insecure-cookie` 仅用于本地回环地址开发。Vite 开发服务器会将 API 请求代理到 `127.0.0.1:8080`。

## 相关项目

- [Relayward Agent](https://github.com/Relayward/relayward-agent)
- [Relayward SDK](https://github.com/Relayward/relayward-sdk)
- [Relayward Xray Plugin](https://github.com/Relayward/relayward-plugin-xray)

## 安全

插件是受信任的原生可执行程序，只能从可信仓库安装。不要在 Issue 中公开注册令牌、订阅链接、恢复代码、GitHub 令牌、私有配置或 Relayward 数据卷。

请通过仓库的 [GitHub Security Advisories](https://github.com/Relayward/relayward/security/advisories/new) 私下报告安全问题。

## 许可证

Relayward 使用 [Apache License 2.0](LICENSE) 许可。
