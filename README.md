# Tower API 文档与 OAuth 调试服务

本仓库包含 Tower API 文档以及本地 OAuth 2.0 授权调试服务。

## 目录结构

- `docs/`：Tower API 文档（Docsify 静态站点，入口为 `docs/index.html`）
- `oauth-server/`：Tower OAuth 2.0 授权调试服务端（Go 1.24 + 内嵌 `web/dist` 前端）

## 快速开始

```bash
# 1. 安装前端依赖
make install

# 2. 配置环境变量
cp oauth-server/.env.example oauth-server/.env
# 编辑 oauth-server/.env，填入 TOWER_CLIENT_ID 和 TOWER_CLIENT_SECRET

# 3. 构建前端并启动服务（默认监听 [::]:8888）
make run
```

## 常用命令

- `make install`：安装 `oauth-server/web` 前端依赖（`pnpm install`）
- `make dev`：同时启动 Go 热重载（`air`）与 Vite 前端开发服务
- `make build-web`：构建前端静态资源到 `oauth-server/web/dist`
- `make build`：构建发布用单文件二进制 `oauth-server/bin/tower-oauth`（静态编译并裁剪符号，存在 `upx` 时自动压缩）
- `make test`：构建前端并运行 `go test -v ./...` 与 `go vet ./...`

## 环境变量

可在 `oauth-server/.env` 或进程环境变量中配置：

- `TOWER_CLIENT_ID`：Tower 应用 ID
- `TOWER_CLIENT_SECRET`：Tower 应用私钥
- `TOWER_REDIRECT_URI`：回调地址（默认测试可使用 `urn:ietf:wg:oauth:2.0:oob`，也可配置为服务自身的 `/oauth2/callback`）
- `LISTEN_ADDR`：监听地址（示例 `[::]:8888`）
- `TOWER_BASE_URL`：可选，自定义 Tower 服务基地址
