import { useEffect, useState } from "react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Label } from "@/components/ui/label";
import { Switch } from "@/components/ui/switch";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "@/components/ui/table";
import { StatusBanner } from "@/components/shared";

export default function McpLogs() {
  const [page, setPage] = useState(null);
  const [cursors, setCursors] = useState([]);
  const [loading, setLoading] = useState(true);
  const [saving, setSaving] = useState(false);
  const [error, setError] = useState("");

  useEffect(() => {
    const controller = new AbortController();
    async function load() {
      setLoading(true);
      setError("");
      setPage(null);
      try {
        const before = cursors.at(-1);
        const res = await fetch("/api/mcp-logs" + (before ? `?before=${before}` : ""), { signal: controller.signal });
        const data = await res.json();
        if (!res.ok) throw new Error(data.error || "读取操作日志失败");
        setPage(data);
      } catch (err) {
        if (!controller.signal.aborted) setError(err.message);
      } finally {
        if (!controller.signal.aborted) setLoading(false);
      }
    }
    load();
    return () => controller.abort();
  }, [cursors]);

  async function setRecording(enabled) {
    setSaving(true);
    setError("");
    try {
      const res = await fetch("/api/mcp-logs/settings", {
        method: "PUT",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ enabled }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error || "保存日志设置失败");
      setPage(previous => ({ ...previous, enabled: data.enabled }));
    } catch (err) {
      setError(err.message);
    } finally {
      setSaving(false);
    }
  }

  return (
    <div className="flex-1 min-h-0 overflow-y-auto">
      <div className="mx-auto w-full max-w-[1360px] space-y-5 px-4 py-5 sm:px-6 sm:py-6">
        <header className="flex items-center justify-between gap-4 border-b pb-4">
          <div className="space-y-1">
            <h1 className="font-heading text-lg font-semibold tracking-tight">MCP 操作日志</h1>
            <p className="text-sm leading-relaxed text-muted-foreground">
              回查 AI 助手何时调用了哪个 Tower 工具，以及调用结果和耗时。要记录后续调用，请开启下方开关。
            </p>
          </div>
          <Button size="sm" variant="outline" disabled={loading || saving} onClick={() => setCursors([])}>刷新</Button>
        </header>
        <div className="flex items-center justify-between gap-4 rounded-lg border bg-card p-4">
          <div className="space-y-1">
            <Label id="mcp-log-label" htmlFor="mcp-log-recording">记录我的 MCP 操作</Label>
            <p id="mcp-log-description" className="text-xs text-muted-foreground">
              开启后记录此账户绑定的所有 Tower 账号的工具调用。每条记录保留三个月，关闭后已有记录仍按期清理。
            </p>
          </div>
          <Switch id="mcp-log-recording" aria-labelledby="mcp-log-label" aria-describedby="mcp-log-description" checked={page?.enabled ?? false}
            disabled={!page || loading || saving} onCheckedChange={setRecording} />
        </div>
        {error && <StatusBanner tone="error">{error}</StatusBanner>}
        <section className="space-y-3">
          <h2 className="text-sm font-semibold">我的操作记录</h2>
          {loading ? <p className="text-sm text-muted-foreground">加载中…</p> : page && (page.logs.length ? (
            <Table>
              <TableHeader><TableRow>
                <TableHead>调用时间</TableHead>
                <TableHead>Tower 账号</TableHead><TableHead>工具</TableHead><TableHead>结果</TableHead><TableHead>耗时</TableHead>
              </TableRow></TableHeader>
              <TableBody>{page.logs.map(entry => (
                <TableRow key={entry.id}>
                  <TableCell>{new Date(entry.started_at).toLocaleString()}</TableCell>
                  <TableCell>{entry.tower_account}</TableCell>
                  <TableCell className="font-mono text-xs">{entry.tool}</TableCell>
                  <TableCell><Badge variant={entry.success ? "secondary" : "destructive"}>{entry.success ? "成功" : "失败"}</Badge></TableCell>
                  <TableCell>{entry.duration_ms} ms</TableCell>
                </TableRow>
              ))}</TableBody>
            </Table>
          ) : <p className="text-sm text-muted-foreground">暂无操作记录</p>)}
          {(cursors.length > 0 || page?.has_more) && (
            <div className="flex gap-2">
              <Button size="sm" variant="outline" disabled={loading || saving || !cursors.length} onClick={() => setCursors(cursors.slice(0, -1))}>上一页</Button>
              <Button size="sm" variant="outline" disabled={loading || saving || !page?.has_more} onClick={() => setCursors([...cursors, page.logs.at(-1).id])}>下一页</Button>
            </div>
          )}
        </section>
      </div>
    </div>
  );
}
