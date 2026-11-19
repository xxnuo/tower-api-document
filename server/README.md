# AgentsTower

面向团队成员与 AI Agents 的 Tower.im OAuth 2.0 凭据管理与 Streamable HTTP MCP Server 服务，使用 SQLite 持久化成员凭据并自动生成专属 MCP 鉴权密钥。

## 环境变量

复制 `.env.example` 为 `.env` 并填写：

- `TOWER_CLIENT_ID`: Tower 应用 ID
- `TOWER_CLIENT_SECRET`: Tower 应用密钥
- `TOWER_REDIRECT_URI`: 回调地址（调试可用 `urn:ietf:wg:oauth:2.0:oob`，正式回调如 `http://localhost:8888/oauth2/callback`）
- `LISTEN_ADDR`: 监听地址，默认 `[::]:8888`
- `DB_PATH`: SQLite 数据库路径，默认 `data/data.db`
  同目录已有 `agents-tower.db` 且尚无 `data.db` 时，服务继续使用原数据库。
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
cd server
# 查看所有用户及角色
go run . user list
# 授权指定用户为管理员（或使用编译后的 ./bin/agentstower）
go run . admin grant <username>
# 取消管理员权限
go run . admin revoke <username>
# 重置用户密码
go run . user reset-password <username> <new-password>
```

## Docker 部署

### 生产服务

- 部署主机为 `xxnuo@kompiler.lan`，由当前用户通过 Podman Compose 管理。
- 服务入口为 [http://kompiler.lan:18888](http://kompiler.lan:18888)，MCP 路径为 `/mcp`。宿主机端口 `18888` 映射到容器端口 `8888`。
- 工作目录为 `/home/xxnuo/services/agentstower`。部署使用目录内的 `compose.yml`，镜像为 `registry.lazycat.cloud/x/agentstower`，默认标签为 `1.0.2`。
- 远端 `.env` 从本机 `server/.env` 原样复制，权限为 `0600`。SQLite 数据库为 `data/data.db`，宿主机的 `./data` 挂载到容器的 `/app/data`。

```bash
ssh xxnuo@kompiler.lan
cd /home/xxnuo/services/agentstower
podman compose pull
podman compose up -d --wait
curl -fsS http://kompiler.lan:18888/api/healthz
```

升级时，在远端 `compose.yml` 中更新默认镜像标签，再执行上面的拉取和启动命令。发布新镜像可在仓库根目录运行 `make bump` 和 `make docker-push`。

### 本地 Docker

```bash
# 方式一：在仓库根目录使用 Docker Compose
docker compose up -d

# 方式二：单独构建运行
cd server && docker build -t agentstower .
docker run -d --name agentstower -p 8888:8888 -v "$(pwd)/data:/app/data" --env-file .env agentstower
```

## Agent MCP 接入配置示例

```json
{
  "mcpServers": {
    "tower": {
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

以后每次实测，都在[测试父任务 #76083](https://tower.im/teams/750316/todos/76083/)下面新建本轮专用子任务。

1. 确认 #76083 对应的 Tower API 任务 ID，将其作为 `create_todo` 的 `parent_id`。创建后用 `get_todo` 回读父子关系。
2. 在新建的子任务上测试普通成员的任务操作，包括编辑、指派、完成和重新打开、评论、通知成员及工时。每次写入后用对应的读取接口核对实际状态。
3. 收尾时仅清理本轮创建的数据。若硬删除保护拦截清理，将本轮子任务标为完成，并记录保留的评论、工时等数据。

### 2026-11-19 实测记录

本轮在 #76083 下创建了 [#76085 测试子任务](https://tower.im/teams/750316/todos/76085/)。已回读验证标题、描述、截止日期、指派与取消指派、完成与重新打开、评论，以及工时登记和修改。

- 按编号定位 #76083 时，链接中的团队编号 `750316` 可用，同一查询传团队 GUID 返回 404。
- 按项目搜索 `76083` 命中父任务，按创建人搜索相同编号未命中。
- 通知成员传空数组后，接口返回成功，回读仍包含当前成员。
- 任务级工时读写成功，项目级工时列表返回 403。硬删除保护拦截了删除工时和子任务的请求。#76085 最终已完成，保留一条测试评论和一笔 1 分钟工时。

同日复测，团队成员在 #76083 下创建子任务 #76090，成功编辑标题、描述和截止日期，指派和移除负责人，完成和重新打开任务，并发表评论、添加和清空通知成员。任务工时的创建、读取和修改均成功。清空通知成员仅确认调用成功。

MCP 硬删除保护拦截了本轮的两次删除请求。使用同一成员的 Tower API 凭据删除其创建的工时和子任务，均返回 HTTP 204。随后确认工时已从任务工时列表移除，#76090 已从父任务移除。本轮仅验证成员删除自己创建的资源。

普通成员调用 `create_team` 成功，MCP 返回新团队 ID 和名称，未返回套餐字段。项目权限由用户手动验证：普通成员可创建项目并在参与的项目中协作；修改设置和删除项目需要创建者或相应项目管理权限。

在 #76083 的子任务 #76095 中实测任务附件：团队直传签名和 OSS 上传均返回 HTTP 200，通过 `update_todo.attfile_guids` 关联附件后回读得到附件 ID，传空数组移除后回读为空。临时子任务已删除（HTTP 204）。`create_upload`、`upload_file` 和 `delete_upload` 管理项目文件，作用范围为项目文件列表。

在 #76083 的子任务 #76101 中复测通知成员。初始列表只有创建者，依次传入仅高畅、两人、仅创建者、仅高畅、空数组，每次用 Tower API 回读均与传入集合一致。空数组可清空通知成员，测试子任务已删除。另在隔离的本地 MCP 实例开启硬删除，用同一普通成员账号调用 `delete_todo` 删除临时子任务 #76102，返回 HTTP 204，回读父任务确认子任务已移除。

- 任务评论：普通成员在 #76103 分别发表纯文本和带加粗标记的 HTML 评论。`list_todo_comments` 回读的两条评论与发布结果一致，HTML 标记保留。测试子任务已删除。
- 提及、通知与动态：12:46:21，许广友在 [#76104](https://tower.im/teams/750316/todos/76104/) 评论中 @高畅，回读确认提及链接指向高畅的成员 ID。12:49:03，高畅回复 `1`。许广友通过 `list_notifications` 查到该条未读回复通知，通过 `list_events` 查到同一时间的回复动态。该记录验证了回复通知到达许广友的账号；高畅客户端的推送状态仅接收方可见。
- 用户确认已完成任务清单的创建、改名和删除，以及项目讨论和项目文件相关操作的测试。
