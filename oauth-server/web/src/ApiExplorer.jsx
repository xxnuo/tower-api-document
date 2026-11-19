import { useCallback, useEffect, useMemo, useState } from "react";
import { TOWER_API_CATALOG } from "./towerApiCatalog.js";
import { ModalDialog, StatusBanner, UI } from "./ui.jsx";

const METHOD_BADGE = {
  GET: "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  POST: "border-sky-500/25 bg-sky-500/10 text-sky-700 dark:text-sky-300",
  PATCH: "border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  PUT: "border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  DELETE: "border-red-500/25 bg-red-500/10 text-red-700 dark:text-red-300",
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
      cls = "font-semibold text-zinc-900 dark:text-zinc-100";
    } else if (token === "null") {
      cls = "text-zinc-500 dark:text-zinc-400";
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
    if (readOnly) return;
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
      if (data.status === 401 || data.status === 403) {
        setUnavailableMap((prev) => ({
          ...prev,
          [mapKey]: data.status === 401 ? "未授权 (401)" : "无权限 (403)",
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
      <aside className="lg:col-span-4 xl:col-span-3 flex flex-col min-h-0 border-b lg:border-b-0 lg:border-r border-zinc-200 bg-white dark:border-zinc-800 dark:bg-[#111113]">
        <div className="shrink-0 p-3.5 border-b border-zinc-200 dark:border-zinc-800 space-y-2.5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-[11px] font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              Tower API 目录
            </h2>
            <span className={`${UI.badgeNeutral} font-mono tabular-nums`}>
              {TOWER_API_CATALOG.length}
            </span>
          </div>
          <input
            type="text"
            aria-label="搜索 Tower API 接口"
            value={search}
            onChange={(event) => setSearch(event.target.value)}
            placeholder="搜索接口名称、路径或分组..."
            className={UI.inputSm}
          />
        </div>
        <div className="max-h-64 lg:max-h-none lg:flex-1 min-h-0 overflow-y-auto p-2.5 space-y-3">
          {groupedCatalog.map((group) => (
            <div key={group.name} className="space-y-1">
              <div className="sticky top-0 z-10 bg-white/95 px-2 py-1 text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:bg-[#111113]/95 dark:text-zinc-500">
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
                    className={`w-full text-left rounded-md border px-2.5 py-2 transition-colors cursor-pointer ${
                      active
                        ? "border-zinc-900 bg-zinc-100/80 dark:border-zinc-200 dark:bg-zinc-800/70"
                        : "border-transparent hover:bg-zinc-50 dark:hover:bg-zinc-900/60"
                    } ${unavail ? "opacity-65" : ""}`}
                  >
                    <div className="flex items-center justify-between gap-2">
                      <div className="flex items-center gap-2 min-w-0">
                        <span
                          className={`rounded border px-1.5 py-0.5 font-mono text-[10px] font-semibold shrink-0 ${
                            METHOD_BADGE[item.method] || UI.badgeNeutral
                          }`}
                        >
                          {item.method}
                        </span>
                        <span className="truncate text-xs font-medium text-zinc-900 dark:text-zinc-100">
                          {item.name}
                        </span>
                      </div>
                      {unavail && (
                        <span className={UI.badgeDanger}>不可用</span>
                      )}
                    </div>
                    <div className="mt-1 truncate font-mono text-[11px] text-zinc-400 dark:text-zinc-500">
                      {item.path}
                    </div>
                  </button>
                );
              })}
            </div>
          ))}
          {groupedCatalog.length === 0 && (
            <p className="py-6 text-center text-xs text-zinc-400">没有匹配的接口</p>
          )}
        </div>
      </aside>

      <section className="lg:col-span-8 xl:col-span-9 min-w-0 min-h-0 overflow-y-auto bg-[#FAFAFB] p-4 sm:p-6 space-y-4 dark:bg-[#09090B]">
        <div className={`${UI.panel} p-4 sm:p-5 space-y-4`}>
          <div className="flex flex-wrap items-start justify-between gap-3 border-b border-zinc-100 pb-3.5 dark:border-zinc-800/80">
            <div className="min-w-0">
              <div className="flex flex-wrap items-center gap-2">
                <span
                  className={`rounded border px-2 py-0.5 font-mono text-[11px] font-semibold ${
                    METHOD_BADGE[endpoint.method] || UI.badgeNeutral
                  }`}
                >
                  {endpoint.method}
                </span>
                <h2 className="text-sm font-semibold text-zinc-950 dark:text-zinc-100">{endpoint.name}</h2>
                {currentUnavailable && (
                  <span className={UI.badgeDanger}>
                    当前账号不可用 · {currentUnavailable}
                  </span>
                )}
              </div>
              <div className="mt-1.5 break-all font-mono text-xs text-zinc-500 dark:text-zinc-400">
                https://tower.im{readOnly ? endpoint.path : resolvedPath}
                {!readOnly && query.trim() ? `?${query.trim().replace(/^\?/, "")}` : ""}
              </div>
            </div>
            {!readOnly && (
              <div className="text-xs">
                {accessToken ? (
                  <span className={UI.badgeSuccess}>
                    <span className="h-1.5 w-1.5 rounded-full bg-emerald-500" />
                    使用账号：{accountLabel}
                  </span>
                ) : (
                  <span className={UI.badgeWarning}>
                    <span className="h-1.5 w-1.5 rounded-full bg-amber-500" />
                    未登录，可下方填入 Token
                  </span>
                )}
              </div>
            )}
          </div>

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
                  <label htmlFor="api-manual-token" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                    临时 Access Token
                  </label>
                  <input
                    id="api-manual-token"
                    type="password"
                    value={manualToken}
                    onChange={(event) => setManualToken(event.target.value)}
                    placeholder="输入 Bearer Access Token"
                    className={`${UI.inputSm} font-mono`}
                  />
                </div>
              )}

              {paramNames.length > 0 && (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3.5">
                  {paramNames.map((name) => {
                    const optionKey = resolveOptionKey(endpoint.path, name);
                    const options = paramOptions[optionKey] || [];
                    const currentVal = getParamValue(name);
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
                          <label className="text-xs font-medium text-zinc-700 dark:text-zinc-300">
                            路径参数{" "}
                            <code className="font-mono text-zinc-950 dark:text-zinc-100">{`{${name}}`}</code>
                          </label>
                          {manualLoadPath && (
                            <button
                              type="button"
                              disabled={fetchingKey === optionKey || !effectiveToken}
                              onClick={() => fetchOptionList(optionKey, manualLoadPath)}
                              className="text-[11px] font-medium text-zinc-600 underline-offset-2 hover:text-zinc-950 hover:underline disabled:opacity-50 cursor-pointer dark:text-zinc-400 dark:hover:text-zinc-100"
                            >
                              {fetchingKey === optionKey ? "加载中..." : "刷新列表"}
                            </button>
                          )}
                        </div>
                        {options.length > 0 && (
                          <select
                            aria-label={`选择 ${name}`}
                            value={options.some((opt) => opt.id === currentVal) ? currentVal : ""}
                            onChange={(event) => {
                              const val = event.target.value;
                              setPathParams((prev) => ({
                                ...prev,
                                [name]: val,
                                ...(name === "id" ? { [optionKey]: val } : {}),
                              }));
                            }}
                            className={UI.inputSm}
                          >
                            <option value="">选择或下方手动输入（共 {options.length} 项）</option>
                            {options.map((opt) => (
                              <option key={opt.id} value={opt.id}>
                                {opt.label} ({opt.id})
                              </option>
                            ))}
                          </select>
                        )}
                        <input
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
                          className={`${UI.inputSm} font-mono`}
                        />
                      </div>
                    );
                  })}
                </div>
              )}

              <div className="space-y-1.5">
                <label htmlFor="api-query" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  Query 参数（可选，列表接口默认限制每页 20 条、最大 50 条）
                </label>
                <input
                  id="api-query"
                  type="text"
                  value={query}
                  onChange={(event) => setQuery(event.target.value)}
                  placeholder="例如: page[number]=1&page[size]=20"
                  className={`${UI.inputSm} font-mono`}
                />
              </div>

              {endpoint.method !== "GET" && endpoint.method !== "DELETE" && (
                <div className="space-y-1.5">
                  <label htmlFor="api-body" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                    请求体 JSON
                  </label>
                  <textarea
                    id="api-body"
                    rows={5}
                    value={body}
                    onChange={(event) => setBody(event.target.value)}
                    placeholder="{}"
                    className={`${UI.inputSm} font-mono p-2.5`}
                  />
                </div>
              )}

              <div className="flex flex-wrap items-center gap-2 pt-1">
                <button
                  type="button"
                  disabled={loading}
                  onClick={handleSend}
                  className={endpoint.method === "DELETE" ? UI.btnDanger : UI.btnPrimary}
                >
                  {loading ? "请求中..." : "发送请求"}
                </button>
                <button
                  type="button"
                  onClick={() => copyText("curl", curlCommand)}
                  className={UI.btnSecondary}
                >
                  {copiedKey === "curl" ? "已复制 curl" : "复制 curl"}
                </button>
                {endpoint.method === "DELETE" && (
                  <span className="text-xs text-red-600 dark:text-red-400">
                    注意：DELETE 请求会操作真实 Tower 数据
                  </span>
                )}
              </div>

              {error && <StatusBanner tone="error">{error}</StatusBanner>}
            </>
          )}
        </div>

        {!readOnly && response && (
          <div className={`${UI.panel} p-4 sm:p-5 space-y-3`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex items-center gap-2 text-xs">
                <span
                  className={`font-mono font-semibold tabular-nums ${
                    response.status >= 200 && response.status < 300 ? UI.badgeSuccess : UI.badgeDanger
                  }`}
                >
                  {response.status_text || response.status}
                </span>
                <span className="font-mono tabular-nums text-zinc-500 dark:text-zinc-400">
                  {response.duration_ms} ms
                </span>
              </div>
              <button
                type="button"
                onClick={() => copyText("resp", formattedResponse)}
                className={UI.btnSecondary}
              >
                {copiedKey === "resp" ? "已复制" : "复制响应"}
              </button>
            </div>
            <pre className="min-h-[200px] max-h-[55vh] overflow-y-auto break-all whitespace-pre-wrap rounded-md border border-zinc-200 bg-zinc-50/80 p-3.5 font-mono text-xs leading-relaxed text-zinc-800 dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-200">
              {typeof response.body === "string" ? (
                formattedResponse
              ) : (
                <HighlightedJSON text={formattedResponse} />
              )}
            </pre>
          </div>
        )}
      </section>

      <ModalDialog
        open={!readOnly && confirmDanger}
        onClose={() => setConfirmDanger(false)}
        title="确认执行危险操作"
      >
        <div className="space-y-2.5 text-xs leading-relaxed text-zinc-600 dark:text-zinc-300">
          <p>
            即将使用账号「{accountLabel}」向 Tower 发送{" "}
            <span className="font-mono font-semibold text-red-600 dark:text-red-400">
              {endpoint.method}
            </span>{" "}
            请求（{endpoint.name}）：
          </p>
          <div className="break-all rounded-md border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs text-zinc-800 dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-200">
            https://tower.im{resolvedPath}
            {query.trim() ? `?${query.trim().replace(/^\?/, "")}` : ""}
          </div>
          <p className="text-red-600 dark:text-red-400">
            该操作会直接删除或修改真实 Tower 数据，请确认目标 ID 无误后再继续。
          </p>
        </div>
        <div className="flex justify-end gap-2 pt-1">
          <button type="button" onClick={() => setConfirmDanger(false)} className={UI.btnSecondary}>
            取消
          </button>
          <button
            type="button"
            onClick={() => {
              setConfirmDanger(false);
              executeSend();
            }}
            className={UI.btnDanger}
          >
            确认发送
          </button>
        </div>
      </ModalDialog>
    </div>
  );
}
