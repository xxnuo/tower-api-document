import { useCallback, useEffect, useState } from "react";
import { ArrowsClockwiseIcon } from "@phosphor-icons/react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import {
  Table,
  TableBody,
  TableCell,
  TableHead,
  TableHeader,
  TableRow,
} from "@/components/ui/table";
import { ConfirmDialog, StatusBanner } from "@/components/shared";

const REG_MODE_LABELS = {
  open: "开放注册（团队内网默认）",
  invite: "仅凭邀请码注册",
  closed: "关闭公开注册",
};

export default function AdminPanel({ currentUser }) {
  const [users, setUsers] = useState([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [confirmTarget, setConfirmTarget] = useState(null);
  const [resetTarget, setResetTarget] = useState(null);
  const [newPassword, setNewPassword] = useState("");
  const [regMode, setRegMode] = useState("open");
  const [inviteCode, setInviteCode] = useState("");
  const [allowMCPDelete, setAllowMCPDelete] = useState(false);
  const [savingSettings, setSavingSettings] = useState(false);

  const loadUsers = useCallback(async () => {
    setLoading(true);
    setError("");
    try {
      const [res, settingsRes] = await Promise.all([
        fetch("/api/admin/users"),
        fetch("/api/admin/settings"),
      ]);
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      setUsers(data.users || []);
      if (settingsRes.ok) {
        const sData = await settingsRes.json();
        if (sData.settings) {
          setRegMode(sData.settings.registration_mode || "open");
          setInviteCode(sData.settings.invite_code || "");
          setAllowMCPDelete(Boolean(sData.settings.allow_mcp_delete));
        }
      }
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }, []);

  async function handleSaveSettings(event) {
    event.preventDefault();
    setSavingSettings(true);
    setError("");
    setStatus("");
    try {
      const res = await fetch("/api/admin/settings", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          registration_mode: regMode,
          invite_code: inviteCode.trim(),
          allow_mcp_delete: allowMCPDelete,
        }),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      if (data.settings) {
        setRegMode(data.settings.registration_mode || "open");
        setInviteCode(data.settings.invite_code || "");
        setAllowMCPDelete(Boolean(data.settings.allow_mcp_delete));
      }
      setStatus("已保存全局安全与准入策略");
    } catch (err) {
      setError(err.message);
    } finally {
      setSavingSettings(false);
    }
  }

  useEffect(() => {
    loadUsers();
  }, [loadUsers]);

  async function handleRoleChange(target, nextRole) {
    setError("");
    setStatus("");
    setConfirmTarget(null);
    try {
      const res = await fetch(`/api/admin/users/${target.id}/role`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ role: nextRole }),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      setStatus(`已将 ${target.username} 角色更新为 ${nextRole === "admin" ? "管理员" : "普通用户"}`);
      loadUsers();
    } catch (err) {
      setError(err.message);
    }
  }

  async function handleDeleteUser(target) {
    setError("");
    setStatus("");
    setConfirmTarget(null);
    try {
      const res = await fetch(`/api/admin/users/${target.id}`, { method: "DELETE" });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      setStatus(`已删除用户 ${target.username} 及其绑定的 Tower 账号`);
      loadUsers();
    } catch (err) {
      setError(err.message);
    }
  }

  async function handleResetPassword(event) {
    event.preventDefault();
    if (!resetTarget) return;
    setError("");
    setStatus("");
    try {
      const res = await fetch(`/api/admin/users/${resetTarget.id}/password`, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ password: newPassword }),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      setStatus(`已重置 ${resetTarget.username} 的登录密码`);
      setResetTarget(null);
      setNewPassword("");
    } catch (err) {
      setError(err.message);
    }
  }

  return (
    <div className="flex-1 min-h-0 overflow-y-auto">
      <div className="mx-auto w-full max-w-[1360px] px-4 py-5 sm:px-6 sm:py-6 space-y-5">
        <div className="flex flex-wrap items-center justify-between gap-4 border-b pb-4">
          <div>
            <h1 className="font-heading text-lg font-semibold tracking-tight">成员与权限管理</h1>
            <p className="mt-1 text-sm leading-relaxed text-muted-foreground">
              设置同事如何注册及是否允许 MCP 硬删除资源。修改后保存策略，或在成员列表中调整角色、重置密码。
            </p>
            <p className="mt-1 text-xs text-muted-foreground">
              首个注册账号默认为管理员；也可通过命令行{" "}
              <code className="rounded border bg-muted px-1.5 py-0.5 font-mono text-[11px] text-foreground">
                agentstower admin grant &lt;username&gt;
              </code>{" "}
              手动授权。
            </p>
          </div>
          <Button type="button" variant="outline" size="sm" onClick={loadUsers} disabled={loading}>
            <ArrowsClockwiseIcon />
            {loading ? "加载中..." : "刷新成员列表"}
          </Button>
        </div>

        {error && <StatusBanner tone="error">{error}</StatusBanner>}
        {status && <StatusBanner tone="success">{status}</StatusBanner>}

        <form onSubmit={handleSaveSettings}>
          <Card>
            <CardHeader className="border-b">
              <CardTitle>系统安全与准入策略</CardTitle>
              <CardDescription className="text-xs">
                控制新成员注册方式与 Agent MCP 硬删除接口保护（拦截删除项目、清单、任务与文件）。
              </CardDescription>
              <CardAction>
                <Button type="submit" size="sm" disabled={savingSettings}>
                  {savingSettings ? "保存中..." : "保存策略"}
                </Button>
              </CardAction>
            </CardHeader>

            <CardContent className="grid grid-cols-1 md:grid-cols-3 gap-4 items-start">
              <div className="space-y-1.5">
                <Label htmlFor="admin-reg-mode" className="text-xs">新用户注册准入</Label>
                <Select value={regMode} onValueChange={setRegMode}>
                  <SelectTrigger id="admin-reg-mode" className="w-full text-xs">
                    <SelectValue>{REG_MODE_LABELS[regMode] || regMode}</SelectValue>
                  </SelectTrigger>
                  <SelectContent>
                    <SelectItem value="open">{REG_MODE_LABELS.open}</SelectItem>
                    <SelectItem value="invite">{REG_MODE_LABELS.invite}</SelectItem>
                    <SelectItem value="closed">{REG_MODE_LABELS.closed}</SelectItem>
                  </SelectContent>
                </Select>
              </div>

              <div className="space-y-1.5">
                <Label htmlFor="admin-invite-code" className="text-xs">
                  团队注册邀请码（仅邀请码模式生效）
                </Label>
                <Input
                  id="admin-invite-code"
                  type="text"
                  value={inviteCode}
                  onChange={(e) => setInviteCode(e.target.value)}
                  disabled={regMode !== "invite"}
                  placeholder={regMode === "invite" ? "设置必填邀请码" : "切换为邀请码模式后可填"}
                  className="font-mono text-xs"
                />
              </div>

              <div className="space-y-1.5">
                <Label className="text-xs">MCP 硬删除接口权限</Label>
                <label className="flex h-8 items-center gap-2 rounded-lg border border-input bg-transparent px-2.5 text-xs cursor-pointer dark:bg-input/30">
                  <Checkbox
                    checked={allowMCPDelete}
                    onCheckedChange={(checked) => setAllowMCPDelete(Boolean(checked))}
                  />
                  <span className="truncate">
                    {allowMCPDelete
                      ? "已允许 MCP 执行硬删除（高风险）"
                      : "默认拦截硬删除（仅允许取消完成/解绑）"}
                  </span>
                </label>
              </div>
            </CardContent>
          </Card>
        </form>

        <Card className="gap-0 py-0">
          <CardHeader className="border-b py-3">
            <CardTitle className="text-xs font-semibold uppercase tracking-wider text-muted-foreground">
              成员名册
            </CardTitle>
            <CardAction>
              <span className="font-mono text-xs tabular-nums text-muted-foreground">
                共 {users.length} 人
              </span>
            </CardAction>
          </CardHeader>
          <Table className="text-xs">
            <TableHeader className="bg-muted/40">
              <TableRow>
                <TableHead className="px-4 h-9 text-[11px] text-muted-foreground">ID</TableHead>
                <TableHead className="px-4 h-9 text-[11px] text-muted-foreground">账户</TableHead>
                <TableHead className="px-4 h-9 text-[11px] text-muted-foreground">角色</TableHead>
                <TableHead className="px-4 h-9 text-[11px] text-muted-foreground">已绑定 Tower 账号</TableHead>
                <TableHead className="px-4 h-9 text-[11px] text-muted-foreground">注册时间</TableHead>
                <TableHead className="px-4 h-9 text-right text-[11px] text-muted-foreground">操作</TableHead>
              </TableRow>
            </TableHeader>
            <TableBody>
              {users.map((u) => {
                const isSelf = u.id === currentUser?.id;
                const isAdmin = u.role === "admin";
                return (
                  <TableRow key={u.id}>
                    <TableCell className="px-4 py-3 font-mono tabular-nums text-muted-foreground">
                      #{u.id}
                    </TableCell>
                    <TableCell className="px-4 py-3">
                      <div className="font-medium">
                        {u.display_name || u.username}
                        {isSelf && (
                          <span className="ml-1.5 text-[11px] font-normal text-muted-foreground">
                            (当前登录)
                          </span>
                        )}
                      </div>
                      <div className="font-mono text-[11px] text-muted-foreground">@{u.username}</div>
                    </TableCell>
                    <TableCell className="px-4 py-3">
                      <Badge variant={isAdmin ? "default" : "secondary"}>
                        {isAdmin ? "管理员" : "普通用户"}
                      </Badge>
                    </TableCell>
                    <TableCell className="px-4 py-3">
                      {u.tower_count > 0 ? (
                        <span>
                          <span className="font-mono tabular-nums">{u.tower_count}</span> 个 ({u.tower_emails})
                        </span>
                      ) : (
                        <span className="text-muted-foreground">未绑定</span>
                      )}
                    </TableCell>
                    <TableCell className="px-4 py-3 font-mono text-[11px] tabular-nums text-muted-foreground">
                      {new Date(u.created_at * 1000).toLocaleString()}
                    </TableCell>
                    <TableCell className="px-4 py-3 text-right">
                      <div className="inline-flex flex-wrap justify-end gap-1.5">
                        {isAdmin ? (
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            disabled={isSelf}
                            onClick={() => setConfirmTarget({ type: "revoke", user: u })}
                          >
                            取消管理员
                          </Button>
                        ) : (
                          <Button
                            type="button"
                            variant="outline"
                            size="xs"
                            onClick={() => handleRoleChange(u, "admin")}
                          >
                            设为管理员
                          </Button>
                        )}
                        <Button
                          type="button"
                          variant="outline"
                          size="xs"
                          onClick={() => {
                            setResetTarget(u);
                            setNewPassword("");
                          }}
                        >
                          重置密码
                        </Button>
                        <Button
                          type="button"
                          variant="destructive"
                          size="xs"
                          disabled={isSelf}
                          onClick={() => setConfirmTarget({ type: "delete", user: u })}
                        >
                          删除
                        </Button>
                      </div>
                    </TableCell>
                  </TableRow>
                );
              })}
              {!loading && users.length === 0 && (
                <TableRow>
                  <TableCell colSpan={6} className="px-4 py-8 text-center text-xs text-muted-foreground">
                    暂无成员记录
                  </TableCell>
                </TableRow>
              )}
            </TableBody>
          </Table>
        </Card>

        <ConfirmDialog
          open={Boolean(confirmTarget)}
          onOpenChange={(open) => {
            if (!open) setConfirmTarget(null);
          }}
          title={confirmTarget?.type === "delete" ? "确认删除用户" : "确认取消管理员权限"}
          description={
            confirmTarget?.type === "delete"
              ? `确定删除账户「${confirmTarget.user.username}」吗？该用户的服务端会话与绑定的全部 Tower 账号绑定及 MCP 密钥将被彻底删除。`
              : confirmTarget
                ? `确定将「${confirmTarget.user.username}」降为普通用户吗？`
                : ""
          }
          confirmLabel="确认执行"
          confirmVariant="destructive"
          onConfirm={() =>
            confirmTarget.type === "delete"
              ? handleDeleteUser(confirmTarget.user)
              : handleRoleChange(confirmTarget.user, "user")
          }
        />

        <Dialog
          open={Boolean(resetTarget)}
          onOpenChange={(open) => {
            if (!open) setResetTarget(null);
          }}
        >
          <DialogContent className="sm:max-w-md">
            <DialogHeader>
              <DialogTitle>{resetTarget ? `重置密码 · ${resetTarget.username}` : "重置密码"}</DialogTitle>
            </DialogHeader>
            {resetTarget && (
              <form onSubmit={handleResetPassword} className="space-y-4">
                <div className="space-y-1.5">
                  <Label htmlFor="admin-reset-pw" className="text-xs">
                    新密码（至少 6 位）
                  </Label>
                  <Input
                    id="admin-reset-pw"
                    type="password"
                    required
                    minLength={6}
                    autoFocus
                    value={newPassword}
                    onChange={(e) => setNewPassword(e.target.value)}
                    placeholder="输入至少 6 位新密码"
                  />
                </div>
                <DialogFooter>
                  <Button type="button" variant="outline" onClick={() => setResetTarget(null)}>
                    取消
                  </Button>
                  <Button type="submit">保存新密码</Button>
                </DialogFooter>
              </form>
            )}
          </DialogContent>
        </Dialog>
      </div>
    </div>
  );
}
