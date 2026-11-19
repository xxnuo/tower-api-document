# 单机蓝绿部署

## 首次接管生产入口

将此目录复制到独立生产目录，保留生产现有的 `.env`、`data/` 和旧版 Compose 文件。

```bash
scp -r deploy xxnuo@kompiler.lan:/home/xxnuo/services/agentstower/
```

在生产目录运行部署。首次迁移前，用 SQLite 的 `.backup` 生成一致的在线备份。仍使用旧文件名时，下面的 `data/data.db` 改为 `data/agents-tower.db`。

```bash
cd /home/xxnuo/services/agentstower
umask 077
mkdir -p backups
test -f data/data.db && sqlite3 data/data.db ".backup 'backups/pre-blue-green-$(date +%Y%m%d-%H%M%S).db'"
```

镜像请使用版本标签。默认入口为宿主机端口 `18888`。首次迁移会在验证新版及旧版副本后停止原 `agentstower` 容器，再启动 Caddy 接管端口。首次接管有一次短暂入口中断。此时若有尚未完成且保存在旧进程内存中的 OAuth 授权，需重新发起。之后的发布通过 Caddy 平滑重载切流。

## 日常升级示例：从 1.x 升到 2.0.0

Caddy 接管入口后，在本地仓库根目录将 `server/web/package.json` 的 `version` 更新为 `2.0.0`，构建并推送镜像。

```bash
make version
make docker-push
```

在生产机部署该版本，并确认健康检查返回 `status` 为 `ok`、`version` 为 `2.0.0`。

```bash
ssh xxnuo@kompiler.lan
cd ~/services/agentstower
./deploy/deploy.sh deploy registry.lazycat.cloud/x/agentstower:2.0.0
./deploy/deploy.sh status
curl -fsS http://kompiler.lan:18888/api/healthz
```

脚本启动新实例并验证健康后，Caddy 才把新请求转给新版。用户继续使用原来的网页和 MCP 地址。切换时已经交给旧实例的普通请求和 MCP 流继续由旧实例处理，旧版哈希静态资源也仍可访问。

## 回滚

新版出现问题时，在生产目录把流量切回上一实例。

```bash
./deploy/deploy.sh rollback
./deploy/deploy.sh status
curl -fsS http://kompiler.lan:18888/api/healthz
```

两个版本共用同一份 SQLite 数据库，回滚只切换流量，新版已写入的数据仍会保留。若升级包含不兼容的数据库变更，需单独安排迁移和维护窗口。

脚本确认旧实例的代理连接排空后才清理更早的实例。若超过 `DRAIN_TIMEOUT`（默认 120 秒），部署保留该实例，并在下次发布前重新检查排空。

## 隔离验收

隔离验收时，从单独的目录运行脚本，使用假 `.env`、空 SQLite 目录和独立端口，可将脚本所在目录写成绝对路径。

```bash
export CONTAINER_ENGINE=docker COMPOSE_PROJECT_NAME=agentstower-sandbox
export PUBLIC_BIND=127.0.0.1:28888 LEGACY_CONTAINER=agentstower-sandbox-legacy
/path/to/deploy/deploy.sh deploy registry.lazycat.cloud/x/agentstower:1.0.5
curl -fsS http://127.0.0.1:28888/api/healthz
```

默认使用 `podman compose`。使用本地已有应用镜像可设 `SKIP_PULL=1`。宿主机需要 `flock` 与 `curl` 7.84 或更新版本。
