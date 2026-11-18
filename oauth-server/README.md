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
