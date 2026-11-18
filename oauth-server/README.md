# Tower OAuth 凭据中心

无状态 Tower.im OAuth 2.0 登录代理与静态前端，服务端不存储任何成员账号或 Token，凭据仅保存在用户本地浏览器 `localStorage` 中。

## 环境变量

复制 `.env.example` 为 `.env` 并填写：

- `TOWER_CLIENT_ID`: Tower 应用 ID
- `TOWER_CLIENT_SECRET`: Tower 应用密钥
- `TOWER_REDIRECT_URI`: 回调地址（调试可用 `urn:ietf:wg:oauth:2.0:oob`，正式回调如 `http://localhost:8888/oauth2/callback`）
- `LISTEN_ADDR`: 监听地址，默认 `[::]:8888`

## 本地运行（仓库根目录）

- `make install`: 安装前端依赖
- `make dev`: 启动 `air` 后端热重载与 Vite 前端热更新（统一访问 `http://localhost:8888`）
- `make run`: 构建前端并启动 Go 服务
- `make test`: 构建前端并运行 Go 单测与 `go vet`

## Docker 部署

```bash
cd oauth-server
docker build -t tower-oauth .
docker run -d --name tower-oauth -p 8888:8888 --env-file .env tower-oauth
```
