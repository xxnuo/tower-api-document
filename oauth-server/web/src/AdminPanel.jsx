import { useCallback, useEffect, useState } from "react";

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
    <div className="flex-1 min-h-0 overflow-y-auto px-4 py-5 sm:px-7 sm:py-6 space-y-5">
      <div className="flex flex-wrap items-center justify-between gap-4">
        <div>
          <h2 className="text-xl sm:text-2xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
            成员与权限管理
          </h2>
          <p className="mt-0.5 text-xs sm:text-sm text-slate-500 dark:text-slate-400">
            首个注册账号默认为管理员；也可通过命令行 <code className="rounded bg-slate-200/70 px-1.5 py-0.5 font-mono text-xs dark:bg-slate-800">agents-tower admin grant &lt;username&gt;</code> 手动授权。
          </p>
        </div>
        <button
          type="button"
          onClick={loadUsers}
          disabled={loading}
          className="rounded-full border border-slate-200 bg-white px-4 py-2 text-xs font-medium text-slate-700 hover:border-violet-300 hover:text-violet-600 cursor-pointer dark:border-slate-700 dark:bg-[#171522] dark:text-slate-200"
        >
          {loading ? "加载中..." : "刷新成员列表"}
        </button>
      </div>

      {error && (
        <div className="rounded-xl border border-red-200 bg-red-50 px-4 py-3 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
          {error}
        </div>
      )}
      {status && (
        <div className="rounded-xl border border-emerald-200 bg-emerald-50 px-4 py-3 text-xs text-emerald-700 dark:border-emerald-900/60 dark:bg-emerald-950/40 dark:text-emerald-300">
          {status}
        </div>
      )}

      <form
        onSubmit={handleSaveSettings}
        className="rounded-2xl border border-slate-100/90 bg-white p-4 sm:p-5 shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] space-y-4 dark:border-slate-800/80 dark:bg-[#171522]"
      >
        <div className="flex flex-wrap items-center justify-between gap-3">
          <div>
            <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">
              系统安全与准入策略
            </h3>
            <p className="text-xs text-slate-500 dark:text-slate-400">
              控制新成员注册方式与 Agent MCP 硬删除接口保护（拦截删除项目、清单、任务与文件）。
            </p>
          </div>
          <button
            type="submit"
            disabled={savingSettings}
            className="rounded-full bg-violet-600 px-4 py-1.5 text-xs font-medium text-white hover:bg-violet-700 disabled:opacity-60 cursor-pointer"
          >
            {savingSettings ? "保存中..." : "保存策略"}
          </button>
        </div>

        <div className="grid grid-cols-1 md:grid-cols-3 gap-4 items-start">
          <div className="space-y-1">
            <label className="text-xs font-medium text-slate-600 dark:text-slate-300">
              新用户注册准入
            </label>
            <select
              value={regMode}
              onChange={(e) => setRegMode(e.target.value)}
              className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3 py-2 text-xs text-slate-800 outline-none focus:border-violet-500 dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
            >
              <option value="open">开放注册（团队内网默认）</option>
              <option value="invite">仅凭邀请码注册</option>
              <option value="closed">关闭公开注册</option>
            </select>
          </div>

          <div className="space-y-1">
            <label className="text-xs font-medium text-slate-600 dark:text-slate-300">
              团队注册邀请码（仅邀请码模式生效）
            </label>
            <input
              type="text"
              value={inviteCode}
              onChange={(e) => setInviteCode(e.target.value)}
              disabled={regMode !== "invite"}
              placeholder={regMode === "invite" ? "设置必填邀请码" : "切换为邀请码模式后可填"}
              className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3 py-2 text-xs text-slate-800 outline-none focus:border-violet-500 disabled:opacity-50 dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
            />
          </div>

          <div className="space-y-1">
            <label className="text-xs font-medium text-slate-600 dark:text-slate-300">
              MCP 硬删除接口权限
            </label>
            <label className="flex items-center gap-2 rounded-xl border border-slate-200 bg-[#f8f8fc] px-3 py-2 text-xs text-slate-700 cursor-pointer dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-300">
              <input
                type="checkbox"
                checked={allowMCPDelete}
                onChange={(e) => setAllowMCPDelete(e.target.checked)}
                className="rounded border-slate-300 text-violet-600 focus:ring-violet-500"
              />
              <span>
                {allowMCPDelete
                  ? "已允许 MCP 执行硬删除（高风险）"
                  : "默认拦截硬删除（仅允许取消完成/解绑负责人）"}
              </span>
            </label>
          </div>
        </div>
      </form>

      <div className="rounded-2xl border border-slate-100/90 bg-white shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] overflow-hidden dark:border-slate-800/80 dark:bg-[#171522]">
        <div className="overflow-x-auto">
          <table className="w-full text-left text-xs sm:text-sm">
            <thead className="border-b border-slate-100 bg-slate-50/70 text-xs font-medium text-slate-500 dark:border-slate-800 dark:bg-[#12101b] dark:text-slate-400">
              <tr>
                <th className="px-4 py-3">ID</th>
                <th className="px-4 py-3">账户</th>
                <th className="px-4 py-3">角色</th>
                <th className="px-4 py-3">已绑定 Tower 账号</th>
                <th className="px-4 py-3">注册时间</th>
                <th className="px-4 py-3 text-right">操作</th>
              </tr>
            </thead>
            <tbody className="divide-y divide-slate-100 dark:divide-slate-800/70">
              {users.map((u) => {
                const isSelf = u.id === currentUser?.id;
                const isAdmin = u.role === "admin";
                return (
                  <tr key={u.id} className="hover:bg-slate-50/60 dark:hover:bg-white/[0.02]">
                    <td className="px-4 py-3.5 font-mono text-xs text-slate-400">#{u.id}</td>
                    <td className="px-4 py-3.5">
                      <div className="font-medium text-slate-900 dark:text-slate-100">
                        {u.display_name || u.username}
                        {isSelf && (
                          <span className="ml-1.5 text-[11px] font-normal text-slate-400">(当前登录)</span>
                        )}
                      </div>
                      <div className="font-mono text-xs text-slate-400">@{u.username}</div>
                    </td>
                    <td className="px-4 py-3.5">
                      <span
                        className={`inline-flex rounded-full px-2.5 py-0.5 text-xs font-medium ${
                          isAdmin
                            ? "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300"
                            : "bg-slate-100 text-slate-600 dark:bg-slate-800 dark:text-slate-300"
                        }`}
                      >
                        {isAdmin ? "管理员" : "普通用户"}
                      </span>
                    </td>
                    <td className="px-4 py-3.5 text-xs text-slate-600 dark:text-slate-300">
                      {u.tower_count > 0 ? `${u.tower_count} 个 (${u.tower_emails})` : "未绑定"}
                    </td>
                    <td className="px-4 py-3.5 text-xs text-slate-400">
                      {new Date(u.created_at * 1000).toLocaleString()}
                    </td>
                    <td className="px-4 py-3.5 text-right">
                      <div className="inline-flex flex-wrap justify-end gap-1.5">
                        {isAdmin ? (
                          <button
                            type="button"
                            disabled={isSelf}
                            onClick={() => setConfirmTarget({ type: "revoke", user: u })}
                            className="rounded-full border border-slate-200 bg-white px-2.5 py-1 text-xs text-slate-600 hover:border-amber-300 hover:text-amber-700 disabled:opacity-40 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300"
                          >
                            取消管理员
                          </button>
                        ) : (
                          <button
                            type="button"
                            onClick={() => handleRoleChange(u, "admin")}
                            className="rounded-full border border-violet-200 bg-violet-50 px-2.5 py-1 text-xs font-medium text-violet-700 hover:bg-violet-100 cursor-pointer dark:border-violet-800 dark:bg-violet-950/60 dark:text-violet-300"
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
                          className="rounded-full border border-slate-200 bg-white px-2.5 py-1 text-xs text-slate-600 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300"
                        >
                          重置密码
                        </button>
                        <button
                          type="button"
                          disabled={isSelf}
                          onClick={() => setConfirmTarget({ type: "delete", user: u })}
                          className="rounded-full border border-red-200 bg-white px-2.5 py-1 text-xs text-red-600 hover:bg-red-50 disabled:opacity-40 cursor-pointer dark:border-red-900/60 dark:bg-slate-800 dark:text-red-400"
                        >
                          删除
                        </button>
                      </div>
                    </td>
                  </tr>
                );
              })}
            </tbody>
          </table>
        </div>
      </div>

      {confirmTarget && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 px-4 dark:bg-black/60"
          onClick={() => setConfirmTarget(null)}
        >
          <div
            role="dialog"
            aria-modal="true"
            className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-6 shadow-xl space-y-4 dark:border-slate-700 dark:bg-slate-900"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-base font-semibold text-slate-900 dark:text-slate-100">
              {confirmTarget.type === "delete" ? "确认删除用户" : "确认取消管理员权限"}
            </h3>
            <p className="text-sm text-slate-600 dark:text-slate-300">
              {confirmTarget.type === "delete"
                ? `确定删除账户「${confirmTarget.user.username}」吗？该用户的服务端会话与绑定的全部 Tower 凭据/MCP 密钥将被彻底删除。`
                : `确定将「${confirmTarget.user.username}」降为普通用户吗？`}
            </p>
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={() => setConfirmTarget(null)}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm text-slate-700 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                取消
              </button>
              <button
                type="button"
                onClick={() =>
                  confirmTarget.type === "delete"
                    ? handleDeleteUser(confirmTarget.user)
                    : handleRoleChange(confirmTarget.user, "user")
                }
                className="rounded-lg bg-red-600 px-4 py-2 text-sm font-medium text-white hover:bg-red-700 cursor-pointer"
              >
                确认执行
              </button>
            </div>
          </div>
        </div>
      )}

      {resetTarget && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 px-4 dark:bg-black/60"
          onClick={() => setResetTarget(null)}
        >
          <form
            role="dialog"
            aria-modal="true"
            onSubmit={handleResetPassword}
            className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-6 shadow-xl space-y-4 dark:border-slate-700 dark:bg-slate-900"
            onClick={(e) => e.stopPropagation()}
          >
            <h3 className="text-base font-semibold text-slate-900 dark:text-slate-100">
              重置密码 · {resetTarget.username}
            </h3>
            <input
              type="password"
              required
              minLength={6}
              autoFocus
              value={newPassword}
              onChange={(e) => setNewPassword(e.target.value)}
              placeholder="输入至少 6 位新密码"
              className="w-full rounded-xl border border-slate-300 bg-white px-3.5 py-2 text-sm outline-none focus:border-violet-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
            />
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={() => setResetTarget(null)}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm text-slate-700 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                取消
              </button>
              <button
                type="submit"
                className="rounded-lg bg-violet-600 px-4 py-2 text-sm font-medium text-white hover:bg-violet-700 cursor-pointer"
              >
                保存新密码
              </button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}
