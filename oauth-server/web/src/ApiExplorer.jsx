import { useCallback, useEffect, useMemo, useState } from "react";
import { CheckIcon, CopyIcon } from "@phosphor-icons/react";
import { TOWER_API_CATALOG } from "./towerApiCatalog.js";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmDialog, StatusBanner, TONE_BADGE } from "@/components/shared";

const METHOD_BADGE = {
  GET: TONE_BADGE.success,
  POST: TONE_BADGE.info,
  PATCH: TONE_BADGE.warning,
  PUT: TONE_BADGE.warning,
  DELETE: TONE_BADGE.danger,
};

function extractPathParams(path) {
  return [...path.matchAll(/\{([^}]+)\}/g)].map((m) => m[1]);
}

function defaultQueryForEndpoint(item) {
  if (item.defaultQuery) return item.defaultQuery;
  if (item.method !== "GET") return "";
  for (const suffix of [
    "/members",
    "/assigned_todos",
    "/assigned_uncompleted_todos",
    "/assigned_completed_todos",
    "/created_todos",
    "/created_uncompleted_todos",
    "/created_completed_todos",
    "/todos",
    "/comments",
    "/topics",
    "/uploads",
    "/time_logs",
    "/notifications",
  ]) {
    if (item.path.endsWith(suffix)) {
      return "page[number]=1&page[size]=20";
    }
  }
  return "";
}

function resolveOptionKey(path, paramName) {
  if (paramName !== "id") return paramName;
  if (path.startsWith("/api/v1/teams/")) return "team_id";
  if (path.startsWith("/api/v1/projects/")) return "project_id";
  if (path.startsWith("/api/v1/todolists/")) return "todolist_id";
  if (path.startsWith("/api/v1/todos/")) return "todo_id";
  if (path.startsWith("/api/v1/topics/")) return "topic_id";
  if (path.startsWith("/api/v1/uploads/")) return "upload_id";
  return "id";
}

export function toOptions(body) {
  const list = Array.isArray(body)
    ? body
    : Array.isArray(body?.data)
      ? body.data
      : Array.isArray(body?.items)
        ? body.items
        : [];
  return list
    .map((item) => {
      const id = item?.id || item?.guid;
      if (!id) return null;
      const attrs = item?.attributes && typeof item.attributes === "object" ? item.attributes : item;
      const label =
        attrs?.name ||
        attrs?.nickname ||
        attrs?.subject ||
        attrs?.content ||
        attrs?.title ||
        attrs?.filename ||
        attrs?.desc ||
        item?.name ||
        String(id);
      return { id: String(id), label: String(label).slice(0, 40) };
    })
    .filter(Boolean);
}

export function HighlightedJSON({ text }) {
  const tokens = [];
  const pattern =
    /("(?:\\u[a-fA-F0-9]{4}|\\[^u]|[^\\"])*"(?:\s*:)?|\b(?:true|false|null)\b|-?\d+(?:\.\d*)?(?:[eE][+\-]?\d+)?)/g;
  let lastIndex = 0;
  let match;
  let key = 0;

  while ((match = pattern.exec(text)) !== null) {
    if (match.index > lastIndex) {
      tokens.push(text.slice(lastIndex, match.index));
    }
    const token = match[0];
    let cls = "text-amber-700 dark:text-amber-300";
    if (token.startsWith('"')) {
      cls = token.endsWith(":")
        ? "text-sky-800 dark:text-sky-300"
        : "text-emerald-800 dark:text-emerald-300";
    } else if (token === "true" || token === "false") {
      cls = "font-semibold text-foreground";
    } else if (token === "null") {
      cls = "text-muted-foreground";
    }
    tokens.push(
      <span key={key++} className={cls}>
        {token}
      </span>
    );
    lastIndex = pattern.lastIndex;
  }
  if (lastIndex < text.length) {
    tokens.push(text.slice(lastIndex));
  }
  return tokens;
}

