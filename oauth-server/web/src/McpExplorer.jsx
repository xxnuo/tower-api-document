import { useCallback, useEffect, useMemo, useRef, useState } from "react";
import { CheckIcon, CopyIcon } from "@phosphor-icons/react";
import { HighlightedJSON, toOptions } from "./ApiExplorer.jsx";
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
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { Textarea } from "@/components/ui/textarea";
import { ConfirmDialog, StatusBanner, TONE_BADGE } from "@/components/shared";

function resolveToolGroup(name) {
  if (name === "get_current_user") return "用户";
  if (name.includes("_team") && !name.includes("member")) return "团队";
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

function parseToolOptions(text) {
  const parsed = tryParseJSON(text);
  if (parsed.ok) return { options: toOptions(parsed.val), next: parsed.val?.next || parsed.val?.links?.next };
  const options = Array.from(text.matchAll(/^  - ID (\S+)(?: \| (.*))?$/gm), ([, id, name]) => ({
    id, label: (name || id).slice(0, 40),
  }));
  return { options, next: text.match(/^下一页：(\S+)$/m)?.[1] };
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
  const [selectedName, setSelectedName] = useState("get_current_user");

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

  const harvestFromToolOutput = useCallback((toolName, opts) => {
    if (opts.length === 0) return;
    let targetKey = "";
    if (toolName === "list_teams") targetKey = "team_id";
    else if (toolName === "list_projects") targetKey = "project_id";
    else if (toolName === "list_team_members" || toolName === "list_project_members") targetKey = "member_id";
    else if (toolName === "list_todolists") targetKey = "todolist_id";
    else if (toolName.startsWith("list_") && toolName.endsWith("_todos")) targetKey = "todo_id";
    else if (toolName === "list_topics") targetKey = "topic_id";
    else if (toolName === "list_uploads") targetKey = "upload_id";
    else if (toolName.endsWith("_time_logs")) targetKey = "time_log_id";
    if (!targetKey) return;

    setParamOptions((prev) => ({ ...prev, [targetKey]: opts }));
    if (opts.length === 1) {
      setFormValues((prev) => (prev[targetKey] ? prev : { ...prev, [targetKey]: opts[0].id }));
    }
  }, []);

  const fetchToolOptions = useCallback(
    async (targetKey, toolName, toolArgs = {}, signal) => {
      if (!effectiveKey) return;
      setFetchingOpt(targetKey);
      try {
        const options = new Map();
        const nextLinks = new Set();
        for (let page = 1; ; page++) {
          const { result } = await sendRPC("tools/call", {
            name: toolName,
            arguments: { ...toolArgs, page_size: 50, ...(page > 1 ? { page } : {}) },
          });
          const text = result?.content?.[0]?.text;
          if (signal?.aborted || result?.isError || !text) return;
          const { options: pageOptions, next } = parseToolOptions(text);
          pageOptions.forEach((option) => options.set(option.id, option));
          if (toolName !== "list_team_members" || !next || nextLinks.has(next)) break;
          nextLinks.add(next);
        }
        harvestFromToolOutput(toolName, [...options.values()]);
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
    fetchToolOptions("team_id", "list_teams");
  }, [effectiveKey, Boolean(serverMeta), fetchToolOptions]);

  useEffect(() => {
    const teamId = String(formValues.team_id || "").trim();
    if (!effectiveKey || !serverMeta || !teamId) return;
    fetchToolOptions("project_id", "list_projects", { team_id: teamId });
    const membersRequest = new AbortController();
    fetchToolOptions("member_id", "list_team_members", { team_id: teamId }, membersRequest.signal);
    return () => membersRequest.abort();
  }, [effectiveKey, Boolean(serverMeta), formValues.team_id, fetchToolOptions]);

  useEffect(() => {
    const projectId = String(formValues.project_id || "").trim();
    if (!effectiveKey || !serverMeta || !projectId) return;
    fetchToolOptions("todolist_id", "list_todolists", { project_id: projectId });
  }, [effectiveKey, Boolean(serverMeta), formValues.project_id, fetchToolOptions]);

  const activeTool = useMemo(
    () => tools.find((item) => item.name === selectedName) || tools[0] || null,
    [tools, selectedName]
  );

  const groupedTools = useMemo(() => {
    const q = search.trim().toLowerCase();
    const filtered = tools.filter((tool) => {
      const ann = tool.annotations || {};
      const isDanger = Boolean(ann.destructiveHint && tool.name !== "raw_request");
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
    if (activeTool.name === "raw_request") {
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
      if (isErr && text.includes("HTTP 401")) {
        setUnavailableTools((prev) => ({
          ...prev,
          [unavailKey]: "未授权 (401)",
        }));
      } else if (!isErr) {
        setUnavailableTools((prev) => {
          if (!prev[unavailKey]) return prev;
          const next = { ...prev };
          delete next[unavailKey];
          return next;
        });
        const currentTeamId = String(formValues.team_id || "").trim();
        if (activeTool.name !== "list_team_members" || argsObj.team_id !== currentTeamId) {
          harvestFromToolOutput(activeTool.name, parseToolOptions(text).options);
        }
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
      <aside className="lg:col-span-4 xl:col-span-3 flex flex-col min-h-0 border-b lg:border-b-0 lg:border-r bg-card">
        <div className="shrink-0 p-3.5 border-b space-y-2.5">
          <div className="flex items-center justify-between gap-2">
            <h2 className="text-[11px] font-semibold uppercase tracking-wider text-muted-foreground">
              MCP 工具目录 (tools/list)
            </h2>
            <Badge variant="secondary" className="font-mono tabular-nums">
              {tools.length}
            </Badge>
          </div>
          <Input
            type="text"
            aria-label="搜索 MCP 工具"
            value={search}
            onChange={(e) => setSearch(e.target.value)}
            placeholder="搜索工具名、标题或功能描述..."
            className="text-xs"
          />
          <Tabs value={modeFilter} onValueChange={setModeFilter}>
            <TabsList className="w-full h-7">
              <TabsTrigger value="all" className="text-[11px]">全部</TabsTrigger>
              <TabsTrigger value="read" className="text-[11px]">只读</TabsTrigger>
              <TabsTrigger value="write" className="text-[11px]">写入</TabsTrigger>
              <TabsTrigger value="danger" className="text-[11px]">删除</TabsTrigger>
            </TabsList>
          </Tabs>
        </div>

        <div className="max-h-64 lg:max-h-none lg:flex-1 min-h-0 overflow-y-auto p-2.5 space-y-3">
          {connecting ? (
            <p className="py-6 text-center text-xs text-muted-foreground">正在握手连接 /mcp 并拉取工具列表...</p>
          ) : !effectiveKey ? (
            <div className="py-6 px-3 text-center space-y-2.5">
              <p className="text-xs text-muted-foreground">尚未获取到 AGENTS_TOWER_API_KEY</p>
              {onGoCreds && (
                <Button type="button" size="sm" onClick={onGoCreds}>
                  前往凭据中心绑定账号
                </Button>
              )}
            </div>
          ) : (
            groupedTools.map((group) => (
              <div key={group.name} className="space-y-1">
                <div className="sticky top-0 z-10 bg-card/95 px-2 py-1 text-[11px] font-medium uppercase tracking-wider text-muted-foreground">
                  {group.name}
                </div>
                {group.items.map((tool) => {
                  const active = activeTool?.name === tool.name;
                  const ann = tool.annotations || {};
                  const isDanger = Boolean(ann.destructiveHint && tool.name !== "raw_request");
                  const isRead = Boolean(ann.readOnlyHint);
                  const unavail = unavailableTools[`${keyTag}::${tool.name}`];
                  const badgeCls = isDanger
                    ? TONE_BADGE.danger
                    : isRead
                      ? TONE_BADGE.success
                      : TONE_BADGE.info;
                  const badgeText = isDanger ? "删除" : isRead ? "只读" : "写入";
                  return (
                    <button
                      key={tool.name}
                      type="button"
                      onClick={() => selectTool(tool)}
                      className={`w-full text-left rounded-lg border px-2.5 py-2 transition-colors cursor-pointer ${
                        active
                          ? "border-foreground bg-muted/70"
                          : "border-transparent hover:bg-muted/40"
                      } ${unavail ? "opacity-65" : ""}`}
                    >
                      <div className="flex items-center justify-between gap-2">
                        <div className="flex items-center gap-1.5 min-w-0">
                          <Badge variant="outline" className={`rounded px-1.5 py-0 text-[10px] font-semibold shrink-0 ${badgeCls}`}>
                            {badgeText}
                          </Badge>
                          <span className="truncate text-xs font-medium text-foreground">
                            {ann.title || tool.name}
                          </span>
                        </div>
                        {unavail && (
                          <Badge variant="outline" className={`shrink-0 ${TONE_BADGE.danger}`}>
                            不可用
                          </Badge>
                        )}
                      </div>
                      <div className="mt-1 truncate font-mono text-[11px] text-muted-foreground">
                        {tool.name}
                      </div>
                    </button>
                  );
                })}
              </div>
            ))
          )}
          {effectiveKey && !connecting && groupedTools.length === 0 && !connectError && (
            <p className="py-6 text-center text-xs text-muted-foreground">没有匹配的 MCP 工具</p>
          )}
        </div>
      </aside>

      <section className="lg:col-span-8 xl:col-span-9 min-w-0 min-h-0 overflow-y-auto bg-background p-4 sm:p-6 space-y-4">
        <Card size="sm">
          <CardContent className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-3">
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <Badge variant="outline" className={serverMeta ? TONE_BADGE.success : TONE_BADGE.warning}>
                  <span className={`size-1.5 rounded-full ${serverMeta ? "bg-emerald-500" : "bg-amber-500"}`} />
                  {serverMeta
                    ? `${serverMeta.name} v${serverMeta.version} · MCP ${serverMeta.protocolVersion}`
                    : "MCP 未连接"}
                </Badge>
                {mcpKey && <Badge variant="secondary">绑定账号：{accountLabel}</Badge>}
                {pingLatency !== null && (
                  <span className="font-mono text-[11px] tabular-nums text-muted-foreground">
                    ping: {pingLatency} ms
                  </span>
                )}
              </div>

              <div className="flex flex-wrap items-center gap-2">
                <Button type="button" variant="outline" size="sm" disabled={!effectiveKey || connecting} onClick={handlePing}>
                  Ping 检测
                </Button>
                <Button type="button" variant="outline" size="sm" disabled={!effectiveKey || connecting} onClick={connectAndLoadTools}>
                  {connecting ? "握手中..." : "重新握手"}
                </Button>
              </div>
            </div>

            {!mcpKey && (
              <div className="space-y-1.5 pt-1">
                <Label htmlFor="mcp-manual-key" className="text-xs">
                  临时 AGENTS_TOWER_API_KEY（测试未绑定密钥时使用）
                </Label>
                <Input
                  id="mcp-manual-key"
                  type="password"
                  value={manualKey}
                  onChange={(e) => setManualKey(e.target.value)}
                  placeholder="输入 at_sk-... 密钥自动握手加载工具"
                  className="font-mono text-xs"
                />
              </div>
            )}

            {connectError && <StatusBanner tone="error">MCP 握手失败：{connectError}</StatusBanner>}

            {serverMeta?.instructions && (
              <details className="group text-xs text-muted-foreground">
                <summary className="cursor-pointer select-none font-medium hover:text-foreground">
                  查看服务端下发给 Agent 的 MCP Instructions 说明
                </summary>
                <p className="mt-2 rounded-lg border bg-muted/40 p-3 leading-relaxed text-foreground">
                  {serverMeta.instructions}
                </p>
              </details>
            )}
          </CardContent>
        </Card>

        {activeTool && (
          <Card>
            <CardHeader className="border-b">
              <div className="flex flex-wrap items-center gap-2">
                <CardTitle className="text-sm">{activeTool.annotations?.title || activeTool.name}</CardTitle>
                <Badge variant="secondary" className="font-mono">
                  {activeTool.name}
                </Badge>
                {currentUnavail && (
                  <Badge variant="outline" className={TONE_BADGE.danger}>
                    当前账号不可用 · {currentUnavail}
                  </Badge>
                )}
              </div>
              <CardDescription className="text-xs leading-relaxed">
                {activeTool.description}
              </CardDescription>
              <CardAction>
                <Tabs value={inputMode} onValueChange={switchInputMode}>
                  <TabsList className="h-7">
                    <TabsTrigger value="form" className="text-xs">Schema 表单</TabsTrigger>
                    <TabsTrigger value="json" className="text-xs">JSON 参数</TabsTrigger>
                  </TabsList>
                </Tabs>
              </CardAction>
            </CardHeader>

            <CardContent className="space-y-4">
              {inputMode === "json" ? (
                <div className="space-y-1.5">
                  <Label htmlFor="mcp-raw-args" className="text-xs">
                    tools/call arguments (JSON 对象)
                  </Label>
                  <Textarea
                    id="mcp-raw-args"
                    rows={6}
                    value={rawArgsText}
                    onChange={(e) => setRawArgsText(e.target.value)}
                    placeholder="{}"
                    className="font-mono text-xs"
                  />
                </div>
              ) : schemaEntries.length === 0 ? (
                <div className="rounded-lg border border-dashed px-4 py-3 text-xs text-muted-foreground">
                  该工具无需输入参数，直接点击下方「调用工具」即可执行。
                </div>
              ) : (
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3.5">
                  {schemaEntries.map((field) => {
                    const val = formValues[field.name] ?? "";
                    const opts = paramOptions[field.name] || [];
                    const selectedOpt = opts.find((o) => o.id === val);
                    const isWide =
                      field.type === "object" ||
                      field.type === "array" ||
                      field.name === "desc" ||
                      field.name === "content";
                    const canReloadOpt =
                      field.name === "team_id"
                        ? () => fetchToolOptions("team_id", "list_teams")
                        : field.name === "todo_id" && String(formValues.todolist_id || "").trim()
                          ? () =>
                              fetchToolOptions("todo_id", "list_todos", {
                                todolist_id: String(formValues.todolist_id).trim(),
                              })
                          : field.name === "topic_id" && String(formValues.project_id || "").trim()
                            ? () =>
                                fetchToolOptions("topic_id", "list_topics", {
                                  project_id: String(formValues.project_id).trim(),
                                })
                            : field.name === "upload_id" && String(formValues.project_id || "").trim()
                              ? () =>
                                  fetchToolOptions("upload_id", "list_uploads", {
                                    project_id: String(formValues.project_id).trim(),
                                  })
                              : null;

                    return (
                      <div key={field.name} className={`space-y-1.5 ${isWide ? "sm:col-span-2" : ""}`}>
                        <div className="flex items-center justify-between gap-2">
                          <Label className="text-xs">
                            <code className="font-mono">{field.name}</code>
                            {field.required && <span className="text-destructive">*</span>}
                            <span className="font-mono text-[11px] font-normal text-muted-foreground">
                              ({field.type})
                            </span>
                          </Label>
                          {canReloadOpt && (
                            <Button
                              type="button"
                              variant="link"
                              size="xs"
                              disabled={fetchingOpt === field.name}
                              onClick={canReloadOpt}
                              className="h-auto p-0 text-[11px]"
                            >
                              {fetchingOpt === field.name ? "加载中..." : "拉取选项"}
                            </Button>
                          )}
                        </div>
                        {field.description && (
                          <div className="text-[11px] text-muted-foreground">{field.description}</div>
                        )}

                        {opts.length > 0 && field.type === "string" && (
                          <Select
                            value={selectedOpt ? val : "__empty__"}
                            onValueChange={(nextVal) =>
                              setFormValues((prev) => ({
                                ...prev,
                                [field.name]: nextVal === "__empty__" ? "" : nextVal,
                              }))
                            }
                          >
                            <SelectTrigger aria-label={`选择 ${field.name}`} className="w-full text-xs">
                              <SelectValue>
                                {selectedOpt
                                  ? `${selectedOpt.label} (${selectedOpt.id})`
                                  : `下拉选择或下方手动输入（共 ${opts.length} 项）`}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="__empty__">
                                下拉选择或下方手动输入（共 {opts.length} 项）
                              </SelectItem>
                              {opts.map((o) => (
                                <SelectItem key={o.id} value={o.id}>
                                  {o.label} ({o.id})
                                </SelectItem>
                              ))}
                            </SelectContent>
                          </Select>
                        )}

                        {field.type === "boolean" ? (
                          <Select
                            value={val === "true" || val === "false" ? val : "__empty__"}
                            onValueChange={(nextVal) =>
                              setFormValues((prev) => ({
                                ...prev,
                                [field.name]: nextVal === "__empty__" ? "" : nextVal,
                              }))
                            }
                          >
                            <SelectTrigger aria-label={field.name} className="w-full font-mono text-xs">
                              <SelectValue>
                                {val === "true" || val === "false" ? val : "默认（不传）"}
                              </SelectValue>
                            </SelectTrigger>
                            <SelectContent>
                              <SelectItem value="__empty__">默认（不传）</SelectItem>
                              <SelectItem value="true">true</SelectItem>
                              <SelectItem value="false">false</SelectItem>
                            </SelectContent>
                          </Select>
                        ) : field.type === "object" || field.type === "array" ? (
                          <Textarea
                            rows={3}
                            value={val}
                            onChange={(e) =>
                              setFormValues((prev) => ({ ...prev, [field.name]: e.target.value }))
                            }
                            placeholder={field.type === "array" ? '["id1", "id2"]' : '{"key": "value"}'}
                            className="font-mono text-xs"
                          />
                        ) : (
                          <Input
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
                            className="font-mono text-xs"
                          />
                        )}
                      </div>
                    );
                  })}
                </div>
              )}

              <div className="flex flex-wrap items-center gap-2 pt-1">
                <Button
                  type="button"
                  variant={isActiveToolDangerous ? "destructive" : "default"}
                  disabled={calling || !effectiveKey}
                  onClick={handleInvokeClick}
                >
                  {calling ? "调用中..." : "调用工具 (tools/call)"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => copyText("rpc", JSON.stringify(previewPayload, null, 2))}
                >
                  {copiedKey === "rpc" ? <CheckIcon /> : <CopyIcon />}
                  {copiedKey === "rpc" ? "已复制 JSON-RPC" : "复制 JSON-RPC"}
                </Button>
                <Button
                  type="button"
                  variant="outline"
                  onClick={() => copyText("curl", mcpCurlCommand)}
                >
                  {copiedKey === "curl" ? <CheckIcon /> : <CopyIcon />}
                  {copiedKey === "curl" ? "已复制 curl" : "复制 /mcp curl"}
                </Button>
                {isActiveToolDangerous && (
                  <span className="text-xs text-destructive">
                    危险删除工具：调用前将弹出二次确认
                  </span>
                )}
              </div>

              {callError && <StatusBanner tone="error">{callError}</StatusBanner>}
            </CardContent>
          </Card>
        )}

        {callResult && (
          <Card>
            <CardHeader className="border-b">
              <div className="flex flex-wrap items-center gap-2 text-xs">
                <Badge
                  variant="outline"
                  className={`font-mono font-semibold ${
                    callResult.isError ? TONE_BADGE.danger : TONE_BADGE.success
                  }`}
                >
                  {callResult.isError ? "isError: true" : "isError: false"}
                </Badge>
                <span className="font-mono tabular-nums text-muted-foreground">
                  {callResult.durationMs} ms
                </span>
                <Tabs value={resultView} onValueChange={setResultView}>
                  <TabsList className="h-7">
                    <TabsTrigger value="content" className="text-[11px]">工具返回内容</TabsTrigger>
                    <TabsTrigger value="rpc" className="text-[11px]">JSON-RPC 原始报文</TabsTrigger>
                  </TabsList>
                </Tabs>
              </div>
              <CardAction>
                <Button type="button" variant="outline" size="sm" onClick={() => copyText("out", formattedOutput.text)}>
                  {copiedKey === "out" ? <CheckIcon /> : <CopyIcon />}
                  {copiedKey === "out" ? "已复制" : "复制结果"}
                </Button>
              </CardAction>
            </CardHeader>

            <CardContent>
              <pre className="min-h-[200px] max-h-[55vh] overflow-y-auto break-all whitespace-pre-wrap rounded-lg border bg-muted/40 p-3.5 font-mono text-xs leading-relaxed text-foreground">
                {formattedOutput.isJSON ? (
                  <HighlightedJSON text={formattedOutput.text} />
                ) : (
                  formattedOutput.text
                )}
              </pre>
            </CardContent>
          </Card>
        )}
      </section>

      <ConfirmDialog
        open={Boolean(confirmDanger && activeTool)}
        onOpenChange={(open) => {
          if (!open) setConfirmDanger(false);
        }}
        title="确认执行危险 MCP 工具调用"
        confirmLabel="确认调用"
        confirmVariant="destructive"
        onConfirm={() => {
          const args = confirmDanger;
          setConfirmDanger(false);
          executeToolCall(args);
        }}
      >
        {confirmDanger && activeTool && (
          <div className="space-y-2.5 text-xs leading-relaxed text-muted-foreground">
            <p>
              即将以账号「{accountLabel}」执行{" "}
              <code className="font-mono font-semibold text-destructive">{activeTool.name}</code>
              （{activeTool.annotations?.title || "删除操作"}），调用参数：
            </p>
            <pre className="max-h-40 overflow-y-auto break-all whitespace-pre-wrap rounded-lg border bg-muted/40 px-3 py-2 font-mono text-xs text-foreground">
              {JSON.stringify(confirmDanger, null, 2)}
            </pre>
            <p className="text-destructive">
              该工具会直接删除或变更真实 Tower 数据，请核对参数无误后再继续。
            </p>
          </div>
        )}
      </ConfirmDialog>
    </div>
  );
}
