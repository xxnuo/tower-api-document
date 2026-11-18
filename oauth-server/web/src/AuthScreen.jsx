import { useEffect, useState } from "react";

export const QUICK_LOGIN_STORAGE_KEY = "agents_tower_saved_logins_v1";

export function loadSavedLogins() {
  try {
    const raw = localStorage.getItem(QUICK_LOGIN_STORAGE_KEY);
    const parsed = raw ? JSON.parse(raw) : [];
    return Array.isArray(parsed) ? parsed.filter((item) => item?.username && item?.session_token) : [];
  } catch {
    return [];
  }
}

export function saveQuickLoginEntry(user, sessionToken) {
  if (!user?.username || !sessionToken) return;
  const current = loadSavedLogins().filter((item) => item.username.toLowerCase() !== user.username.toLowerCase());
  const entry = {
    id: user.id,
    username: user.username,
    display_name: user.display_name || user.username,
    role: user.role,
    session_token: sessionToken,
    saved_at: Date.now(),
  };
  localStorage.setItem(QUICK_LOGIN_STORAGE_KEY, JSON.stringify([entry, ...current].slice(0, 8)));
}

export function removeQuickLoginEntry(username) {
  const next = loadSavedLogins().filter((item) => item.username.toLowerCase() !== String(username).toLowerCase());
  if (next.length === 0) localStorage.removeItem(QUICK_LOGIN_STORAGE_KEY);
  else localStorage.setItem(QUICK_LOGIN_STORAGE_KEY, JSON.stringify(next));
  return next;
}

