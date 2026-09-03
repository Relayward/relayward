# Relayward

[简体中文](README.md) | [English](README.en.md)

[![CI](https://github.com/Relayward/relayward/actions/workflows/ci.yml/badge.svg)](https://github.com/Relayward/relayward/actions/workflows/ci.yml)
[![Release](https://img.shields.io/github/v/release/Relayward/relayward)](https://github.com/Relayward/relayward/releases)
[![License](https://img.shields.io/github/license/Relayward/relayward)](LICENSE)

Relayward 是一个面向单管理员场景的轻量节点控制中心，负责管理员认证、节点登记、用户、授权、流量配额、订阅入口、审计记录和插件生命周期。节点通过兼容的 Agent 接入中心，具体运行时能力由独立插件提供。

Relayward 目前仍在积极开发中，当前版本面向自托管 Linux AMD64 环境。所有 `v0.x` Release 都是公开开发制品，尚未建立持久化数据兼容承诺；更换 `v0.x` 版本时必须使用空的 `.data` 目录重新初始化，不能将旧版本数据目录交给新版本运行。首次兼容版本会在发布说明中明确标注。

## 功能

- 单管理员密码认证、TOTP、恢复代码、会话管理和本地管理员恢复
- 节点登记、一次性 Agent 注册、节点主动建立的控制连接和在线状态管理
- 可靠的命令与事件投递、策略下发、Agent 更新以及失败回滚协调
- 按用户和节点配置并下发流量配额、到期时间、重置周期和软性来源 IP 限制策略
- 插件服务目录、授权服务绑定、统一订阅入口和按公网端点展开订阅节点
- 直连、NAT、域名端点，以及中心统一管理的 Cloudflare DDNS 记录
- 插件版本检查、权限审批、制品校验、进程监管、升级回滚和沙箱化管理页面
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
运行时或功能插件（独立项目）
```

中心只管理通用节点、策略、服务和插件状态，不解释运行时专属配置，也不运行或打包代理核心。Agent 不暴露管理端口，而是主动连接中心。

## 环境要求

| 组件 | 支持的环境 |
| --- | --- |
| 中心 | Linux AMD64、Docker Engine、Docker Compose v2、curl |
| 中心访问 | 可达的 HTTP 地址，或域名以及支持 WebSocket 的 HTTPS 反向代理 |
| 节点 | 运行 Debian/systemd 或 Alpine/OpenRC 的 Linux AMD64 |
| 网络 | 节点需要通过 HTTP(S) 访问中心，并通过 HTTPS 访问 GitHub Releases、运行时发布源以及 `api4.ipify.org`/`api6.ipify.org` |

示例部署默认将 Relayward 绑定到 `127.0.0.1:8080`。直接使用 HTTP 时，需要在 `.env` 中将 `RELAYWARD_BIND_ADDRESS` 改为客户端可达的宿主机地址；HTTP 传输不会加密登录凭据、订阅令牌和 Agent 数据。

## 快速开始

### 1. 部署中心

进入你选定的空部署目录，直接下载仓库提供的 Compose 和环境变量示例：

```bash
curl -fsSL \
  https://raw.githubusercontent.com/Relayward/relayward/main/compose.yaml \
  -o compose.yaml
curl -fsSL \
  https://raw.githubusercontent.com/Relayward/relayward/main/.env.example \
  -o .env
chmod 600 .env
```

启动前检查 `.env`，并保持其权限为 `0600`，因为可选配置可能包含秘密。`RELAYWARD_VERSION` 必须是已发布的镜像标签；生产环境应使用 [Relayward Releases](https://github.com/Relayward/relayward/releases) 中的明确版本号，不要使用 `latest`。需要跟踪 `main` 最新开发代码时可以使用 `dev`，该标签只在非文档变更通过 CI 后更新，不提供稳定性或数据兼容保证。容器首次启动时会自动创建 `.data` 并准备目录权限，Relayward 主进程仍以非 root 用户运行。

```bash
docker compose config --quiet
docker compose up -d --wait
docker compose exec relayward relayward healthcheck
docker compose logs --tail=100 relayward
```

确认健康检查成功后再继续。所有持久化状态都保存在当前部署目录的 `.data` 中。

### 2. 配置访问方式

Relayward 支持直接使用 HTTP，HTTPS 不是启动和使用系统的前置条件。通过 HTTP 使用管理端和 Agent 时，登录凭据、会话数据、注册凭据、命令及遥测会以明文传输；界面会持续显示安全警告。只应在你接受该风险的网络中使用 HTTP。

直接使用 HTTP 时，还需要在云平台防火墙和宿主机防火墙中放行 `RELAYWARD_PORT` 对应的 TCP 端口。仅当宿主机使用 UFW 且保留默认端口时，可以执行 `ufw allow 8080/tcp`；其他环境应使用对应的防火墙工具和实际端口。

强烈建议使用反向代理终止 TLS，并将所有路径（包括 WebSocket 升级请求）转发到 `127.0.0.1:8080`。最小 Caddy 配置如下：

```caddyfile
relayward.example.com {
    reverse_proxy 127.0.0.1:8080
}
```

`compose.yaml` 还提供了默认注释的 Cloudflare Tunnel 服务。使用时，在 Cloudflare 创建 Tunnel 和公开主机名，将源站服务设为 `http://relayward:8080`，把 Tunnel Token 写入 `.env` 的 `CLOUDFLARE_TUNNEL_TOKEN`，然后取消 `cloudflared` 服务整段注释。Token 属于秘密，不能提交到 Git。

使用 Nginx 或主机管理面板时，需要保留原始 `Host` 请求头、传递 `X-Forwarded-Proto` 并启用 HTTP/1.1 WebSocket 转发。Relayward 会根据请求协议自动设置会话 Cookie；HTTPS 请求使用 `Secure` Cookie，HTTP 请求使用普通 Cookie。

### 3. 初始化管理员

在浏览器中打开选定的 HTTP 或 HTTPS 地址并创建唯一的管理员账号。

登录后：

1. 打开**设置**，确认自动填入的**中心公开 URL**；它来自当前浏览器访问地址，也可以改为其他 HTTP 或 HTTPS 源地址。不要包含路径、查询参数、凭据或应用路由。
2. 设置部署所在时区。
3. 可选：打开**安全**并启用 TOTP。公网或不受信任网络中的部署强烈建议启用；启用后请将生成的恢复代码保存在 Relayward `.data` 目录之外。

### 4. 注册节点

进入**节点**，选择**添加节点**，填写节点名称，然后选择**注册 Agent**。Relayward 会生成一条短期有效且只能使用一次的 root 安装命令。

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

Agent 不需要入站防火墙规则，它会通过中心公开 URL 配置的 HTTP 或 HTTPS 地址主动连接中心。

### 5. 选择并安装插件

Relayward 本身不提供代理协议或代理核心。选择兼容的运行时插件，并以该插件仓库的 README 作为安装、节点配置和网络放行依据。

安装插件时，打开**插件**并选择**安装插件**，填写插件的 GitHub 仓库以及私有仓库所需的可选令牌。等待已发布版本列表加载完成，选择要安装的版本，再选择**检查版本**。核对清单和制品信息，批准插件申请的每一项权限，然后安装。

当前官方运行时插件列在[相关项目](#相关项目)中。插件提供的协议、配置字段、服务端口、订阅格式和验收方法不属于本仓库，请遵循对应插件文档。

### 6. 配置订阅端点

进入节点详情的**订阅端点**页。Agent 启动时会探测公网 IPv4 和 IPv6，之后每 10 分钟更新一次；成功探测到的地址可以分别创建为直连端点。NAT 机器可以填写固定的公网 IP 或域名，已有外部维护域名的节点可以添加域名端点。

需要由 Relayward 更新 DNS 时，打开中心级 **DDNS** 页面。先在**DNS 服务商连接**中添加 Cloudflare 连接；API Token 至少需要目标 Zone 的 `Zone:Read` 和 `DNS:Edit` 权限，建议将资源范围限制到实际使用的 Zone。然后在**托管记录**中选择节点、地址族、连接、Zone 和记录名称。Relayward 每分钟检查待同步记录，并在节点公网地址变化后创建或更新对应的 A/AAAA 记录。Cloudflare 凭据由中心保存并可供多个节点复用，节点详情只展示关联记录及同步状态。

一个节点可以有多个端点。每个有效授权服务会按所有可用端点展开为独立的客户端节点。如果 NAT 或端口转发后的公网端口不同于运行时监听端口，可以在端点中按插件和服务设置公网端口覆盖。

### 7. 创建访问权限

1. 打开**用户**并创建用户。
2. 打开**授权**，为该用户和节点添加授权。
3. 设置流量配额、重置周期、到期时间、时区以及可选的软性 IP 限制。
4. 安装的插件发布服务后，使用**管理服务**将授权绑定到一个或多个服务。
5. 复制授权生成的订阅链接。

Relayward 负责授权状态、流量信息和统一订阅入口；订阅中的具体连接内容和输出格式由已安装插件提供。

## 部署验收

部署完成后，逐项确认：

- `docker compose exec relayward relayward healthcheck` 执行成功。
- 管理页面可通过选定的 HTTP 或 HTTPS 地址访问；使用 HTTP 时页面会显示明文传输警告。
- 节点显示在线，并能看到 Agent 版本。
- 节点详情显示探测到的公网地址，并至少配置了一个可用订阅端点。
- 已安装的中心插件显示为有效且健康。
- 策略下发后，授权显示为有效状态。

运行时服务、插件节点实例、订阅内容和客户端连通性应按照对应插件 README 继续验收。

## 运维

`.data` 目录包含主数据库、热事件数据库、实例加密密钥、插件制品、插件状态和事件归档。备份和恢复时必须将整个目录作为一个整体处理。

有关备份、开发版本更换、回滚、管理员恢复、实例密钥恢复和节点凭据恢复流程，请参阅[运维文档](docs/operations.md)。当前 `v0.x` 开发版本不能复用其他版本创建的 `.data`；备份只能与创建它的相同镜像版本配套恢复。

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
go run ./cmd/relayward serve -listen 127.0.0.1:8080 -data ./data

cd web
npm run dev
```

Vite 开发服务器会将 API 请求代理到 `127.0.0.1:8080`。Relayward 会根据浏览器请求的协议自动选择会话 Cookie 的安全属性。

## 相关项目

- [Relayward Agent](https://github.com/Relayward/relayward-agent)
- [Relayward SDK](https://github.com/Relayward/relayward-sdk)
- [Xray Plugin for Relayward](https://github.com/qqqasdwx/relayward-plugin-xray)

## 安全

插件是受信任的原生可执行程序，只能从可信仓库安装。HTTP 可以完整使用，但不会提供传输加密；对不受信任的网络和公网部署应使用 HTTPS。不要在 Issue 中公开注册令牌、订阅链接、恢复代码、GitHub 令牌、私有配置或 Relayward `.data` 目录。

请通过仓库的 [GitHub Security Advisories](https://github.com/Relayward/relayward/security/advisories/new) 私下报告安全问题。

## 许可证

Relayward 使用 [Apache License 2.0](LICENSE) 许可。
