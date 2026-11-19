# AgentsTower

面向团队成员与 AI Agents 的 Tower.im OAuth 2.0 凭据管理与 Streamable HTTP MCP Server 服务，使用 SQLite 持久化成员凭据并自动生成专属 MCP 鉴权密钥。

## 环境变量

复制 `.env.example` 为 `.env` 并填写：

- `TOWER_CLIENT_ID`: Tower 应用 ID
- `TOWER_CLIENT_SECRET`: Tower 应用密钥
- `TOWER_REDIRECT_URI`: 回调地址（调试可用 `urn:ietf:wg:oauth:2.0:oob`，正式回调如 `http://localhost:8888/oauth2/callback`）
- `LISTEN_ADDR`: 监听地址，默认 `[::]:8888`
- `DB_PATH`: SQLite 数据库路径，默认 `data/agents-tower.db`
- `REGISTRATION_MODE`: 注册准入模式，支持 `open`（默认开放）、`invite`（仅凭邀请码）或 `closed`（关闭公开注册；也可在管理员后台动态调整）
- `INVITE_CODE`: 当 `REGISTRATION_MODE=invite` 时所需的团队邀请码
- `ALLOW_MCP_DELETE`: 是否允许 MCP 执行硬删除操作，默认 `false`（仅放行取消任务完成状态与移除负责人）

## 本地运行（仓库根目录）

- `make install`: 安装前端依赖
- `make dev`: 启动 `air` 后端热重载与 Vite 前端热更新（统一访问 `http://localhost:8888`）
- `make run`: 构建前端并启动 Go 服务
- `make test`: 构建前端并运行 Go 单测与 `go vet`

## 账号与管理员命令行授权

首个在网页注册的账户默认自动成为系统管理员（`admin`）；后续注册成员默认为普通用户（`user`）。也可在服务端通过命令行手动授权：

```bash
cd oauth-server
# 查看所有用户及角色
go run . user list
# 授权指定用户为管理员（或使用编译后的 ./bin/agents-tower）
go run . admin grant <username>
# 取消管理员权限
go run . admin revoke <username>
# 重置用户密码
go run . user reset-password <username> <new-password>
```

## Docker 部署

```bash
# 方式一：在仓库根目录使用 Docker Compose
docker compose up -d --build

# 方式二：单独构建运行
cd oauth-server && docker build -t agents-tower .
docker run -d --name agents-tower -p 8888:8888 -v "$(pwd)/data:/app/data" --env-file .env agents-tower
```

## Agent MCP 接入配置示例

```json
{
  "mcpServers": {
    "agents-tower": {
      "type": "http",
      "url": "http://localhost:8888/mcp",
      "headers": {
        "AGENTS_TOWER_API_KEY": "at_sk-..."
      }
    }
  }
}
```

## MCP 真实任务测试

以后每次实测，都在[指定的测试父任务](https://tower.im/teams/750316/todos/76083/#subtodo-151011233)下面新建本轮专用子任务。链接中的 `#subtodo-151011233` 指定了主任务 #76083 下的具体节点，请保留完整链接。

1. 确认该节点对应的 Tower API 任务 ID，将其作为 `create_todo` 的 `parent_id`。创建后用 `get_todo` 回读父子关系。
2. 在新建的子任务上测试普通成员的任务操作，包括编辑、指派、完成和重新打开、评论、通知成员及工时。每次写入后用对应的读取接口核对实际状态。
3. 收尾时仅清理本轮创建的数据。若硬删除保护拦截清理，将本轮子任务标为完成，并记录保留的评论、工时等数据。

### 2026-11-19 实测记录

本轮在 #76083 下创建了 [#76085 测试子任务](https://tower.im/teams/750316/todos/76085/)。已回读验证标题、描述、截止日期、指派与取消指派、完成与重新打开、评论，以及工时登记和修改。

- 按编号定位 #76083 时，链接中的团队编号 `750316` 可用，同一查询传团队 GUID 返回 404。
- 按项目搜索 `76083` 命中父任务，按创建人搜索相同编号未命中。
- 通知成员传空数组后，接口返回成功，回读仍包含当前成员。
- 任务级工时读写成功，项目级工时列表返回 403。硬删除保护拦截了删除工时和子任务的请求。#76085 最终已完成，保留一条测试评论和一笔 1 分钟工时。
