import { useCallback, useEffect, useState } from "react";
import { ModalDialog, StatusBanner, UI } from "./ui.jsx";

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
      setStatus(`已删除用户 ${target.username} 及其关联凭据`);
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
        <div className="flex flex-wrap items-center justify-between gap-4 border-b border-zinc-200 pb-4 dark:border-zinc-800">
          <div>
            <h1 className="text-lg font-semibold tracking-tight text-zinc-950 dark:text-zinc-100">
              成员与权限管理
            </h1>
            <p className="mt-1 text-xs text-zinc-500 dark:text-zinc-400">
              首个注册账号默认为管理员；也可通过命令行{" "}
              <code className="rounded border border-zinc-200 bg-zinc-100 px-1.5 py-0.5 font-mono text-[11px] text-zinc-800 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-200">
                agents-tower admin grant &lt;username&gt;
              </code>{" "}
              手动授权。
            </p>
          </div>
          <button type="button" onClick={loadUsers} disabled={loading} className={UI.btnSecondary}>
            {loading ? "加载中..." : "刷新成员列表"}
          </button>
        </div>

        {error && <StatusBanner tone="error">{error}</StatusBanner>}
        {status && <StatusBanner tone="success">{status}</StatusBanner>}

        <form onSubmit={handleSaveSettings} className={`${UI.panel} p-4 sm:p-5 space-y-4`}>
          <div className="flex flex-wrap items-center justify-between gap-3 border-b border-zinc-100 pb-3.5 dark:border-zinc-800/80">
            <div>
              <h2 className="text-sm font-semibold text-zinc-950 dark:text-zinc-100">
                系统安全与准入策略
              </h2>
              <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
                控制新成员注册方式与 Agent MCP 硬删除接口保护（拦截删除项目、清单、任务与文件）。
              </p>
            </div>
            <button type="submit" disabled={savingSettings} className={UI.btnPrimary}>
              {savingSettings ? "保存中..." : "保存策略"}
            </button>
          </div>

          <div className="grid grid-cols-1 md:grid-cols-3 gap-4 items-start">
            <div className="space-y-1.5">
              <label htmlFor="admin-reg-mode" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                新用户注册准入
              </label>
              <select
                id="admin-reg-mode"
                value={regMode}
                onChange={(e) => setRegMode(e.target.value)}
                className={`${UI.inputSm} h-[34px]`}
              >
                <option value="open">开放注册（团队内网默认）</option>
                <option value="invite">仅凭邀请码注册</option>
                <option value="closed">关闭公开注册</option>
              </select>
            </div>

            <div className="space-y-1.5">
              <label htmlFor="admin-invite-code" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                团队注册邀请码（仅邀请码模式生效）
              </label>
              <input
                id="admin-invite-code"
                type="text"
                value={inviteCode}
                onChange={(e) => setInviteCode(e.target.value)}
                disabled={regMode !== "invite"}
                placeholder={regMode === "invite" ? "设置必填邀请码" : "切换为邀请码模式后可填"}
                className={`${UI.inputSm} h-[34px] font-mono disabled:opacity-50`}
              />
            </div>

            <div className="space-y-1.5">
              <span className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                MCP 硬删除接口权限
              </span>
              <label className="flex min-h-[34px] items-center gap-2 rounded-md border border-zinc-200 bg-zinc-50/70 px-3 py-1.5 text-xs text-zinc-700 cursor-pointer dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-300">
                <input
                  type="checkbox"
                  checked={allowMCPDelete}
                  onChange={(e) => setAllowMCPDelete(e.target.checked)}
                  className="h-3.5 w-3.5 rounded border-zinc-300 accent-zinc-950 dark:border-zinc-700 dark:accent-zinc-100"
                />
                <span>
                  {allowMCPDelete
                    ? "已允许 MCP 执行硬删除（高风险）"
                    : "默认拦截硬删除（仅允许取消完成/解绑）"}
                </span>
              </label>
            </div>
          </div>
        </form>

        <div className={`${UI.panel} overflow-hidden`}>
          <div className="flex items-center justify-between border-b border-zinc-200 px-4 py-3 dark:border-zinc-800">
            <h2 className="text-xs font-semibold uppercase tracking-wider text-zinc-500 dark:text-zinc-400">
              成员名册
            </h2>
            <span className="font-mono text-xs tabular-nums text-zinc-500 dark:text-zinc-400">
              共 {users.length} 人
            </span>
          </div>
          <div className="overflow-x-auto">
            <table className="w-full text-left text-xs">
              <thead className="border-b border-zinc-200 bg-zinc-50/80 text-[11px] font-medium uppercase tracking-wider text-zinc-500 dark:border-zinc-800 dark:bg-zinc-900/50 dark:text-zinc-400">
                <tr>
                  <th className="px-4 py-2.5">ID</th>
                  <th className="px-4 py-2.5">账户</th>
                  <th className="px-4 py-2.5">角色</th>
                  <th className="px-4 py-2.5">已绑定 Tower 账号</th>
                  <th className="px-4 py-2.5">注册时间</th>
                  <th className="px-4 py-2.5 text-right">操作</th>
                </tr>
              </thead>
              <tbody className="divide-y divide-zinc-100 dark:divide-zinc-800/80">
                {users.map((u) => {
                  const isSelf = u.id === currentUser?.id;
                  const isAdmin = u.role === "admin";
                  return (
                    <tr key={u.id} className="transition-colors hover:bg-zinc-50/80 dark:hover:bg-zinc-900/40">
                      <td className="px-4 py-3 font-mono tabular-nums text-zinc-400 dark:text-zinc-500">
                        #{u.id}
                      </td>
                      <td className="px-4 py-3">
                        <div className="font-medium text-zinc-950 dark:text-zinc-100">
                          {u.display_name || u.username}
                          {isSelf && (
                            <span className="ml-1.5 text-[11px] font-normal text-zinc-400">(当前登录)</span>
                          )}
                        </div>
                        <div className="font-mono text-[11px] text-zinc-400 dark:text-zinc-500">@{u.username}</div>
                      </td>
                      <td className="px-4 py-3">
                        <span className={isAdmin ? UI.badgeAdmin : UI.badgeNeutral}>
                          {isAdmin ? "管理员" : "普通用户"}
                        </span>
                      </td>
                      <td className="px-4 py-3 text-zinc-600 dark:text-zinc-300">
                        {u.tower_count > 0 ? (
                          <span>
                            <span className="font-mono tabular-nums">{u.tower_count}</span> 个 ({u.tower_emails})
                          </span>
                        ) : (
                          <span className="text-zinc-400 dark:text-zinc-500">未绑定</span>
                        )}
                      </td>
                      <td className="px-4 py-3 font-mono text-[11px] tabular-nums text-zinc-400 dark:text-zinc-500">
                        {new Date(u.created_at * 1000).toLocaleString()}
                      </td>
                      <td className="px-4 py-3 text-right">
                        <div className="inline-flex flex-wrap justify-end gap-1.5">
                          {isAdmin ? (
                            <button
                              type="button"
                              disabled={isSelf}
                              onClick={() => setConfirmTarget({ type: "revoke", user: u })}
                              className={`${UI.btnSecondary} px-2.5 py-1 text-[11px]`}
                            >
                              取消管理员
                            </button>
                          ) : (
                            <button
                              type="button"
                              onClick={() => handleRoleChange(u, "admin")}
                              className={`${UI.btnSecondary} px-2.5 py-1 text-[11px]`}
                            >
                              设为管理员
                            </button>
                          )}
                          <button
                            type="button"
                            onClick={() => {
                              setResetTarget(u);
                              setNewPassword("");
                            }}
                            className={`${UI.btnSecondary} px-2.5 py-1 text-[11px]`}
                          >
                            重置密码
                          </button>
                          <button
                            type="button"
                            disabled={isSelf}
                            onClick={() => setConfirmTarget({ type: "delete", user: u })}
                            className={`${UI.btnDangerOutline} px-2.5 py-1 text-[11px]`}
                          >
                            删除
                          </button>
                        </div>
                      </td>
                    </tr>
                  );
                })}
                {!loading && users.length === 0 && (
                  <tr>
                    <td colSpan={6} className="px-4 py-8 text-center text-xs text-zinc-400">
                      暂无成员记录
                    </td>
                  </tr>
                )}
              </tbody>
            </table>
          </div>
        </div>

        <ModalDialog
          open={Boolean(confirmTarget)}
          onClose={() => setConfirmTarget(null)}
          title={confirmTarget?.type === "delete" ? "确认删除用户" : "确认取消管理员权限"}
        >
          {confirmTarget && (
            <>
              <p className="text-xs leading-relaxed text-zinc-600 dark:text-zinc-300">
                {confirmTarget.type === "delete"
                  ? `确定删除账户「${confirmTarget.user.username}」吗？该用户的服务端会话与绑定的全部 Tower 凭据/MCP 密钥将被彻底删除。`
                  : `确定将「${confirmTarget.user.username}」降为普通用户吗？`}
              </p>
              <div className="flex justify-end gap-2 pt-1">
                <button type="button" onClick={() => setConfirmTarget(null)} className={UI.btnSecondary}>
                  取消
                </button>
                <button
                  type="button"
                  onClick={() =>
                    confirmTarget.type === "delete"
                      ? handleDeleteUser(confirmTarget.user)
                      : handleRoleChange(confirmTarget.user, "user")
                  }
                  className={UI.btnDanger}
                >
                  确认执行
                </button>
              </div>
            </>
          )}
        </ModalDialog>

        <ModalDialog
          open={Boolean(resetTarget)}
          onClose={() => setResetTarget(null)}
          title={resetTarget ? `重置密码 · ${resetTarget.username}` : ""}
        >
          {resetTarget && (
            <form onSubmit={handleResetPassword} className="space-y-4">
              <div className="space-y-1.5">
                <label htmlFor="admin-reset-pw" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                  新密码（至少 6 位）
                </label>
                <input
                  id="admin-reset-pw"
                  type="password"
                  required
                  minLength={6}
                  autoFocus
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  placeholder="输入至少 6 位新密码"
                  className={UI.input}
                />
              </div>
              <div className="flex justify-end gap-2 pt-1">
                <button type="button" onClick={() => setResetTarget(null)} className={UI.btnSecondary}>
                  取消
                </button>
                <button type="submit" className={UI.btnPrimary}>
                  保存新密码
                </button>
              </div>
            </form>
          )}
        </ModalDialog>
      </div>
    </div>
  );
}
