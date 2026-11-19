#!/usr/bin/env bash
set -Eeuo pipefail
source_dir=$(cd -- "$(dirname -- "${BASH_SOURCE[0]}")" && pwd)
root=$PWD
state=$root/.rollout
engine=${CONTAINER_ENGINE:-podman}
project=${COMPOSE_PROJECT_NAME:-agentstower-production}
public_bind=${PUBLIC_BIND:-18888}
public_port=${public_bind##*:}
legacy=${LEGACY_CONTAINER:-agentstower}
health_timeout=${HEALTH_TIMEOUT:-60}
drain_timeout=${DRAIN_TIMEOUT:-120}
route_file=$state/Caddyfile
handover=$state/handover

fail() { echo "$*" >&2; exit 1; }
compose() { "$engine" compose --project-directory "$root" -p "$project" -f "$source_dir/compose.yml" "$@"; }

read_routes() {
    active= previous=
    if [[ -f $route_file ]]; then
        read -r _ _ active previous < "$route_file"
        case "$active:$previous" in
            blue:|green:|spare:|blue:green|blue:spare|green:blue|green:spare|spare:blue|spare:green) ;;
            *) fail "部署路由文件无效：$route_file" ;;
        esac
    fi
}

write_routes() {
    sed -e "1c# routes: $1 ${2:-}" -e "s/ACTIVE_SLOT/$1/g" \
        -e "s/PREVIOUS_SLOT/${2:-$1}/g" "$source_dir/Caddyfile" > "$state/Caddyfile.next" || return
    mv -f -- "$state/Caddyfile.next" "$route_file"
}

reload_routes() {
    write_routes "$1" "${2:-}" || return
    compose exec -T gateway caddy reload --config /rollout/Caddyfile --adapter caddyfile
}

body() { compose exec -T "$1" wget -T 2 -qO- "http://127.0.0.1:8888$2"; }