export default function ApiExplorer({ accessToken, accountLabel }) {
  const [search, setSearch] = useState("");
  const [selectedId, setSelectedId] = useState(TOWER_API_CATALOG[0].id);
  const [pathParams, setPathParams] = useState({});
  const [query, setQuery] = useState("");
  const [body, setBody] = useState("");
  const [manualToken, setManualToken] = useState("");
  const [loading, setLoading] = useState(false);
  const [response, setResponse] = useState(null);
  const [error, setError] = useState("");
  const [copiedKey, setCopiedKey] = useState("");
  const [unavailableMap, setUnavailableMap] = useState({});
  const [paramOptions, setParamOptions] = useState({});
  const [fetchingKey, setFetchingKey] = useState("");
  const [confirmDanger, setConfirmDanger] = useState(false);

  const endpoint = useMemo(
    () => TOWER_API_CATALOG.find((item) => item.id === selectedId) || TOWER_API_CATALOG[0],
    [selectedId]
  );
  const readOnly = endpoint.method === "DELETE" && endpoint.id !== "todos-reopen" && endpoint.id !== "todos-unassign";

  const groupedCatalog = useMemo(() => {
    const q = search.trim().toLowerCase();
    const filtered = q
      ? TOWER_API_CATALOG.filter(
          (item) =>
            item.name.toLowerCase().includes(q) ||
            item.path.toLowerCase().includes(q) ||
            item.group.toLowerCase().includes(q) ||
            item.method.toLowerCase().includes(q)
        )
      : TOWER_API_CATALOG;

    const groups = [];
    for (const item of filtered) {
      let group = groups.find((g) => g.name === item.group);
      if (!group) {
        group = { name: item.group, items: [] };
        groups.push(group);
      }
      group.items.push(item);
    }
    return groups;
  }, [search]);

  const paramNames = useMemo(() => extractPathParams(endpoint.path), [endpoint.path]);
  const effectiveToken = (accessToken || manualToken).trim();
  const accountKey = effectiveToken ? effectiveToken.slice(0, 12) : "anon";
  const currentUnavailable = unavailableMap[`${accountKey}::${endpoint.id}`] || "";

  const getParamValue = useCallback(
    (name) => {
      const direct = pathParams[name];
      if (direct !== undefined) return direct;
      if (name === "id") {
        return pathParams[resolveOptionKey(endpoint.path, name)] || "";
      }
      return "";
    },
    [endpoint.path, pathParams]
  );

  const resolvedPath = useMemo(() => {
    let p = endpoint.path;
    for (const name of paramNames) {
      const val = getParamValue(name).trim();
      if (val) p = p.replace(`{${name}}`, encodeURIComponent(val));
    }
    return p;
  }, [endpoint.path, paramNames, getParamValue]);

  const renameTarget = endpoint.id === "teams-rename" && effectiveToken && !resolvedPath.includes("{") ? resolvedPath : "";
  const [verifiedOwner, setVerifiedOwner] = useState(null);
  useEffect(() => {
    let active = true;
    setVerifiedOwner(null);
    if (renameTarget) {
      fetch("/api/proxy", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ method: "GET", path: renameTarget + "/member", access_token: effectiveToken }),
      }).then(res => res.json()).then(result => {
        if (active && result.body?.data?.attributes?.role === "owner")
          setVerifiedOwner({ token: effectiveToken, target: renameTarget });
      }).catch(() => {});
    }
    return () => { active = false; };
  }, [effectiveToken, renameTarget]);
  const renameLocked = endpoint.id === "teams-rename" &&
    !(verifiedOwner?.target === renameTarget && verifiedOwner?.token === effectiveToken);

  const hasUnfilledParams = paramNames.some((name) => !getParamValue(name).trim());

  useEffect(() => {
    setParamOptions({});
    setPathParams({});
  }, [accountKey]);

  const harvestOptionsFromResponse = useCallback((apiPath, respBody) => {
    const opts = toOptions(respBody);
    if (opts.length === 0) return;
    let targetKey = "";
    if (apiPath === "/api/v1/teams") targetKey = "team_id";
    else if (/^\/api\/v1\/teams\/[^/]+\/projects$/.test(apiPath)) targetKey = "project_id";
    else if (/^\/api\/v1\/(?:teams|projects)\/[^/]+\/members$/.test(apiPath)) targetKey = "member_id";
    else if (/^\/api\/v1\/projects\/[^/]+\/todolists$/.test(apiPath)) targetKey = "todolist_id";
    else if (/^\/api\/v1\/todolists\/[^/]+\/todos$/.test(apiPath)) targetKey = "todo_id";
    else if (/^\/api\/v1\/projects\/[^/]+\/topics$/.test(apiPath)) targetKey = "topic_id";
    else if (/^\/api\/v1\/projects\/[^/]+\/uploads$/.test(apiPath)) targetKey = "upload_id";
    else if (/^\/api\/v1\/(?:projects|todos)\/[^/]+\/time_logs$/.test(apiPath)) targetKey = "time_log_id";
    if (!targetKey) return;

    setParamOptions((prev) => ({ ...prev, [targetKey]: opts }));
    if (opts.length === 1) {
      setPathParams((prev) => (prev[targetKey] ? prev : { ...prev, [targetKey]: opts[0].id }));
    }
  }, []);

  const fetchOptionList = useCallback(
    async (targetKey, apiPath) => {
      if (!effectiveToken || !apiPath) return;
      setFetchingKey(targetKey);
      try {
        const res = await fetch("/api/proxy", {
          method: "POST",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({
            method: "GET",
            path: apiPath,
            query: apiPath === "/api/v1/teams" ? "" : "page[number]=1&page[size]=50",
            body: "",
            access_token: effectiveToken,
          }),
        });
        const data = await res.json();
        if (res.ok && data.status >= 200 && data.status < 300) {
          harvestOptionsFromResponse(apiPath, data.body);
        }
      } catch {
        // ignore background option fetch failures
      } finally {
        setFetchingKey((prev) => (prev === targetKey ? "" : prev));
      }
    },
    [effectiveToken, harvestOptionsFromResponse]
  );

  useEffect(() => {
    if (!effectiveToken) return;
    fetchOptionList("team_id", "/api/v1/teams");
  }, [effectiveToken, fetchOptionList]);

  useEffect(() => {
    const teamId = (pathParams.team_id || "").trim();
    if (!effectiveToken || !teamId) return;
    fetchOptionList("project_id", `/api/v1/teams/${encodeURIComponent(teamId)}/projects`);
    fetchOptionList("member_id", `/api/v1/teams/${encodeURIComponent(teamId)}/members`);
  }, [effectiveToken, pathParams.team_id, fetchOptionList]);

  useEffect(() => {
    const projectId = (pathParams.project_id || "").trim();
    if (!effectiveToken || !projectId) return;
    fetchOptionList("todolist_id", `/api/v1/projects/${encodeURIComponent(projectId)}/todolists`);
  }, [effectiveToken, pathParams.project_id, fetchOptionList]);

  function selectEndpoint(item) {
    setSelectedId(item.id);
    setBody(item.defaultBody || "");
    setQuery(defaultQueryForEndpoint(item));
    setError("");
    setResponse(null);
    setConfirmDanger(false);
  }

  async function executeSend() {
    if (readOnly || renameLocked) return;
    if (!effectiveToken) {
      setError("请先登录账号获取 Access Token，或在下方填入临时 Token");
      return;
    }
    if (hasUnfilledParams) {
      setError("请先填写路径参数（如 team_id / project_id 等）");
      return;
    }
    setLoading(true);
    setError("");
    try {
      const res = await fetch("/api/proxy", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          method: endpoint.method,
          path: resolvedPath,
          query: query.trim(),
          body: endpoint.method === "GET" || endpoint.method === "DELETE" ? "" : body,
          access_token: effectiveToken,
        }),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        setError(data.error || `HTTP ${res.status}`);
        setResponse(null);
        return;
      }
      const mapKey = `${accountKey}::${endpoint.id}`;
      if (data.status === 401) {
        setUnavailableMap((prev) => ({
          ...prev,
          [mapKey]: "未授权 (401)",
        }));
      } else if (data.status >= 200 && data.status < 300) {
        setUnavailableMap((prev) => {
          if (!prev[mapKey]) return prev;
          const next = { ...prev };
          delete next[mapKey];
          return next;
        });
        harvestOptionsFromResponse(resolvedPath, data.body);
      }
      setResponse(data);
    } catch (err) {
      setError(`请求失败: ${err.message}`);
      setResponse(null);
    } finally {
      setLoading(false);
    }
  }

  function handleSend() {
    if (!effectiveToken) {
      setError("请先登录账号获取 Access Token，或在下方填入临时 Token");
      return;
    }
    if (hasUnfilledParams) {
      setError("请先填写路径参数（如 team_id / project_id 等）");
      return;
    }
    if (endpoint.method === "DELETE") {
      setConfirmDanger(true);
      return;
    }
    executeSend();
  }

  async function copyText(key, text) {
    await navigator.clipboard.writeText(text);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(""), 1200);
  }

  const curlCommand = useMemo(() => {
    const fullURL = `https://tower.im${resolvedPath}${query.trim() ? `?${query.trim().replace(/^\?/, "")}` : ""}`;
    const lines = [`curl -X ${endpoint.method} "${fullURL}"`];
    lines.push(`  -H "Authorization: Bearer ${effectiveToken || "<ACCESS_TOKEN>"}"`);
    if (endpoint.method !== "GET" && endpoint.method !== "DELETE" && body.trim()) {
      lines.push(`  -H "Content-Type: application/json"`);
      lines.push(`  -d '${body.replace(/\n/g, " ")}'`);
    }
    return lines.join(" \\\n");
  }, [endpoint.method, resolvedPath, query, effectiveToken, body]);

  const formattedResponse = useMemo(() => {
    if (!response) return "";
    return typeof response.body === "string"
      ? response.body
      : JSON.stringify(response.body, null, 2);
  }, [response]);

  return (
    <div className="flex-1 min-h-0 flex flex-col lg:grid lg:grid-cols-12">
      <aside className="lg:col-span-4 xl:col-span-3 flex flex-col min-h-0 border-b lg:border-b-0 lg:border-r bg-card">
        <div className="shrink-0 p-3.5 border-b space-y-2.5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
              Tower API 目录
            </h2>
            <Badge variant="secondary" className="font-mono tabular-nums">
              {TOWER_API_CATALOG.length}
            </Badge>
          </div>
          <Input
            type="text"
            aria-label="搜索 Tower API 接口"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索接口名称、路径或分组..."
            className="text-xs"
          />
        </div>
        <div className="max-h-64 lg:max-h-none lg:flex-1 min-h-0 overflow-y-auto p-2.5 space-y-3">
          {groupedCatalog.map((group) => (
            <div key={group.name} className="space-y-1">
              <div className="sticky top-0 z-10 bg-card/95 px-2 py-1 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                {group.name}
              </div>
              {group.items.map((item) => {
                const active = item.id === endpoint.id;
                const unavail = unavailableMap[`${accountKey}::${item.id}`];
                return (
                  <button
                    key={item.id}
                    type="button"
                    onClick={() => selectEndpoint(item)}
                    className={`w-full text-left rounded-lg border px-2.5 py-2 transition-colors cursor-pointer ${
                      active
                        ? "border-foreground bg-muted/70"
                        : "border-transparent hover:bg-muted/40"
                    } ${unavail ? "opacity-65" : ""}`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2 min-w-0">
                        <Badge
                          variant="outline"
                          className={`rounded px-1.5 py-0 font-mono text-[10px] font-semibold shrink-0 ${
                            METHOD_BADGE[item.method] || ""
                          }`}
                        >
                          {item.method}
                        </Badge>
                        <span className="truncate text-xs font-medium text-foreground">
                          {item.name}
                        </span>
                      </div>
                      {unavail && (
                        <Badge variant="outline" className={`shrink-0 ${TONE_BADGE.danger}`}>
                          不可用
                        </Badge>
                      )}
                    </div>
                    <div className="mt-1 truncate font-mono text-[11px] text-muted-foreground">
                      {item.path}
                    </div>
                  </button>
                );
              })}
            </div>
          ))}
          {groupedCatalog.length === 0 && (
            <p className="py-6 text-center text-xs text-muted-foreground">没有匹配的接口</p>
          )}
        </div>
      </aside>

      <section className="lg:col-span-8 xl:col-span-9 min-w-0 min-h-0 overflow-y-auto bg-background p-4 sm:p-6 space-y-4">
        <Card>
          <CardHeader className="border-b">
            <div className="flex flex-wrap items-center gap-2">
              <Badge
                variant="outline"
                className={`rounded px-2 font-mono text-[11px] font-semibold ${
                  METHOD_BADGE[endpoint.method] || ""
                }`}
              >
                {endpoint.method}
              </Badge>
              <CardTitle className="text-sm">{endpoint.name}</CardTitle>
              {currentUnavailable && (
                <Badge variant="outline" className={TONE_BADGE.danger}>
                  当前账号不可用 · {currentUnavailable}
                </Badge>
              )}
            </div>
            <CardDescription className="break-all font-mono text-xs">
              https://tower.im{readOnly ? endpoint.path : resolvedPath}
              {!readOnly && query.trim() ? `?${query.trim().replace(/^\?/, "")}` : ""}
            </CardDescription>
            {!readOnly && (
              <CardAction>
                {accessToken ? (
                  <Badge variant="outline" className={TONE_BADGE.success}>
                    <span className="size-1.5 rounded-full bg-emerald-500" />
                    使用账号：{accountLabel}
                  </Badge>
                ) : (
                  <Badge variant="outline" className={TONE_BADGE.warning}>
                    <span className="size-1.5 rounded-full bg-amber-500" />
                    未登录，可下方填入 Token
                  </Badge>
                )}
              </CardAction>
            )}
          </CardHeader>

          <CardContent className="space-y-4">
            {readOnly ? (
              <StatusBanner tone="warning">
                此删除接口仅供查看，调试工具已禁用请求发送。
              </StatusBanner>
            ) : (
              <>
                {currentUnavailable && (
                  <StatusBanner tone="error">
                    当前账号对该接口 {currentUnavailable}，已标记为不可用；可切换其他有权限的账号或重新授权后再试。
                  </StatusBanner>
                )}

                {!accessToken && (
                  <div className="space-y-1.5">
                    <Label htmlFor="api-manual-token" className="text-xs">
                      临时 Access Token
                    </Label>
                    <Input
                      id="api-manual-token"
                      type="password"
                      value={manualToken}
                      onChange={(event) => setManualToken(event.target.value)}
                      placeholder="输入 Bearer Access Token"
                      className="font-mono text-xs"
                    />
                  </div>
                )}

                {paramNames.length > 0 && (
                  <div className="grid grid-cols-1 sm:grid-cols-2 gap-3.5">
                    {paramNames.map((name) => {
                      const optionKey = resolveOptionKey(endpoint.path, name);
                      const options = paramOptions[optionKey] || [];
                      const currentVal = getParamValue(name);
                      const selectedOption = options.find((opt) => opt.id === currentVal);
                      const canLoadTodo = optionKey === "todo_id" && (pathParams.todolist_id || "").trim();
                      const canLoadTopics = optionKey === "topic_id" && (pathParams.project_id || "").trim();
                      const canLoadUploads = optionKey === "upload_id" && (pathParams.project_id || "").trim();
                      const manualLoadPath =
                        optionKey === "team_id"
                          ? "/api/v1/teams"
                          : canLoadTodo
                            ? `/api/v1/todolists/${encodeURIComponent(pathParams.todolist_id.trim())}/todos`
                            : canLoadTopics
                              ? `/api/v1/projects/${encodeURIComponent(pathParams.project_id.trim())}/topics`
                              : canLoadUploads
                                ? `/api/v1/projects/${encodeURIComponent(pathParams.project_id.trim())}/uploads`
                                : "";

                      return (
                        <div key={name} className="space-y-1.5">
                          <div className="flex items-center justify-between gap-2">
                            <Label className="text-xs">
                              路径参数 <code className="font-mono">{`{${name}}`}</code>
                            </Label>
                            {manualLoadPath && (
                              <Button
                                type="button"
                                variant="link"
                                size="xs"
                                disabled={fetchingKey === optionKey || !effectiveToken}
                                onClick={() => fetchOptionList(optionKey, manualLoadPath)}
                                className="h-auto p-0 text-[11px]"
                              >
                                {fetchingKey === optionKey ? "加载中..." : "刷新列表"}
                              </Button>
                            )}
                          </div>
                          {options.length > 0 && (
                            <Select
                              value={selectedOption ? currentVal : "__empty__"}
                              onValueChange={(val) => {
                                const next = val === "__empty__" ? "" : val;
                                setPathParams((prev) => ({
                                  ...prev,
                                  [name]: next,
                                  ...(name === "id" ? { [optionKey]: next } : {}),
                                }));
                              }}
                            >
                              <SelectTrigger aria-label={`选择 ${name}`} className="w-full text-xs">
                                <SelectValue>
                                  {selectedOption
                                    ? `${selectedOption.label} (${selectedOption.id})`
                                    : `选择或下方手动输入（共 ${options.length} 项）`}
                                </SelectValue>
                              </SelectTrigger>
                              <SelectContent>
                                <SelectItem value="__empty__">
                                  选择或下方手动输入（共 {options.length} 项）
                                </SelectItem>
                                {options.map((opt) => (
                                  <SelectItem key={opt.id} value={opt.id}>
                                    {opt.label} ({opt.id})
                                  </SelectItem>
                                ))}
                              </SelectContent>
                            </Select>
                          )}
                          <Input
                            type="text"
                            value={currentVal}
                            onChange={(event) => {
                              const val = event.target.value;
                              setPathParams((prev) => ({
                                ...prev,
                                [name]: val,
                                ...(name === "id" ? { [optionKey]: val } : {}),
                              }));
                            }}
                            placeholder={`输入 ${name}`}
                            className="font-mono text-xs"
                          />
                        </div>
                      );
                    })}
                  </div>
                )}

                <div className="space-y-1.5">
                  <Label htmlFor="api-query" className="text-xs">
                    Query 参数（可选，列表接口默认限制每页 20 条、最大 50 条）
                  </Label>
                  <Input
                    id="api-query"
                    type="text"
                    value={query}
                    onChange={(event) => setQuery(event.target.value)}
                    placeholder="例如: page[number]=1&page[size]=20"
                    className="font-mono text-xs"
                  />
                </div>

                {endpoint.method !== "GET" && endpoint.method !== "DELETE" && (
                  <div className="space-y-1.5">
                    <Label htmlFor="api-body" className="text-xs">
                      请求体 JSON
                    </Label>
                    <Textarea
                      id="api-body"
                      rows={5}
                      value={body}
                      onChange={(event) => setBody(event.target.value)}
                      placeholder="{}"
                      className="font-mono text-xs"
                    />
                  </div>
                )}

                <div className="flex flex-wrap items-center gap-2 pt-1">
                  <Button
                    type="button"
                    variant={endpoint.method === "DELETE" ? "destructive" : "default"}
                    disabled={loading || renameLocked}
                    onClick={handleSend}
                  >
                    {renameLocked ? "仅团队超级管理员可执行" : loading ? "请求中..." : "发送请求"}
                  </Button>
                  <Button type="button" variant="outline" onClick={() => copyText("curl", curlCommand)}>
                    {copiedKey === "curl" ? <CheckIcon /> : <CopyIcon />}
                    {copiedKey === "curl" ? "已复制 curl" : "复制 curl"}
                  </Button>
                  {endpoint.method === "DELETE" && (
                    <span className="text-xs text-destructive">
                      注意：DELETE 请求会操作真实 Tower 数据
                    </span>
                  )}
                </div>

                {error && <StatusBanner tone="error">{error}</StatusBanner>}
              </>
            )}
          </CardContent>
        </Card>

        {!readOnly && response && (
          <Card>
            <CardHeader className="border-b">
              <div className="flex items-center gap-2 text-xs">
                <Badge
                  variant="outline"
                  className={`font-mono font-semibold tabular-nums ${
                    response.status >= 200 && response.status < 300 ? TONE_BADGE.success : TONE_BADGE.danger
                  }`}
                >
                  {response.status_text || response.status}
                </Badge>
                <span className="font-mono tabular-nums text-muted-foreground">
                  {response.duration_ms} ms
                </span>
              </div>
              <CardAction>
                <Button type="button" variant="outline" size="sm" onClick={() => copyText("resp", formattedResponse)}>
                  {copiedKey === "resp" ? <CheckIcon /> : <CopyIcon />}
                  {copiedKey === "resp" ? "已复制" : "复制响应"}
                </Button>
              </CardAction>
            </CardHeader>
            <CardContent>
              <pre className="min-h-[200px] max-h-[55vh] overflow-y-auto break-all whitespace-pre-wrap rounded-lg border bg-muted/40 p-3.5 font-mono text-xs leading-relaxed text-foreground">
                {typeof response.body === "string" ? (
                  formattedResponse
                ) : (
                  <HighlightedJSON text={formattedResponse} />
                )}
              </pre>
            </CardContent>
          </Card>
        )}
      </section>

      <ConfirmDialog
        open={!readOnly && confirmDanger}
        onOpenChange={setConfirmDanger}
        title="确认执行危险操作"
        confirmLabel="确认发送"
        confirmVariant="destructive"
        onConfirm={() => {
          setConfirmDanger(false);
          executeSend();
        }}
      >
        <div className="space-y-2.5 text-xs leading-relaxed text-muted-foreground">
          <p>
            即将使用账号「{accountLabel}」向 Tower 发送{" "}
            <span className="font-mono font-semibold text-destructive">{endpoint.method}</span>{" "}
            请求（{endpoint.name}）：
          </p>
          <div className="break-all rounded-lg border bg-muted/40 px-3 py-2 font-mono text-xs text-foreground">
            https://tower.im{resolvedPath}
            {query.trim() ? `?${query.trim().replace(/^\?/, "")}` : ""}
          </div>
          <p className="text-destructive">
            该操作会直接删除或修改真实 Tower 数据，请确认目标 ID 无误后再继续。
          </p>
        </div>
      </ConfirmDialog>
    </div>
  );
}
