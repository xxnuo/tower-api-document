# Product

<!-- impeccable:product-schema 1 -->

## Platform

web

## Users

- **团队内部成员**：在日常协作中绑定、查看、刷新自己的 Tower.im OAuth 2.0 授权凭据，并在多账号或快速登录会话间切换；当为管理员时，维护成员注册准入、密码重置与系统级安全开关。
- **接入 AI Agent 的开发者**：为本地或团队内的 AI Agent（如 Claude Desktop、Codex、Cursor 等 MCP 客户端）获取专属 `at_sk-...` 鉴权密钥、生成标准接入配置，并通过交互式控制台调试 MCP 工具与 Tower REST API。

## Product Purpose

AgentsTower 及其配套的 Tower API 文档站提供团队内部自托管的 Tower.im OAuth 2.0 凭据托管、交互式 REST API 调试台与 Streamable HTTP MCP 网关服务。产品旨在消除人工频繁获取/传递原始 OAuth Token 的繁琐与泄露风险；衡量成功的标准是团队成员能顺畅完成授权绑定，开发者能在最短路径内让 AI Agent 安全、稳定地调用受控的 Tower 接口并随时查阅准确的接口规范。

## Positioning

将 Tower.im OAuth 2.0 授权流程、SQLite 凭据持久化、按成员签发 `at_sk-...` 密钥的 Streamable HTTP MCP Server、基于真实工作区上下文动态解析参数的 `ApiExplorer` / `McpExplorer` 调试台，以及配套静态 API 参考文档（`docs/`）整合在单一自托管仓库与单二进制部署包中，使 AI Agent 在不接触原始 OAuth 令牌且默认屏蔽高危硬删除的前提下直接操作团队 Tower 资源。

## Operating Context

- **部署与运行环境**：面向受信团队内部自托管运行（通过 `make run`、`make dev` 或 Docker Compose 监听默认 `:8888` 端口，支持通过 `SITE_BASE_URL` 与 `MCP_BASE_URL` 适配反向代理公开地址）。
- **双界面协同**：核心操作界面为 `server/web`（AgentsTower 管理与调试控制台），配套参考界面为 `docs/`（Docsify 静态 Tower API 文档站）。
- **端侧场景权重**：以桌面端高频调试、参数编排与凭据管理为优先场景，同时完整适配移动端日常授权、查看与管理操作。
- **关键衔接动作**：支持 OOB 手动粘贴授权码（`urn:ietf:wg:oauth:2.0:oob`）与 `/oauth2/callback` 自动回调两种授权链路，以及一键复制 MCP 客户端 JSON 配置片段（`AGENTS_TOWER_API_KEY`）。

## Capabilities and Constraints

- **账号与权限治理**：首个网页注册账号自动成为管理员（`admin`），后续默认为普通成员（`user`）；支持本地快速登录记录切换、服务端 CLI 授权/改密及 Web `AdminPanel` 管理。
- **安全准入与防误删边界**：注册准入支持 `open`（开放）、`invite`（凭 `INVITE_CODE` 邀请码）与 `closed`（关闭公开注册）动态切换；MCP 硬删除受全局 `ALLOW_MCP_DELETE` 控制（默认 `false`，仅放行取消完成状态与移除负责人等可逆操作），界面端高危工具调用需显式二次确认。
- **凭据安全展示**：前端展示与调试输出中的敏感令牌及密钥默认脱敏（`maskToken` / `maskSecretsInText`），仅在用户显式复制时写入剪贴板；界面实时提示令牌剩余有效期并支持刷新。
- **双协议调试能力**：`ApiExplorer` 基于 `towerApiCatalog.js` 提供 REST 接口参数自动拉取与调试；`McpExplorer` 提供 JSON-RPC Streamable HTTP 会话连接、延迟检测、读写/高危筛选以及表单与原始 JSON 双模式调用。
- **技术栈与交付约束**：后端为 Go 1.24 + SQLite 单文件数据库，生产构建将 `server/web/dist`（React 19、Vite 6、Tailwind CSS 4，`pnpm` 管理依赖，支持明暗主题切换）内嵌为单一可执行文件；`docs/` 保持轻量静态文档形态。
- **明确未定事项**：暂不引入公网多租户计费、外部商业化套餐或跨组织公开市场机制。

## Brand Commitments

- 主控制台产品名称固定为 **AgentsTower**，配套文档站名称为 **Tower API Docs**。
- 界面文案以简洁准确的中文为主，协议、环境变量、HTTP 方法与鉴权头（如 `OAuth 2.0`、`MCP`、`AGENTS_TOWER_API_KEY`、`at_sk-...`）保留精确英文技术标识。

## Evidence on Hand

- **已实现的核心界面与目录数据**：`server/web/src/` 下的登录注册页（`AuthScreen.jsx`）、凭据管理主视图（`App.jsx`）、REST 调试台（`ApiExplorer.jsx`）、MCP 调试台（`McpExplorer.jsx`）、管理后台（`AdminPanel.jsx`）及接口目录（`towerApiCatalog.js`）。
- **配套文档与既有素材**：`docs/gettingstarted.md`、`docs/_sidebar.md`、`docs/api/*.md` 全量 v1 接口说明，以及 `docs/TowerLogo.png`、`docs/media/logo-apidocs@2x.png` 与授权流程截图（`docs/media/`）。
- **严禁虚构的空白项**：不存在公网客户证言、外部企业案例、SLA 承诺、第三方合规认证徽章或商业定价数据，任何后续界面不得捏造此类背书。

## Product Principles

1. **安全边界显式且默认收敛**：原始令牌默认脱敏展示，高危删除能力默认关闭且需二次确认，任何界面简化都不得削弱凭据与操作风险提示。
2. **缩短从授权到 Agent 可用的路径**：围绕「完成 Tower 授权 → 获取 `at_sk-...` 与 MCP 配置 → 在线验证工具/接口调用」保持最短操作链路与清晰状态反馈。
3. **桌面高密度效率与移动可用性兼顾**：桌面端优先保障参数编排、JSON 载荷检视与多工具切换效率，移动端确保授权登录、凭据复制与后台开关在小屏视口下同样顺畅可操作。
4. **自托管运维极简透明**：配置项、角色权限与运行状态面向内部团队直接可查可控，不制造黑盒状态或外部服务依赖。

## Accessibility & Inclusion

所有新增与改版界面须一并适配桌面端、移动端与 Web 无障碍标准：确保明暗双主题下的文本与状态徽章对比度达标，表单控件、标签页、复制按钮、模态/二次确认操作具备完整键盘可达性与语义化标签（ARIA / 原生表单关联），且不单纯依赖颜色区分成功、过期或高危状态。