export default function AuthScreen({
  hasUsers,
  registrationMode = "open",
  onAuthenticated,
  theme,
  onToggleTheme,
  themeLabel,
}) {
  const [mode, setMode] = useState(hasUsers ? "login" : "register");
  const [savedLogins, setSavedLogins] = useState(loadSavedLogins);
  const [username, setUsername] = useState("");
  const [displayName, setDisplayName] = useState("");
  const [password, setPassword] = useState("");
  const [inviteCode, setInviteCode] = useState("");
  const [rememberQuick, setRememberQuick] = useState(true);
  const [loading, setLoading] = useState(false);
  const [error, setError] = useState("");
  const regClosed = hasUsers && registrationMode === "closed";
  const regNeedsInvite = hasUsers && registrationMode === "invite";

  useEffect(() => {
    if (!hasUsers) setMode("register");
    else if (regClosed && mode === "register") setMode("login");
  }, [hasUsers, regClosed, mode]);

  async function handleSubmit(event) {
    event.preventDefault();
    setLoading(true);
    setError("");
    try {
      const endpoint = mode === "register" ? "/api/auth/register" : "/api/auth/login";
      const payload =
        mode === "register"
          ? {
              username: username.trim(),
              password,
              display_name: displayName.trim(),
              invite_code: inviteCode.trim(),
            }
          : { username: username.trim(), password };
      const res = await fetch(endpoint, {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify(payload),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      if (rememberQuick && data.user && data.session_token) {
        saveQuickLoginEntry(data.user, data.session_token);
      }
      onAuthenticated(data);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  async function handleQuickSelect(entry) {
    setLoading(true);
    setError("");
    try {
      const res = await fetch("/api/auth/quick-login", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ session_token: entry.session_token }),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        const next = removeQuickLoginEntry(entry.username);
        setSavedLogins(next);
        setMode("login");
        setUsername(entry.username);
        throw new Error(data.error || "快捷登录已过期，请输入密码重新登录");
      }
      saveQuickLoginEntry(data.user, data.session_token);
      onAuthenticated(data);
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  function handleForgetEntry(event, entry) {
    event.stopPropagation();
    fetch("/api/auth/logout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ session_token: entry.session_token, revoke: true }),
    }).catch(() => {});
    setSavedLogins(removeQuickLoginEntry(entry.username));
  }

  return (
    <div className="min-h-dvh flex flex-col justify-center items-center px-4 py-8 bg-[#f6f6fb] dark:bg-[#0d0c14]">
      <div className="fixed top-4 right-4">
        <button
          type="button"
          onClick={onToggleTheme}
          className="rounded-full border border-slate-200 bg-white px-3.5 py-1.5 text-xs font-medium text-slate-600 shadow-xs hover:border-violet-300 hover:text-violet-600 cursor-pointer dark:border-slate-800 dark:bg-[#171522] dark:text-slate-300"
        >
          主题：{themeLabel}
        </button>
      </div>

      <div className="w-full max-w-md rounded-3xl border border-slate-100/90 bg-white p-6 sm:p-8 shadow-[0_8px_30px_-8px_rgba(15,23,42,0.08)] space-y-6 dark:border-slate-800/80 dark:bg-[#171522]">
        <div className="flex flex-col items-center text-center space-y-2">
          <div className="flex h-12 w-12 items-center justify-center rounded-2xl bg-gradient-to-br from-violet-500 to-purple-600 text-white shadow-lg shadow-violet-600/30">
            <svg className="h-6 w-6" viewBox="0 0 24 24" fill="currentColor">
              <rect x="4" y="4" width="7" height="7" rx="2" />
              <rect x="13" y="4" width="7" height="7" rx="2" opacity="0.7" />
              <rect x="4" y="13" width="7" height="7" rx="2" opacity="0.7" />
              <rect x="13" y="13" width="7" height="7" rx="2" />
            </svg>
          </div>
          <h1 className="text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
            AgentsTower
          </h1>
          <p className="text-xs text-slate-500 dark:text-slate-400">
            团队成员 Tower OAuth 凭据托管与 AI Agent MCP 服务
          </p>
        </div>

        {savedLogins.length > 0 && (
          <div className="space-y-2">
            <div className="text-xs font-medium text-slate-500 dark:text-slate-400">
              已保存账号 · 一键快捷登录
            </div>
            <div className="space-y-1.5">
              {savedLogins.map((entry) => (
                <div
                  key={entry.username}
                  onClick={() => !loading && handleQuickSelect(entry)}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") handleQuickSelect(entry);
                  }}
                  className="group flex items-center justify-between gap-3 rounded-2xl border border-slate-200/80 bg-[#f8f8fc] px-3.5 py-2.5 transition-all hover:border-violet-400 hover:bg-violet-50/50 cursor-pointer dark:border-slate-800 dark:bg-[#0e0d16] dark:hover:border-violet-600"
                >
                  <div className="flex items-center gap-3 min-w-0">
                    <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-violet-600 text-xs font-semibold text-white">
                      {(entry.display_name || entry.username).slice(0, 1).toUpperCase()}
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate text-sm font-medium text-slate-900 dark:text-slate-100">
                          {entry.display_name || entry.username}
                        </span>
                        <span
                          className={`rounded-full px-1.5 py-0.5 text-[10px] font-medium ${
                            entry.role === "admin"
                              ? "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300"
                              : "bg-slate-200/70 text-slate-600 dark:bg-slate-800 dark:text-slate-300"
                          }`}
                        >
                          {entry.role === "admin" ? "管理员" : "成员"}
                        </span>
                      </div>
                      <div className="truncate text-[11px] text-slate-400">@{entry.username}</div>
                    </div>
                  </div>
                  <div className="flex items-center gap-2 shrink-0">
                    <span className="text-xs font-medium text-violet-600 dark:text-violet-400">
                      快捷进入
                    </span>
                    <button
                      type="button"
                      title="移除该快捷登录记录"
                      onClick={(event) => handleForgetEntry(event, entry)}
                      className="rounded-full p-1 text-slate-400 hover:bg-red-50 hover:text-red-600 cursor-pointer dark:hover:bg-red-950/50 dark:hover:text-red-400"
                    >
                      <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                        <line x1="18" y1="6" x2="6" y2="18" />
                        <line x1="6" y1="6" x2="18" y2="18" />
                      </svg>
                    </button>
                  </div>
                </div>
              ))}
            </div>
          </div>
        )}

        <div className="grid grid-cols-2 rounded-full border border-slate-200/80 bg-slate-100/70 p-1 dark:border-slate-800 dark:bg-[#0e0d16]">
          <button
            type="button"
            onClick={() => {
              setMode("login");
              setError("");
            }}
            className={`rounded-full py-1.5 text-xs font-semibold transition-all cursor-pointer ${
              mode === "login"
                ? "bg-white text-slate-900 shadow-xs dark:bg-slate-800 dark:text-slate-100"
                : "text-slate-500 hover:text-slate-900 dark:text-slate-400"
            }`}
          >
            账号登录
          </button>
          <button
            type="button"
            disabled={regClosed}
            title={regClosed ? "管理员已关闭公开注册" : undefined}
            onClick={() => {
              if (regClosed) return;
              setMode("register");
              setError("");
            }}
            className={`rounded-full py-1.5 text-xs font-semibold transition-all cursor-pointer ${
              mode === "register"
                ? "bg-white text-slate-900 shadow-xs dark:bg-slate-800 dark:text-slate-100"
                : regClosed
                  ? "text-slate-400 opacity-50 cursor-not-allowed dark:text-slate-600"
                  : "text-slate-500 hover:text-slate-900 dark:text-slate-400"
            }`}
          >
            {regClosed ? "注册已关闭" : regNeedsInvite ? "凭邀请码注册" : "注册新账号"}
          </button>
        </div>

        {!hasUsers && mode === "register" && (
          <div className="rounded-xl border border-violet-200 bg-violet-50/70 px-3.5 py-2.5 text-xs text-violet-700 dark:border-violet-900/60 dark:bg-violet-950/40 dark:text-violet-300">
            当前系统尚无注册用户，首个创建的账户将自动成为<strong>系统管理员</strong>。
          </div>
        )}

        {error && (
          <div className="rounded-xl border border-red-200 bg-red-50 px-3.5 py-2.5 text-xs text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            {error}
          </div>
        )}

        <form className="space-y-3.5" method="post" onSubmit={handleSubmit}>
          <div className="space-y-1">
            <label className="text-xs font-medium text-slate-600 dark:text-slate-300">用户名</label>
            <input
              type="text"
              name="username"
              autoComplete="username"
              required
              minLength={2}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="例如: alice"
              className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3.5 py-2.5 text-sm outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
            />
          </div>

          {mode === "register" && (
            <div className="space-y-1">
              <label className="text-xs font-medium text-slate-600 dark:text-slate-300">
                显示名称（可选）
              </label>
              <input
                type="text"
                name="display_name"
                autoComplete="nickname"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder="团队内展示的姓名或昵称"
                className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3.5 py-2.5 text-sm outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
              />
            </div>
          )}

          {mode === "register" && regNeedsInvite && (
            <div className="space-y-1">
              <label className="text-xs font-medium text-slate-600 dark:text-slate-300">
                注册邀请码
              </label>
              <input
                type="text"
                name="invite_code"
                required
                value={inviteCode}
                onChange={(e) => setInviteCode(e.target.value)}
                placeholder="输入管理员提供的团队邀请码"
                className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3.5 py-2.5 text-sm outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
              />
            </div>
          )}

          <div className="space-y-1">
            <label className="text-xs font-medium text-slate-600 dark:text-slate-300">密码</label>
            <input
              type="password"
              name="password"
              autoComplete={mode === "login" ? "current-password" : "new-password"}
              required
              minLength={6}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={mode === "register" ? "设置至少 6 位登录密码" : "输入登录密码"}
              className="w-full rounded-xl border border-slate-200 bg-[#f8f8fc] px-3.5 py-2.5 text-sm outline-none focus:border-violet-500 focus:bg-white dark:border-slate-700 dark:bg-[#0e0d16] dark:text-slate-100"
            />
          </div>

          <label className="flex items-center gap-2 text-xs text-slate-600 cursor-pointer select-none dark:text-slate-400">
            <input
              type="checkbox"
              checked={rememberQuick}
              onChange={(e) => setRememberQuick(e.target.checked)}
              className="rounded border-slate-300 text-violet-600 focus:ring-violet-500"
            />
            <span>在此浏览器保存登录信息以便下次一键快捷登录</span>
          </label>

          <button
            type="submit"
            disabled={loading}
            className="w-full rounded-full bg-violet-600 py-2.5 text-sm font-semibold text-white shadow-sm shadow-violet-600/25 hover:bg-violet-700 disabled:opacity-60 cursor-pointer"
          >
            {loading ? "处理中..." : mode === "register" ? "注册并进入 AgentsTower" : "登录 AgentsTower"}
          </button>
        </form>
      </div>
    </div>
  );
}
