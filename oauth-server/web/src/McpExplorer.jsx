import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { HighlightedJSON, toOptions } from "./ApiExplorer.jsx";

function resolveToolGroup(name) {
  if (name === "tower_get_current_user") return "用户";
  if (name.startsWith("tower_") && name.includes("_team") && !name.includes("member")) return "团队";
  if (name.includes("_project") && !name.includes("time_log")) return "项目";
  if (name.includes("_member")) return "成员";
  if (name.includes("_todolist")) return "任务清单";
  if (name.includes("_todo") && !name.includes("time_log")) return "任务";
  if (name.includes("_topic")) return "讨论";
  if (name.includes("_upload") || name.includes("attachment")) return "文件与直传";
  if (name.includes("time_log")) return "工时";
  if (name.includes("_events") || name.includes("_notifications")) return "动态与通知";
  return "通用扩展";
}

function tryParseJSON(text) {
  if (typeof text !== "string") return { ok: false, val: text };
  try {
    return { ok: true, val: JSON.parse(text) };
  } catch {
    return { ok: false, val: text };
  }
}

export default function McpExplorer({ mcpKey, mcpServerURL, accountLabel, onGoCreds }) {
  const [manualKey, setManualKey] = useState("");
  const [tools, setTools] = useState([]);
  const [serverMeta, setServerMeta] = useState(null);
  const [connecting, setConnecting] = useState(false);
  const [connectError, setConnectError] = useState("");
  const [pingLatency, setPingLatency] = useState(null);

  const [search, setSearch] = useState("");
  const [modeFilter, setModeFilter] = useState("all"); // all | read | write | danger
  const [selectedName, setSelectedName] = useState("tower_get_current_user");

  const [inputMode, setInputMode] = useState("form"); // form | json
  const [formValues, setFormValues] = useState({});
  const [rawArgsText, setRawArgsText] = useState("{}");
  const [paramOptions, setParamOptions] = useState({});
  const [fetchingOpt, setFetchingOpt] = useState("");

  const [calling, setCalling] = useState(false);
  const [callError, setCallError] = useState("");
  const [resultView, setResultView] = useState("content"); // content | rpc
  const [callResult, setCallResult] = useState(null);
  const [unavailableTools, setUnavailableTools] = useState({});
  const [confirmDanger, setConfirmDanger] = useState(false);
  const [copiedKey, setCopiedKey] = useState("");
  const rpcIdRef = useRef(1);

  const effectiveKey = (mcpKey || manualKey).trim();
  const keyTag = effectiveKey ? effectiveKey.slice(0, 14) : "none";

  const sendRPC = useCallback(
    async (method, params, sessionIdOverride) => {
      const id = rpcIdRef.current++;
      const reqEnvelope = {
        jsonrpc: "2.0",
        id,
        method,
        ...(params !== undefined ? { params } : {}),
      };
      const headers = {
        "Content-Type": "application/json",
        Accept: "application/json, text/event-stream",
        AGENTS_TOWER_API_KEY: effectiveKey,
      };
      const sid = sessionIdOverride || serverMeta?.sessionId;
      if (sid) headers["Mcp-Session-Id"] = sid;

      const started = performance.now();
      const res = await fetch(mcpServerURL, {
        method: "POST",
        headers,
        body: JSON.stringify(reqEnvelope),
      });
      const durationMs = Math.round(performance.now() - started);
      const sessionId = res.headers.get("Mcp-Session-Id") || sid || "";
      const respEnvelope = await res.json();
      if (!res.ok || respEnvelope.error) {
        throw new Error(respEnvelope.error?.message || `HTTP ${res.status}`);
      }
      return { reqEnvelope, respEnvelope, result: respEnvelope.result, durationMs, sessionId };
    },
    [effectiveKey, mcpServerURL, serverMeta?.sessionId]
  );

  const harvestFromToolOutput = useCallback((toolName, parsedBody) => {
    const opts = toOptions(parsedBody);
    if (opts.length === 0) return;
    let targetKey = "";
    if (toolName === "tower_list_teams") targetKey = "team_id";
    else if (toolName === "tower_list_projects") targetKey = "project_id";
    else if (toolName === "tower_list_team_members" || toolName === "tower_list_project_members") targetKey = "member_id";
    else if (toolName === "tower_list_todolists") targetKey = "todolist_id";
    else if (toolName.startsWith("tower_list_") && toolName.endsWith("_todos")) targetKey = "todo_id";
    else if (toolName === "tower_list_topics") targetKey = "topic_id";
    else if (toolName === "tower_list_uploads") targetKey = "upload_id";
    else if (toolName.endsWith("_time_logs")) targetKey = "time_log_id";
    if (!targetKey) return;

    setParamOptions((prev) => ({ ...prev, [targetKey]: opts }));
    if (opts.length === 1) {
      setFormValues((prev) => (prev[targetKey] ? prev : { ...prev, [targetKey]: opts[0].id }));
    }
  }, []);

  const fetchToolOptions = useCallback(
    async (targetKey, toolName, toolArgs = {}) => {
      if (!effectiveKey) return;
      setFetchingOpt(targetKey);
      try {
        const { result } = await sendRPC("tools/call", {
          name: toolName,
          arguments: { ...toolArgs, page_size: 50 },
        });
        if (!result?.isError && result?.content?.[0]?.text) {
          const parsed = tryParseJSON(result.content[0].text);
          if (parsed.ok) harvestFromToolOutput(toolName, parsed.val);
        }
      } catch {
        // ignore silent option preload errors
      } finally {
        setFetchingOpt((prev) => (prev === targetKey ? "" : prev));
      }
    },
    [effectiveKey, sendRPC, harvestFromToolOutput]
  );

  const connectAndLoadTools = useCallback(async () => {
    if (!effectiveKey) {
      setTools([]);
      setServerMeta(null);
      setConnectError("");
      return;
    }
    setConnecting(true);
    setConnectError("");
    try {
      const initOut = await sendRPC(
        "initialize",
        {
          protocolVersion: "2025-03-26",
          capabilities: {},
          clientInfo: { name: "AgentsTower-WebInspector", version: "1.0.0" },
        },
        ""
      );
      fetch(mcpServerURL, {
        method: "POST",
        headers: {
          "Content-Type": "application/json",
          AGENTS_TOWER_API_KEY: effectiveKey,
          ...(initOut.sessionId ? { "Mcp-Session-Id": initOut.sessionId } : {}),
        },
        body: JSON.stringify({ jsonrpc: "2.0", method: "notifications/initialized" }),
      }).catch(() => {});

      const listOut = await sendRPC("tools/list", {}, initOut.sessionId);
      const loadedTools = listOut.result?.tools || [];
      setServerMeta({
        name: initOut.result?.serverInfo?.name || "AgentsTower",
        version: initOut.result?.serverInfo?.version || "1.0.0",
        protocolVersion: initOut.result?.protocolVersion || "2025-03-26",
        instructions: initOut.result?.instructions || "",
        sessionId: initOut.sessionId,
        handshakeMs: initOut.durationMs,
      });
      setTools(loadedTools);
      if (loadedTools.length > 0 && !loadedTools.some((t) => t.name === selectedName)) {
        setSelectedName(loadedTools[0].name);
      }
    } catch (err) {
      setConnectError(err.message);
      setServerMeta(null);
      setTools([]);
    } finally {
      setConnecting(false);
    }
  }, [effectiveKey, mcpServerURL]);

  useEffect(() => {
    setParamOptions({});
    setFormValues({});
    setCallResult(null);
    setPingLatency(null);
    connectAndLoadTools();
  }, [keyTag, connectAndLoadTools]);

  useEffect(() => {
    if (!effectiveKey || !serverMeta) return;
    fetchToolOptions("team_id", "tower_list_teams");
  }, [effectiveKey, Boolean(serverMeta), fetchToolOptions]);

  useEffect(() => {
    const teamId = String(formValues.team_id || "").trim();
    if (!effectiveKey || !serverMeta || !teamId) return;
    fetchToolOptions("project_id", "tower_list_projects", { team_id: teamId });
    fetchToolOptions("member_id", "tower_list_team_members", { team_id: teamId });
  }, [effectiveKey, Boolean(serverMeta), formValues.team_id, fetchToolOptions]);

  useEffect(() => {
    const projectId = String(formValues.project_id || "").trim();
    if (!effectiveKey || !serverMeta || !projectId) return;
    fetchToolOptions("todolist_id", "tower_list_todolists", { project_id: projectId });
  }, [effectiveKey, Boolean(serverMeta), formValues.project_id, fetchToolOptions]);

  const activeTool = useMemo(
    () => tools.find((item) => item.name === selectedName) || tools[0] || null,
    [tools, selectedName]
  );

  const groupedTools = useMemo(() => {
    const q = search.trim().toLowerCase();
    const filtered = tools.filter((tool) => {
      const ann = tool.annotations || {};
      const isDanger = Boolean(ann.destructiveHint && tool.name !== "tower_raw_request");
      const isRead = Boolean(ann.readOnlyHint);
      if (modeFilter === "read" && !isRead) return false;
      if (modeFilter === "write" && (isRead || isDanger)) return false;
      if (modeFilter === "danger" && !isDanger) return false;
      if (!q) return true;
      const group = resolveToolGroup(tool.name).toLowerCase();
      return (
        tool.name.toLowerCase().includes(q) ||
        (ann.title || "").toLowerCase().includes(q) ||
        (tool.description || "").toLowerCase().includes(q) ||
        group.includes(q)
      );
    });

    const groups = [];
    for (const tool of filtered) {
      const gName = resolveToolGroup(tool.name);
      let group = groups.find((g) => g.name === gName);
      if (!group) {
        group = { name: gName, items: [] };
        groups.push(group);
      }
      group.items.push(tool);
    }
    return groups;
  }, [tools, search, modeFilter]);

  const schemaEntries = useMemo(() => {
    if (!activeTool?.inputSchema?.properties) return [];
    const reqSet = new Set(activeTool.inputSchema.required || []);
    return Object.entries(activeTool.inputSchema.properties).map(([key, spec]) => ({
      name: key,
      type: spec.type || "string",
      description: spec.description || "",
      required: reqSet.has(key),
      itemsType: spec.items?.type || "string",
    }));
  }, [activeTool]);

  function buildArgumentsObject() {
    if (inputMode === "json") {
      const parsed = tryParseJSON(rawArgsText.trim() || "{}");
      if (!parsed.ok || typeof parsed.val !== "object" || Array.isArray(parsed.val) || parsed.val === null) {
        throw new Error("Arguments JSON 必须是一个有效的 JSON 对象");
      }
      return parsed.val;
    }
    const args = {};
    for (const field of schemaEntries) {
      const raw = formValues[field.name];
      if (raw === undefined || raw === "") {
        if (field.required) {
          throw new Error(`请填写必填参数：${field.name}`);
        }
        continue;
      }
      if (field.type === "integer") {
        const num = Number.parseInt(String(raw), 10);
        if (Number.isNaN(num)) throw new Error(`参数 ${field.name} 需要填写整数`);
        args[field.name] = num;
      } else if (field.type === "number") {
        const num = Number(raw);
        if (Number.isNaN(num)) throw new Error(`参数 ${field.name} 需要填写数字`);
        args[field.name] = num;
      } else if (field.type === "boolean") {
        if (raw === "true" || raw === true) args[field.name] = true;
        else if (raw === "false" || raw === false) args[field.name] = false;
      } else if (field.type === "array" || field.type === "object") {
        const parsed = tryParseJSON(String(raw).trim());
        if (!parsed.ok) {
          throw new Error(`参数 ${field.name} 需要合法的 JSON ${field.type === "array" ? "数组" : "对象"}`);
        }
        args[field.name] = parsed.val;
      } else {
        const trimmed = String(raw).trim();
        if (field.required && !trimmed) throw new Error(`请填写必填参数：${field.name}`);
        if (trimmed) args[field.name] = trimmed;
      }
    }
    return args;
  }

  function selectTool(tool) {
    setSelectedName(tool.name);
    setCallError("");
    setCallResult(null);
    setConfirmDanger(false);
    if (inputMode === "json") {
      const nextObj = {};
      const props = tool.inputSchema?.properties || {};
      for (const key of Object.keys(props)) {
        if (formValues[key] !== undefined && formValues[key] !== "") {
          nextObj[key] = formValues[key];
        }
      }
      setRawArgsText(JSON.stringify(nextObj, null, 2));
    }
  }

  function switchInputMode(nextMode) {
    if (nextMode === inputMode) return;
    if (nextMode === "json") {
      try {
        const built = buildArgumentsObject();
        setRawArgsText(JSON.stringify(built, null, 2));
      } catch {
        const draft = {};
        for (const field of schemaEntries) {
          if (formValues[field.name] !== undefined && formValues[field.name] !== "") {
            draft[field.name] = formValues[field.name];
          }
        }
        setRawArgsText(JSON.stringify(draft, null, 2));
      }
    } else {
      const parsed = tryParseJSON(rawArgsText);
      if (parsed.ok && parsed.val && typeof parsed.val === "object" && !Array.isArray(parsed.val)) {
        const nextForm = { ...formValues };
        for (const [k, v] of Object.entries(parsed.val)) {
          nextForm[k] = typeof v === "object" && v !== null ? JSON.stringify(v) : String(v);
        }
        setFormValues(nextForm);
      }
    }
    setInputMode(nextMode);
  }

  const isActiveToolDangerous = useMemo(() => {
    if (!activeTool) return false;
    if (activeTool.name === "tower_raw_request") {
      if (inputMode === "json") {
        const parsed = tryParseJSON(rawArgsText);
        return String(parsed.val?.method || "").toUpperCase() === "DELETE";
      }
      return String(formValues.method || "").toUpperCase() === "DELETE";
    }
    return Boolean(activeTool.annotations?.destructiveHint);
  }, [activeTool, inputMode, rawArgsText, formValues.method]);

  async function executeToolCall(argsObj) {
    if (!activeTool) return;
    setCalling(true);
    setCallError("");
    try {
      const out = await sendRPC("tools/call", {
        name: activeTool.name,
        arguments: argsObj,
      });
      const text = out.result?.content?.[0]?.text || "";
      const isErr = Boolean(out.result?.isError);
      const unavailKey = `${keyTag}::${activeTool.name}`;
      if (isErr && (text.includes("HTTP 401") || text.includes("HTTP 403"))) {
        setUnavailableTools((prev) => ({
          ...prev,
          [unavailKey]: text.includes("HTTP 401") ? "未授权 (401)" : "无权限 (403)",
        }));
      } else if (!isErr) {
        setUnavailableTools((prev) => {
          if (!prev[unavailKey]) return prev;
          const next = { ...prev };
          delete next[unavailKey];
          return next;
        });
        const parsed = tryParseJSON(text);
        if (parsed.ok) harvestFromToolOutput(activeTool.name, parsed.val);
      }
      setCallResult({
        isError: isErr,
        text,
        durationMs: out.durationMs,
        reqEnvelope: out.reqEnvelope,
        respEnvelope: out.respEnvelope,
      });
    } catch (err) {
      setCallError(err.message);
      setCallResult(null);
    } finally {
      setCalling(false);
    }
  }

  function handleInvokeClick() {
    if (!effectiveKey) {
      setCallError("请先在凭据中心绑定 Tower 账号生成 MCP 密钥，或在下方填入临时 AGENTS_TOWER_API_KEY");
      return;
    }
    let argsObj;
    try {
      argsObj = buildArgumentsObject();
    } catch (err) {
      setCallError(err.message);
      return;
    }
    if (isActiveToolDangerous) {
      setConfirmDanger(argsObj);
      return;
    }
    executeToolCall(argsObj);
  }

  async function handlePing() {
    if (!effectiveKey) return;
    try {
      const out = await sendRPC("ping");
      setPingLatency(out.durationMs);
    } catch (err) {
      setConnectError(err.message);
    }
  }

  const previewPayload = useMemo(() => {
    let args = {};
    try {
      args = buildArgumentsObject();
    } catch {
      args = inputMode === "json" ? rawArgsText : formValues;
    }
    return {
      jsonrpc: "2.0",
      id: 1,
      method: "tools/call",
      params: {
        name: activeTool?.name || "",
        arguments: args,
      },
    };
  }, [activeTool, formValues, rawArgsText, inputMode, schemaEntries]);

  const mcpCurlCommand = useMemo(() => {
    const bodyStr = JSON.stringify(previewPayload);
    return [
      `curl -X POST '${mcpServerURL.replace(/'/g, "'\\''")}'`,
      `  -H "Content-Type: application/json"`,
      `  -H "AGENTS_TOWER_API_KEY: ${effectiveKey || "<AGENTS_TOWER_API_KEY>"}"`,
      `  -d '${bodyStr.replace(/'/g, "'\\''")}'`,
    ].join(" \\\n");
  }, [previewPayload, effectiveKey, mcpServerURL]);

  async function copyText(key, text) {
    await navigator.clipboard.writeText(text);
    setCopiedKey(key);
    setTimeout(() => setCopiedKey(""), 1200);
  }

  const currentUnavail = activeTool ? unavailableTools[`${keyTag}::${activeTool.name}`] : "";

  const formattedOutput = useMemo(() => {
    if (!callResult) return { isJSON: false, text: "" };
    if (resultView === "rpc") {
      return {
        isJSON: true,
        text: JSON.stringify(
          { request: callResult.reqEnvelope, response: callResult.respEnvelope },
          null,
          2
        ),
      };
    }
    const parsed = tryParseJSON(callResult.text);
    if (parsed.ok) {
      return { isJSON: true, text: JSON.stringify(parsed.val, null, 2) };
    }
    return { isJSON: false, text: callResult.text };
  }, [callResult, resultView]);

  return (
    <div className="flex-1 min-h-0 flex flex-col lg:grid lg:grid-cols-12">
      <aside className="lg:col-span-4 xl:col-span-3 flex flex-col min-h-0 border-b lg:border-b-0 lg:border-r border-slate-200/70 bg-white dark:border-slate-800/80 dark:bg-[#171522]">
        <div className="shrink-0 p-3.5 sm:p-4 border-b border-slate-100 dark:border-slate-800 space-y-2.5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-xs font-semibold uppercase tracking-wider text-slate-500 dark:text-slate-400">
              MCP 工具目录 (tools/list)
            </h2>
            <span className="rounded-full bg-violet-50 px-2 py-0.5 text-[11px] font-medium text-violet-600 dark:bg-violet-950/70 dark:text-violet-300">
              {tools.length}
            </span>
          </div>
          <input
            type="text"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索工具名、标题或功能描述..."
            className="w-full rounded-xl border border-slate-200/90 bg-[#f8f8fc] px-3 py-1.5 text-sm outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
          />
          <div className="flex flex-wrap gap-1">
            {[
              { id: "all", label: "全部" },
              { id: "read", label: "只读" },
              { id: "write", label: "写操作" },
              { id: "danger", label: "危险删除" },
            ].map((tab) => (
              <button
                key={tab.id}
                type="button"
                onClick={() => setModeFilter(tab.id)}
                className={`rounded-full px-2.5 py-0.5 text-[11px] font-medium transition-colors cursor-pointer ${
                  modeFilter === tab.id
                    ? "bg-violet-600 text-white"
                    : "bg-slate-100 text-slate-600 hover:bg-slate-200/70 dark:bg-slate-800 dark:text-slate-300"
                }`}
              >
                {tab.label}
              </button>
            ))}
          </div>
        </div>

        <div className="max-h-64 lg:max-h-none lg:flex-1 min-h-0 overflow-y-auto p-3 space-y-3">
          {connecting ? (
            <p className="py-6 text-center text-xs text-slate-400">正在握手连接 /mcp 并拉取工具列表...</p>
          ) : !effectiveKey ? (
            <div className="py-6 px-3 text-center space-y-2">
              <p className="text-xs text-slate-400">尚未获取到 AGENTS_TOWER_API_KEY</p>
              {onGoCreds && (
                <button
                  type="button"
                  onClick={onGoCreds}
                  className="rounded-full bg-violet-600 px-3.5 py-1.5 text-xs font-medium text-white hover:bg-violet-700 cursor-pointer"
                >
                  前往凭据中心绑定账号
                </button>
              )}
            </div>
          ) : (
            groupedTools.map((group) => (
              <div key={group.name} className="space-y-1">
                <div className="sticky top-0 bg-white/95 py-0.5 text-xs font-semibold text-slate-400 dark:bg-[#171522]/95">
                  {group.name}
                </div>
                {group.items.map((tool) => {
                  const active = activeTool?.name === tool.name;
                  const ann = tool.annotations || {};
                  const isDanger = Boolean(ann.destructiveHint && tool.name !== "tower_raw_request");
                  const isRead = Boolean(ann.readOnlyHint);
                  const unavail = unavailableTools[`${keyTag}::${tool.name}`];
                  const badgeCls = isDanger
                    ? "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
                    : isRead
                      ? "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
                      : "bg-blue-100 text-blue-700 dark:bg-blue-950 dark:text-blue-300";
                  const badgeText = isDanger ? "删除" : isRead ? "只读" : "写入";
                  return (
                    <button
                      key={tool.name}
                      type="button"
                      onClick={() => selectTool(tool)}
                      className={`w-full text-left rounded-xl border px-2.5 py-2 transition-colors cursor-pointer ${
                        active
                          ? "border-violet-500 bg-violet-50/70 dark:bg-violet-950/40"
                          : "border-transparent hover:bg-slate-50 dark:hover:bg-slate-800/60"
                      } ${unavail ? "opacity-65" : ""}`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-1.5 min-w-0">
                          <span className={`rounded px-1.5 py-0.5 text-[10px] font-semibold shrink-0 ${badgeCls}`}>
                            {badgeText}
                          </span>
                          <span className="truncate text-xs font-medium text-slate-800 dark:text-slate-200">
                            {ann.title || tool.name}
                          </span>
                        </div>
                        {unavail && (
                          <span className="shrink-0 rounded bg-red-100 px-1.5 py-0.5 text-[10px] font-medium text-red-700 dark:bg-red-950 dark:text-red-300">
                            不可用
                          </span>
                        )}
                      </div>
                      <div className="mt-1 truncate font-mono text-[11px] text-slate-400 dark:text-slate-500">
                        {tool.name}
                      </div>
                    </button>
                  );
                })}
              </div>
            ))
          )}
          {effectiveKey && !connecting && groupedTools.length === 0 && !connectError && (
            <p className="py-4 text-center text-xs text-slate-400">没有匹配的 MCP 工具</p>
          )}
        </div>
      </aside>

      <section className="lg:col-span-8 xl:col-span-9 min-w-0 min-h-0 overflow-y-auto p-4 sm:p-6 space-y-4">
        <div className="rounded-2xl border border-slate-100/90 bg-white p-4 sm:p-5 shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] space-y-3 dark:border-slate-800/80 dark:bg-[#171522]">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <span
                className={`inline-flex items-center gap-1.5 rounded-full px-2.5 py-1 font-medium ${
                  serverMeta
                    ? "bg-emerald-50 text-emerald-700 border border-emerald-200 dark:bg-emerald-950/60 dark:border-emerald-800 dark:text-emerald-300"
                    : "bg-amber-50 text-amber-700 border border-amber-200 dark:bg-amber-950/60 dark:border-amber-800 dark:text-amber-300"
                }`}
              >
                <span className={`h-1.5 w-1.5 rounded-full ${serverMeta ? "bg-emerald-500" : "bg-amber-500"}`} />
                {serverMeta
                  ? `${serverMeta.name} v${serverMeta.version} · MCP ${serverMeta.protocolVersion}`
                  : "MCP 未连接"}
              </span>
              {mcpKey && (
                <span className="rounded-full border border-slate-200 bg-slate-50 px-2.5 py-1 text-slate-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300">
                  当前绑定账号：{accountLabel}
                </span>
              )}
              {pingLatency !== null && (
                <span className="text-slate-400">ping: {pingLatency} ms</span>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                disabled={!effectiveKey || connecting}
                onClick={handlePing}
                className="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:border-violet-300 hover:text-violet-600 disabled:opacity-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                Ping 检测
              </button>
              <button
                type="button"
                disabled={!effectiveKey || connecting}
                onClick={connectAndLoadTools}
                className="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:border-violet-300 hover:text-violet-600 disabled:opacity-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                {connecting ? "握手中..." : "重新握手"}
              </button>
            </div>
          </div>

          {!mcpKey && (
            <div className="space-y-1 pt-1">
              <label className="text-xs font-medium text-slate-600 dark:text-slate-400">
                临时 AGENTS_TOWER_API_KEY（测试未绑定密钥时使用）
              </label>
              <input
                type="password"
                value={manualKey}
                onChange={(e) => setManualKey(e.target.value)}
                placeholder="输入 at_sk-... 密钥自动握手加载工具"
                className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3 py-2 font-mono text-xs outline-none focus:border-violet-500 dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
              />
            </div>
          )}

          {connectError && (
            <div className="rounded-xl border border-red-200 bg-red-50 px-3.5 py-2.5 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
              MCP 握手失败：{connectError}
            </div>
          )}

          {serverMeta?.instructions && (
            <details className="group text-xs text-slate-500 dark:text-slate-400">
              <summary className="cursor-pointer select-none font-medium hover:text-slate-800 dark:hover:text-slate-200">
                查看服务端下发给 Agent 的 MCP Instructions 说明
              </summary>
              <p className="mt-1.5 rounded-xl border border-slate-100 bg-[#f8f8fc] p-3 leading-relaxed dark:border-slate-800 dark:bg-[#0e0d16]">
                {serverMeta.instructions}
              </p>
            </details>
          )}
        </div>

        {activeTool && (
          <div className="rounded-2xl border border-slate-100/90 bg-white p-4 sm:p-6 shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] space-y-4 dark:border-slate-800/80 dark:bg-[#171522]">
            <div className="flex flex-wrap items-start justify-between gap-3 border-b border-slate-100 pb-3.5 dark:border-slate-800">
              <div className="min-w-0 space-y-1">
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="text-base font-semibold text-slate-900 dark:text-slate-100">
                    {activeTool.annotations?.title || activeTool.name}
                  </h3>
                  <code className="rounded-md bg-violet-50 px-2 py-0.5 font-mono text-xs font-medium text-violet-700 dark:bg-violet-950/70 dark:text-violet-300">
                    {activeTool.name}
                  </code>
                  {currentUnavail && (
                    <span className="rounded-full bg-red-100 px-2 py-0.5 text-[11px] font-medium text-red-700 dark:bg-red-950 dark:text-red-300">
                      当前账号不可用 · {currentUnavail}
                    </span>
                  )}
                </div>
                <p className="text-xs text-slate-500 dark:text-slate-400">
                  {activeTool.description}
                </p>
              </div>

              <div className="inline-flex rounded-full border border-slate-200 bg-slate-100/70 p-0.5 dark:border-slate-800 dark:bg-[#0e0d16]">
                <button
                  type="button"
                  onClick={() => switchInputMode("form")}
                  className={`rounded-full px-3 py-1 text-xs font-medium transition-all cursor-pointer ${
                    inputMode === "form"
                      ? "bg-white text-slate-900 shadow-xs dark:bg-slate-800 dark:text-slate-100"
                      : "text-slate-500 hover:text-slate-900 dark:text-slate-400"
                  }`}
                >
                  Schema 表单
                </button>
                <button
                  type="button"
                  onClick={() => switchInputMode("json")}
                  className={`rounded-full px-3 py-1 text-xs font-medium transition-all cursor-pointer ${
                    inputMode === "json"
                      ? "bg-white text-slate-900 shadow-xs dark:bg-slate-800 dark:text-slate-100"
                      : "text-slate-500 hover:text-slate-900 dark:text-slate-400"
                  }`}
                >
                  JSON 参数
                </button>
              </div>
            </div>

            {inputMode === "json" ? (
              <div className="space-y-1.5">
                <label className="text-xs font-medium text-slate-600 dark:text-slate-400">
                  tools/call arguments (JSON 对象)
                </label>
                <textarea
                  rows={6}
                  value={rawArgsText}
                  onChange={(e) => setRawArgsText(e.target.value)}
                  placeholder="{}"
                  className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] p-3 font-mono text-xs outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
                />
              </div>
            ) : schemaEntries.length === 0 ? (
              <div className="rounded-xl border border-dashed border-slate-200 px-4 py-3 text-xs text-slate-400 dark:border-slate-800">
                该工具无需输入参数，直接点击下方「调用工具」即可执行。
              </div>
            ) : (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3.5">
                {schemaEntries.map((field) => {
                  const val = formValues[field.name] ?? "";
                  const opts = paramOptions[field.name] || [];
                  const isWide = field.type === "object" || field.type === "array" || field.name === "desc" || field.name === "content";
                  const canReloadOpt =
                    field.name === "team_id"
                      ? () => fetchToolOptions("team_id", "tower_list_teams")
                      : field.name === "todo_id" && String(formValues.todolist_id || "").trim()
                        ? () => fetchToolOptions("todo_id", "tower_list_todos", { todolist_id: String(formValues.todolist_id).trim() })
                        : field.name === "topic_id" && String(formValues.project_id || "").trim()
                          ? () => fetchToolOptions("topic_id", "tower_list_topics", { project_id: String(formValues.project_id).trim() })
                          : field.name === "upload_id" && String(formValues.project_id || "").trim()
                            ? () => fetchToolOptions("upload_id", "tower_list_uploads", { project_id: String(formValues.project_id).trim() })
                            : null;

                  return (
                    <div key={field.name} className={`space-y-1.5 ${isWide ? "sm:col-span-2" : ""}`}>
                      <div className="flex items-center justify-between gap-2">
                        <label className="text-xs font-medium text-slate-700 dark:text-slate-300">
                          <code className="font-mono text-violet-600 dark:text-violet-400">{field.name}</code>
                          {field.required && <span className="ml-1 text-red-500">*</span>}
                          <span className="ml-1.5 font-normal text-slate-400">({field.type})</span>
                        </label>
                        {canReloadOpt && (
                          <button
                            type="button"
                            disabled={fetchingOpt === field.name}
                            onClick={canReloadOpt}
                            className="text-[11px] text-violet-600 hover:underline cursor-pointer dark:text-violet-400"
                          >
                            {fetchingOpt === field.name ? "加载中..." : "拉取选项"}
                          </button>
                        )}
                      </div>
                      {field.description && (
                        <div className="text-[11px] text-slate-400">{field.description}</div>
                      )}

                      {opts.length > 0 && field.type === "string" && (
                        <select
                          value={opts.some((o) => o.id === val) ? val : ""}
                          onChange={(e) =>
                            setFormValues((prev) => ({ ...prev, [field.name]: e.target.value }))
                          }
                          className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3 py-1.5 text-xs outline-none focus:border-violet-500 dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
                        >
                          <option value="">下拉选择或下方手动输入（共 {opts.length} 项）</option>
                          {opts.map((o) => (
                            <option key={o.id} value={o.id}>
                              {o.label} ({o.id})
                            </option>
                          ))}
                        </select>
                      )}

                      {field.type === "boolean" ? (
                        <select
                          value={String(val)}
                          onChange={(e) =>
                            setFormValues((prev) => ({ ...prev, [field.name]: e.target.value }))
                          }
                          className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3 py-1.5 font-mono text-xs outline-none focus:border-violet-500 dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
                        >
                          <option value="">默认（不传）</option>
                          <option value="true">true</option>
                          <option value="false">false</option>
                        </select>
                      ) : field.type === "object" || field.type === "array" ? (
                        <textarea
                          rows={3}
                          value={val}
                          onChange={(e) =>
                            setFormValues((prev) => ({ ...prev, [field.name]: e.target.value }))
                          }
                          placeholder={field.type === "array" ? '["id1", "id2"]' : '{"key": "value"}'}
                          className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] p-2.5 font-mono text-xs outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
                        />
                      ) : (
                        <input
                          type={field.type === "integer" || field.type === "number" ? "number" : "text"}
                          value={val}
                          onChange={(e) =>
                            setFormValues((prev) => ({ ...prev, [field.name]: e.target.value }))
                          }
                          placeholder={
                            field.name === "page"
                              ? "默认 1"
                              : field.name === "page_size"
                                ? "默认 20，最大 50"
                                : `输入 ${field.name}`
                          }
                          className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3 py-1.5 font-mono text-xs outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
                        />
                      )}
                    </div>
                  );
                })}
              </div>
            )}

            <div className="flex flex-wrap items-center gap-2 pt-1">
              <button
                type="button"
                disabled={calling || !effectiveKey}
                onClick={handleInvokeClick}
                className={`rounded-full px-5 py-2 text-sm font-medium text-white disabled:opacity-60 cursor-pointer shadow-sm ${
                  isActiveToolDangerous
                    ? "bg-red-600 hover:bg-red-700 shadow-red-600/25"
                    : "bg-violet-600 hover:bg-violet-700 shadow-violet-600/25"
                }`}
              >
                {calling ? "调用中..." : "调用工具 (tools/call)"}
              </button>
              <button
                type="button"
                onClick={() => copyText("rpc", JSON.stringify(previewPayload, null, 2))}
                className="rounded-full border border-slate-200 bg-white px-4 py-2 text-xs font-medium text-slate-700 hover:border-violet-300 hover:text-violet-600 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                {copiedKey === "rpc" ? "已复制 JSON-RPC" : "复制 JSON-RPC"}
              </button>
              <button
                type="button"
                onClick={() => copyText("curl", mcpCurlCommand)}
                className="rounded-full border border-slate-200 bg-white px-4 py-2 text-xs font-medium text-slate-700 hover:border-violet-300 hover:text-violet-600 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                {copiedKey === "curl" ? "已复制 curl" : "复制 /mcp curl"}
              </button>
              {isActiveToolDangerous && (
                <span className="text-xs text-red-600 dark:text-red-400">
                  危险删除工具：调用前将弹出二次确认
                </span>
              )}
            </div>

            {callError && (
              <div className="rounded-xl border border-red-200 bg-red-50 px-3.5 py-2.5 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
                {callError}
              </div>
            )}
          </div>
        )}

        {callResult && (
          <div className="rounded-2xl border border-slate-100/90 bg-white p-4 sm:p-6 shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] space-y-3 dark:border-slate-800/80 dark:bg-[#171522]">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span
                  className={`rounded-full px-2.5 py-0.5 font-mono font-semibold ${
                    callResult.isError
                      ? "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
                      : "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
                  }`}
                >
                  {callResult.isError ? "isError: true" : "isError: false"}
                </span>
                <span className="text-slate-500 dark:text-slate-400">{callResult.durationMs} ms</span>
                <div className="inline-flex rounded-full border border-slate-200 bg-slate-100/70 p-0.5 dark:border-slate-800 dark:bg-[#0e0d16]">
                  <button
                    type="button"
                    onClick={() => setResultView("content")}
                    className={`rounded-full px-2.5 py-0.5 text-[11px] font-medium cursor-pointer ${
                      resultView === "content"
                        ? "bg-white text-slate-900 shadow-xs dark:bg-slate-800 dark:text-slate-100"
                        : "text-slate-500 dark:text-slate-400"
                    }`}
                  >
                    工具返回内容
                  </button>
                  <button
                    type="button"
                    onClick={() => setResultView("rpc")}
                    className={`rounded-full px-2.5 py-0.5 text-[11px] font-medium cursor-pointer ${
                      resultView === "rpc"
                        ? "bg-white text-slate-900 shadow-xs dark:bg-slate-800 dark:text-slate-100"
                        : "text-slate-500 dark:text-slate-400"
                    }`}
                  >
                    JSON-RPC 原始报文
                  </button>
                </div>
              </div>
              <button
                type="button"
                onClick={() => copyText("out", formattedOutput.text)}
                className="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:border-violet-300 hover:text-violet-600 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                {copiedKey === "out" ? "已复制" : "复制结果"}
              </button>
            </div>

            <pre className="min-h-[200px] max-h-[55vh] overflow-y-auto break-all whitespace-pre-wrap rounded-xl border border-slate-200/80 bg-[#f8f8fc] p-3.5 font-mono text-xs leading-relaxed text-slate-800 dark:border-slate-800 dark:bg-[#0e0d16] dark:text-slate-100">
              {formattedOutput.isJSON ? (
                <HighlightedJSON text={formattedOutput.text} />
              ) : (
                formattedOutput.text
              )}
            </pre>
          </div>
        )}
      </section>

      {confirmDanger && activeTool && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 px-4 dark:bg-black/60"
          onClick={() => setConfirmDanger(false)}
        >
          <div
            role="dialog"
            aria-modal="true"
            className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-6 shadow-xl space-y-4 dark:border-slate-700 dark:bg-slate-900"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-base font-semibold text-slate-900 dark:text-slate-100">
              确认执行危险 MCP 工具调用
            </h3>
            <div className="space-y-2 text-sm text-slate-600 dark:text-slate-300">
              <p>
                即将以账号「{accountLabel}」执行{" "}
                <code className="font-mono font-semibold text-red-600 dark:text-red-400">
                  {activeTool.name}
                </code>
                （{activeTool.annotations?.title || "删除操作"}），调用参数：
              </p>
              <pre className="max-h-40 overflow-y-auto break-all whitespace-pre-wrap rounded-lg border border-slate-200 bg-slate-50 px-3 py-2 font-mono text-xs text-slate-800 dark:border-slate-800 dark:bg-slate-950 dark:text-slate-200">
                {JSON.stringify(confirmDanger, null, 2)}
              </pre>
              <p className="text-xs text-red-600 dark:text-red-400">
                该工具会直接删除或变更真实 Tower 数据，请核对参数无误后再继续。
              </p>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={() => setConfirmDanger(false)}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm text-slate-700 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                取消
              </button>
              <button
                type="button"
                onClick={() => {
                  const args = confirmDanger;
                  setConfirmDanger(false);
                  executeToolCall(args);
                }}
                className="rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 cursor-pointer"
              >
                确认调用
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
