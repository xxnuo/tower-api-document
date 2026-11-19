import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { HighlightedJSON, toOptions } from "./ApiExplorer.jsx";
import { ModalDialog, SegmentedTabs, StatusBanner, UI } from "./ui.jsx";

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
  const [modeFilter, setModeFilter] = useState("all");
  const [selectedName, setSelectedName] = useState("tower_get_current_user");

  const [inputMode, setInputMode] = useState("form");
  const [formValues, setFormValues] = useState({});
  const [rawArgsText, setRawArgsText] = useState("{}");
  const [paramOptions, setParamOptions] = useState({});
  const [fetchingOpt, setFetchingOpt] = useState("");

  const [calling, setCalling] = useState(false);
  const [callError, setCallError] = useState("");
  const [resultView, setResultView] = useState("content");
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
      <aside className="lg:col-span-4 xl:col-span-3 flex flex-col min-h-0 border-b lg:border-b-0 lg:border-r border-zinc-200 bg-white dark:border-zinc-800 dark:bg-[#111113]">
        <div className="shrink-0 p-3.5 border-b border-zinc-200 dark:border-zinc-800 space-y-2.5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-[11px] font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              MCP 工具目录 (tools/list)
            </h2>
            <span className={`${UI.badgeNeutral} font-mono tabular-nums`}>
              {tools.length}
            </span>
          </div>
          <input
            type="text"
            aria-label="搜索 MCP 工具"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索工具名、标题或功能描述..."
            className={UI.inputSm}
          />
          <SegmentedTabs
            fullWidth
            size="xs"
            value={modeFilter}
            onChange={setModeFilter}
            options={[
              { id: "all", label: "全部" },
              { id: "read", label: "只读" },
              { id: "write", label: "写入" },
              { id: "danger", label: "删除" },
            ]}
          />
        </div>

        <div className="max-h-64 lg:max-h-none lg:flex-1 min-h-0 overflow-y-auto p-2.5 space-y-3">
          {connecting ? (
            <p className="py-6 text-center text-xs text-zinc-400">正在握手连接 /mcp 并拉取工具列表...</p>
          ) : !effectiveKey ? (
            <div className="py-6 px-3 text-center space-y-2.5">
              <p className="text-xs text-zinc-400">尚未获取到 AGENTS_TOWER_API_KEY</p>
              {onGoCreds && (
                <button type="button" onClick={onGoCreds} className={UI.btnPrimary}>
                  前往凭据中心绑定账号
                </button>
              )}
            </div>
          ) : (
            groupedTools.map((group) => (
              <div key={group.name} className="space-y-1">
                <div className="sticky top-0 z-10 bg-white/95 px-2 py-1 text-[11px] font-medium uppercase tracking-wider text-zinc-400 dark:bg-[#111113]/95 dark:text-zinc-500">
                  {group.name}
                </div>
                {group.items.map((tool) => {
                  const active = activeTool?.name === tool.name;
                  const ann = tool.annotations || {};
                  const isDanger = Boolean(ann.destructiveHint && tool.name !== "tower_raw_request");
                  const isRead = Boolean(ann.readOnlyHint);
                  const unavail = unavailableTools[`${keyTag}::${tool.name}`];
                  const badgeCls = isDanger
                    ? "border-red-500/25 bg-red-500/10 text-red-700 dark:text-red-300"
                    : isRead
                      ? "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300"
                      : "border-sky-500/25 bg-sky-500/10 text-sky-700 dark:text-sky-300";
                  const badgeText = isDanger ? "删除" : isRead ? "只读" : "写入";
                  return (
                    <button
                      key={tool.name}
                      type="button"
                      onClick={() => selectTool(tool)}
                      className={`w-full text-left rounded-md border px-2.5 py-2 transition-colors cursor-pointer ${
                        active
                          ? "border-zinc-900 bg-zinc-100/80 dark:border-zinc-200 dark:bg-zinc-800/70"
                          : "border-transparent hover:bg-zinc-50 dark:hover:bg-zinc-900/60"
                      } ${unavail ? "opacity-65" : ""}`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-1.5 min-w-0">
                          <span className={`rounded border px-1.5 py-0.5 text-[10px] font-semibold shrink-0 ${badgeCls}`}>
                            {badgeText}
                          </span>
                          <span className="truncate text-xs font-medium text-zinc-900 dark:text-zinc-100">
                            {ann.title || tool.name}
                          </span>
                        </div>
                        {unavail && <span className={UI.badgeDanger}>不可用</span>}
                      </div>
                      <div className="mt-1 truncate font-mono text-[11px] text-zinc-400 dark:text-zinc-500">
                        {tool.name}
                      </div>
                    </button>
                  );
                })}
              </div>
            ))
          )}
          {effectiveKey && !connecting && groupedTools.length === 0 && !connectError && (
            <p className="py-6 text-center text-xs text-zinc-400">没有匹配的 MCP 工具</p>
          )}
        </div>
      </aside>

      <section className="lg:col-span-8 xl:col-span-9 min-w-0 min-h-0 overflow-y-auto bg-[#FAFAFB] p-4 sm:p-6 space-y-4 dark:bg-[#09090B]">
        <div className={`${UI.panel} p-4 space-y-3`}>
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-wrap items-center gap-2 text-xs">
              <span className={serverMeta ? UI.badgeSuccess : UI.badgeWarning}>
                <span className={`h-1.5 w-1.5 rounded-full ${serverMeta ? "bg-emerald-500" : "bg-amber-500"}`} />
                {serverMeta
                  ? `${serverMeta.name} v${serverMeta.version} · MCP ${serverMeta.protocolVersion}`
                  : "MCP 未连接"}
              </span>
              {mcpKey && (
                <span className={UI.badgeNeutral}>
                  绑定账号：{accountLabel}
                </span>
              )}
              {pingLatency !== null && (
                <span className="font-mono text-[11px] tabular-nums text-zinc-500 dark:text-zinc-400">
                  ping: {pingLatency} ms
                </span>
              )}
            </div>

            <div className="flex flex-wrap items-center gap-2">
              <button
                type="button"
                disabled={!effectiveKey || connecting}
                onClick={handlePing}
                className={UI.btnSecondary}
              >
                Ping 检测
              </button>
              <button
                type="button"
                disabled={!effectiveKey || connecting}
                onClick={connectAndLoadTools}
                className={UI.btnSecondary}
              >
                {connecting ? "握手中..." : "重新握手"}
              </button>
            </div>
          </div>

          {!mcpKey && (
            <div className="space-y-1.5 pt-1">
              <label htmlFor="mcp-manual-key" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                临时 AGENTS_TOWER_API_KEY（测试未绑定密钥时使用）
              </label>
              <input
                id="mcp-manual-key"
                type="password"
                value={manualKey}
                onChange={(e) => setManualKey(e.target.value)}
                placeholder="输入 at_sk-... 密钥自动握手加载工具"
                className={`${UI.inputSm} font-mono`}
              />
            </div>
          )}

          {connectError && <StatusBanner tone="error">MCP 握手失败：{connectError}</StatusBanner>}

          {serverMeta?.instructions && (
            <details className="group text-xs text-zinc-500 dark:text-zinc-400">
              <summary className="cursor-pointer select-none font-medium hover:text-zinc-900 dark:hover:text-zinc-200">
                查看服务端下发给 Agent 的 MCP Instructions 说明
              </summary>
              <p className={`mt-2 ${UI.subPanel} p-3 leading-relaxed text-zinc-700 dark:text-zinc-300`}>
                {serverMeta.instructions}
              </p>
            </details>
          )}
        </div>

        {activeTool && (
          <div className={`${UI.panel} p-4 sm:p-5 space-y-4`}>
            <div className="flex flex-wrap items-start justify-between gap-3 border-b border-zinc-100 pb-3.5 dark:border-zinc-800/80">
              <div className="min-w-0 space-y-1">
                <div className="flex flex-wrap items-center gap-2">
                  <h3 className="text-sm font-semibold text-zinc-950 dark:text-zinc-100">
                    {activeTool.annotations?.title || activeTool.name}
                  </h3>
                  <code className={`${UI.badgeNeutral} font-mono`}>
                    {activeTool.name}
                  </code>
                  {currentUnavail && (
                    <span className={UI.badgeDanger}>
                      当前账号不可用 · {currentUnavail}
                    </span>
                  )}
                </div>
                <p className="text-xs leading-relaxed text-zinc-500 dark:text-zinc-400">
                  {activeTool.description}
                </p>
              </div>

              <SegmentedTabs
                size="sm"
                value={inputMode}
                onChange={switchInputMode}
                options={[
                  { id: "form", label: "Schema 表单" },
                  { id: "json", label: "JSON 参数" },
                ]}
              />
            </div>

            {inputMode === "json" ? (
              <div className="space-y-1.5">
                <label htmlFor="mcp-raw-args" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  tools/call arguments (JSON 对象)
                </label>
                <textarea
                  id="mcp-raw-args"
                  rows={6}
                  value={rawArgsText}
                  onChange={(e) => setRawArgsText(e.target.value)}
                  placeholder="{}"
                  className={`${UI.inputSm} font-mono p-3`}
                />
              </div>
            ) : schemaEntries.length === 0 ? (
              <div className="rounded-md border border-dashed border-zinc-200 px-4 py-3 text-xs text-zinc-500 dark:border-zinc-800 dark:text-zinc-400">
                该工具无需输入参数，直接点击下方「调用工具」即可执行。
              </div>
            ) : (
              <div className="grid grid-cols-1 sm:grid-cols-2 gap-3.5">
                {schemaEntries.map((field) => {
                  const val = formValues[field.name] ?? "";
                  const opts = paramOptions[field.name] || [];
                  const isWide =
                    field.type === "object" ||
                    field.type === "array" ||
                    field.name === "desc" ||
                    field.name === "content";
                  const canReloadOpt =
                    field.name === "team_id"
                      ? () => fetchToolOptions("team_id", "tower_list_teams")
                      : field.name === "todo_id" && String(formValues.todolist_id || "").trim()
                        ? () =>
                            fetchToolOptions("todo_id", "tower_list_todos", {
                              todolist_id: String(formValues.todolist_id).trim(),
                            })
                        : field.name === "topic_id" && String(formValues.project_id || "").trim()
                          ? () =>
                              fetchToolOptions("topic_id", "tower_list_topics", {
                                project_id: String(formValues.project_id).trim(),
                              })
                          : field.name === "upload_id" && String(formValues.project_id || "").trim()
                            ? () =>
                                fetchToolOptions("upload_id", "tower_list_uploads", {
                                  project_id: String(formValues.project_id).trim(),
                                })
                            : null;

                  return (
                    <div key={field.name} className={`space-y-1.5 ${isWide ? "sm:col-span-2" : ""}`}>
                      <div className="flex items-center justify-between gap-2">
                        <label className="text-xs font-medium text-zinc-700 dark:text-zinc-300">
                          <code className="font-mono text-zinc-950 dark:text-zinc-100">{field.name}</code>
                          {field.required && <span className="ml-1 text-red-500">*</span>}
                          <span className="ml-1.5 font-mono text-[11px] font-normal text-zinc-400 dark:text-zinc-500">
                            ({field.type})
                          </span>
                        </label>
                        {canReloadOpt && (
                          <button
                            type="button"
                            disabled={fetchingOpt === field.name}
                            onClick={canReloadOpt}
                            className="text-[11px] font-medium text-zinc-600 underline-offset-2 hover:text-zinc-950 hover:underline disabled:opacity-50 cursor-pointer dark:text-zinc-400 dark:hover:text-zinc-100"
                          >
                            {fetchingOpt === field.name ? "加载中..." : "拉取选项"}
                          </button>
                        )}
                      </div>
                      {field.description && (
                        <div className="text-[11px] text-zinc-500 dark:text-zinc-400">{field.description}</div>
                      )}

                      {opts.length > 0 && field.type === "string" && (
                        <select
                          aria-label={`选择 ${field.name}`}
                          value={opts.some((o) => o.id === val) ? val : ""}
                          onChange={(e) =>
                            setFormValues((prev) => ({ ...prev, [field.name]: e.target.value }))
                          }
                          className={UI.inputSm}
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
                          aria-label={field.name}
                          value={String(val)}
                          onChange={(e) =>
                            setFormValues((prev) => ({ ...prev, [field.name]: e.target.value }))
                          }
                          className={`${UI.inputSm} font-mono`}
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
                          className={`${UI.inputSm} font-mono p-2.5`}
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
                          className={`${UI.inputSm} font-mono`}
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
                className={isActiveToolDangerous ? UI.btnDanger : UI.btnPrimary}
              >
                {calling ? "调用中..." : "调用工具 (tools/call)"}
              </button>
              <button
                type="button"
                onClick={() => copyText("rpc", JSON.stringify(previewPayload, null, 2))}
                className={UI.btnSecondary}
              >
                {copiedKey === "rpc" ? "已复制 JSON-RPC" : "复制 JSON-RPC"}
              </button>
              <button
                type="button"
                onClick={() => copyText("curl", mcpCurlCommand)}
                className={UI.btnSecondary}
              >
                {copiedKey === "curl" ? "已复制 curl" : "复制 /mcp curl"}
              </button>
              {isActiveToolDangerous && (
                <span className="text-xs text-red-600 dark:text-red-400">
                  危险删除工具：调用前将弹出二次确认
                </span>
              )}
            </div>

            {callError && <StatusBanner tone="error">{callError}</StatusBanner>}
          </div>
        )}

        {callResult && (
          <div className={`${UI.panel} p-4 sm:p-5 space-y-3`}>
            <div className="flex flex-wrap items-center justify-between gap-2">
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <span
                  className={`font-mono font-semibold ${
                    callResult.isError ? UI.badgeDanger : UI.badgeSuccess
                  }`}
                >
                  {callResult.isError ? "isError: true" : "isError: false"}
                </span>
                <span className="font-mono tabular-nums text-zinc-500 dark:text-zinc-400">
                  {callResult.durationMs} ms
                </span>
                <SegmentedTabs
                  size="xs"
                  value={resultView}
                  onChange={setResultView}
                  options={[
                    { id: "content", label: "工具返回内容" },
                    { id: "rpc", label: "JSON-RPC 原始报文" },
                  ]}
                />
              </div>
              <button
                type="button"
                onClick={() => copyText("out", formattedOutput.text)}
                className={UI.btnSecondary}
              >
                {copiedKey === "out" ? "已复制" : "复制结果"}
              </button>
            </div>

            <pre className="min-h-[200px] max-h-[55vh] overflow-y-auto break-all whitespace-pre-wrap rounded-md border border-zinc-200 bg-zinc-50/80 p-3.5 font-mono text-xs leading-relaxed text-zinc-800 dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-200">
              {formattedOutput.isJSON ? (
                <HighlightedJSON text={formattedOutput.text} />
              ) : (
                formattedOutput.text
              )}
            </pre>
          </div>
        )}
      </section>

      <ModalDialog
        open={Boolean(confirmDanger && activeTool)}
        onClose={() => setConfirmDanger(false)}
        title="确认执行危险 MCP 工具调用"
      >
        {confirmDanger && activeTool && (
          <>
            <div className="space-y-2.5 text-xs leading-relaxed text-zinc-600 dark:text-zinc-300">
              <p>
                即将以账号「{accountLabel}」执行{" "}
                <code className="font-mono font-semibold text-red-600 dark:text-red-400">
                  {activeTool.name}
                </code>
                （{activeTool.annotations?.title || "删除操作"}），调用参数：
              </p>
              <pre className="max-h-40 overflow-y-auto break-all whitespace-pre-wrap rounded-md border border-zinc-200 bg-zinc-50 px-3 py-2 font-mono text-xs text-zinc-800 dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-200">
                {JSON.stringify(confirmDanger, null, 2)}
              </pre>
              <p className="text-red-600 dark:text-red-400">
                该工具会直接删除或变更真实 Tower 数据，请核对参数无误后再继续。
              </p>
            </div>
            <div className="flex justify-end gap-2 pt-1">
              <button type="button" onClick={() => setConfirmDanger(false)} className={UI.btnSecondary}>
                取消
              </button>
              <button
                type="button"
                onClick={() => {
                  const args = confirmDanger;
                  setConfirmDanger(false);
                  executeToolCall(args);
                }}
                className={UI.btnDanger}
              >
                确认调用
              </button>
            </div>
          </>
        )}
      </ModalDialog>
    </div>
  );
}