ready() {
    local deadline=$((SECONDS + health_timeout)) health html
    while (( SECONDS < deadline )); do
        if health=$(body "$1" /api/healthz 2>/dev/null) &&
            [[ $health == *'"status":"ok"'* ]] &&
            body "$1" /api/config 2>/dev/null | grep -qF '"service":"AgentsTower"' &&
            html=$(body "$1" / 2>/dev/null) && [[ $html == *'/assets/'* ]]; then
            # Releases through 1.0.5 published before /api/readyz.
            if [[ $health =~ \"version\":\"1\.0\.[0-5]\" ]] ||
                body "$1" /api/readyz 2>/dev/null | grep -qF '"status":"ready"'; then
                printf '%s' "$health"
                return 0
            fi
        fi
        sleep 1
    done
    return 1
}

verify_public() {
    local origin=http://${PROBE_HOST:-127.0.0.1}:$public_port
    local deadline=$((SECONDS + health_timeout)) probe auth
    while (( SECONDS < deadline )); do
        if probe=$(curl --noproxy '*' -fsS --max-time 2 -w '\n%header{x-agentstower-slot}' \
            "$origin/api/healthz" 2>/dev/null) &&
            [[ ${probe##*$'\n'} == "$1" && $(printf '%s' "${probe%$'\n'*}") == "$2" ]] &&
            auth=$(curl --noproxy '*' -sS --max-time 2 \
                -H 'AGENTS_TOWER_API_KEY: rollout-health-probe' \
                -H 'Content-Type: application/json' \
                -H 'Accept: application/json, text/event-stream' \
                --data '{}' -w '\n%{http_code}' "$origin/mcp" 2>/dev/null) &&
            [[ $auth == *'无效的 AGENTS_TOWER_API_KEY'*$'\n401' ]]; then
            return 0
        fi
        sleep 1
    done
    return 1
}

remove_slot() { compose rm -sf "$1"; }

drain() {
    local deadline=$((SECONDS + drain_timeout)) running sockets
    running=$(compose ps --status running -q "$1") || return
    [[ -n $running ]] || return 0
    while (( SECONDS < deadline )); do
        # The Caddy transports disable upstream keep-alive, so these sockets
        # represent in-flight requests even after Caddy replaces its configuration.
        if sockets=$(compose exec -T "$1" netstat -nt) &&
            awk '$4 ~ /:8888$/ && $6 == "ESTABLISHED" { busy = 1 } END { exit busy }' <<< "$sockets"; then
            return 0
        fi
        sleep 1
    done
    return 1
}

start_slot() {
    APP_IMAGE=$2 compose up -d --no-deps --pull never "$1" >&2 || return
    ready "$1"
}

restore_handover() {
    local saved_legacy
    read -r saved_legacy < "$handover"
    compose stop gateway || return
    if [[ $saved_legacy != "-" ]]; then "$engine" start "$saved_legacy" || return; fi
    rm -f -- "$route_file"
    rm -f -- "$handover"
}

[[ ${1:-} == deploy || ${1:-} == rollback || ${1:-} == status ]] ||
    fail "用法：$0 deploy <镜像引用> | rollback | status"
if [[ $1 == deploy ]]; then
    [[ $# == 2 ]] || fail "请指定完整镜像引用，如 registry.lazycat.cloud/x/agentstower:1.0.6"
else
    [[ $# == 1 ]] || fail "命令 $1 不接受其他参数"
fi
mkdir -p -- "$state"
exec 9>"$state/deploy.lock"
flock -n 9 || fail "已有部署任务运行"
exec 7>"$root/.migration.lock"
flock -n 7 || fail "已有数据库迁移任务运行"
read_routes
if [[ $1 == status ]]; then
    printf '当前实例 %s\n上一实例 %s\n' "${active:-无}" "${previous:-无}"
    compose ps
    exit
fi
if [[ -f $handover ]]; then
    if [[ -n $active ]] && expected=$(ready "$active") &&
        compose start gateway && verify_public "$active" "$expected"; then
        rm -f -- "$handover"
    else
        restore_handover || fail "上次首次切换未完成，无法恢复原入口"
        read_routes
    fi
fi
if [[ -n $active ]]; then
    # Reconcile a reload interrupted after the file was atomically replaced.
    compose exec -T gateway caddy reload --config /rollout/Caddyfile --adapter caddyfile ||
        fail "无法同步入口配置"
fi

if [[ $1 == rollback ]]; then
    [[ -n $previous ]] || fail "没有可回滚的上一实例"
    expected=$(ready "$previous") || fail "上一实例未通过健康检查"
    if ! reload_routes "$previous" "$active" || ! verify_public "$previous" "$expected"; then
        reload_routes "$active" "$previous" || fail "回滚后入口检查失败，无法恢复原路由"
        fail "回滚后入口检查失败，已恢复原路由"
    fi
    exit
fi

[[ -f ${APP_ENV_FILE:-.env} ]] || fail "找不到生产环境配置 ${APP_ENV_FILE:-.env}"
mkdir -p -- "${APP_DATA_DIR:-data}"
image=$2
if [[ ${SKIP_PULL:-0} != 1 ]]; then "$engine" pull "$image"; fi
"$engine" image inspect "$image" >/dev/null

for candidate in blue green spare; do
    [[ $candidate == "$active" || $candidate == "$previous" ]] || break
done

if [[ -n $active ]] && ! drain "$candidate"; then
    fail "先前的入口配置仍有请求，保留旧实例，稍后重试"
fi
remove_slot "$candidate" >/dev/null
if ! expected=$(start_slot "$candidate" "$image"); then
    remove_slot "$candidate" || :
    fail "候选实例未通过健康检查"
fi

if [[ -n $active ]]; then
    if ! reload_routes "$candidate" "$active" || ! verify_public "$candidate" "$expected"; then
        reload_routes "$active" "$previous" || fail "切换后入口检查失败，无法恢复原路由"
        if drain "$candidate"; then remove_slot "$candidate" || :; fi
        fail "切换后入口检查失败，已恢复原路由"
    fi
    if ! drain "$active"; then
        echo "上一入口配置仍在处理请求，当前入口已切换，暂时保留旧实例。" >&2
    fi
    if [[ -n $previous ]] && drain "$previous"; then remove_slot "$previous" || :; fi
else
    initial_previous=
    legacy_running=$("$engine" inspect --format '{{.State.Running}}' "$legacy" 2>/dev/null || :)
    if [[ $legacy_running == true ]]; then
        # Reuse the exact running image even if its tag changed since creation.
        legacy_id=$("$engine" inspect --format '{{.Image}}' "$legacy")
        legacy_image=localhost/${project}-legacy:initial
        "$engine" tag "$legacy_id" "$legacy_image"
        initial_previous=green
        remove_slot "$initial_previous" >/dev/null
        if ! start_slot "$initial_previous" "$legacy_image" >/dev/null; then
            remove_slot "$initial_previous" || :
            remove_slot "$candidate" || :
            fail "旧版副本未通过健康检查，原服务仍在运行"
        fi
    fi

    # The legacy service owns the public port until this one-time handover.
    if [[ -n $initial_previous ]]; then
        printf '%s\n' "$legacy" > "$handover.next"
    else
        printf '%s\n' - > "$handover.next"
    fi
    mv -f -- "$handover.next" "$handover"
    write_routes "$candidate" "$initial_previous"
    if ! compose run --rm --no-deps gateway caddy validate --config /rollout/Caddyfile --adapter caddyfile ||
        ! compose create gateway; then
        rm -f -- "$route_file" "$handover"
        fail "入口配置校验或容器创建失败"
    fi
    if [[ -n $initial_previous ]]; then
        if ! "$engine" stop -t 30 "$legacy"; then
            restore_handover || fail "旧服务停止失败，无法恢复原入口"
            fail "旧服务停止失败"
        fi
    fi
    if ! compose start gateway || ! verify_public "$candidate" "$expected"; then
        restore_handover || fail "入口启动失败，无法恢复原入口"
        remove_slot "$candidate" || :
        if [[ -n $initial_previous ]]; then remove_slot "$initial_previous" || :; fi
        fail "入口启动失败"
    fi
    rm -f -- "$handover"
fi

printf '已切换到 %s (%s)\n' "$candidate" "$image"
