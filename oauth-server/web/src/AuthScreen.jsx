import { useEffect, useState } from "react";
import { BrandMark, SegmentedTabs, StatusBanner, UI } from "./ui.jsx";

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
    <div className="min-h-dvh flex flex-col justify-center items-center px-4 py-10 bg-[#FAFAFB] bg-tech-grid dark:bg-[#09090B]">
      <div className="fixed top-4 right-4 z-10">
        <button type="button" onClick={onToggleTheme} className={UI.btnSecondary}>
          主题：{themeLabel}
        </button>
      </div>

      <div className={`w-full max-w-[400px] ${UI.panel} p-6 sm:p-7 space-y-6`}>
        <div className="flex items-start justify-between gap-3 border-b border-zinc-100 pb-5 dark:border-zinc-800/80">
          <div className="flex items-center gap-3">
            <div className="flex h-9 w-9 shrink-0 items-center justify-center rounded-md border border-zinc-200 bg-zinc-950 text-white dark:border-zinc-700 dark:bg-zinc-100 dark:text-zinc-950">
              <BrandMark className="h-5 w-5" />
            </div>
            <div>
              <h1 className="text-base font-semibold tracking-tight text-zinc-950 dark:text-zinc-100">
                AgentsTower
              </h1>
              <p className="mt-0.5 text-xs text-zinc-500 dark:text-zinc-400">
                Tower OAuth 凭据托管与 MCP 网关
              </p>
            </div>
          </div>
        </div>

        {savedLogins.length > 0 && (
          <div className="space-y-2">
            <div className="text-[11px] font-medium text-zinc-500 dark:text-zinc-400">
              已保存会话 · 快捷进入
            </div>
            <div className="divide-y divide-zinc-200 overflow-hidden rounded-md border border-zinc-200 bg-zinc-50/70 dark:divide-zinc-800 dark:border-zinc-800 dark:bg-[#09090B]">
              {savedLogins.map((entry) => (
                <div
                  key={entry.username}
                  onClick={() => !loading && handleQuickSelect(entry)}
                  role="button"
                  tabIndex={0}
                  onKeyDown={(e) => {
                    if (e.key === "Enter" || e.key === " ") handleQuickSelect(entry);
                  }}
                  className="group flex items-center justify-between gap-3 px-3 py-2.5 transition-colors hover:bg-white cursor-pointer dark:hover:bg-[#141417]"
                >
                  <div className="flex items-center gap-2.5 min-w-0">
                    <div className="flex h-7 w-7 shrink-0 items-center justify-center rounded border border-zinc-200 bg-white font-mono text-xs font-semibold text-zinc-800 dark:border-zinc-700 dark:bg-zinc-900 dark:text-zinc-200">
                      {(entry.display_name || entry.username).slice(0, 1).toUpperCase()}
                    </div>
                    <div className="min-w-0">
                      <div className="flex items-center gap-1.5">
                        <span className="truncate text-xs font-medium text-zinc-900 dark:text-zinc-100">
                          {entry.display_name || entry.username}
                        </span>
                        <span className={entry.role === "admin" ? UI.badgeAdmin : UI.badgeNeutral}>
                          {entry.role === "admin" ? "管理员" : "成员"}
                        </span>
                      </div>
                      <div className="truncate font-mono text-[11px] text-zinc-400 dark:text-zinc-500">
                        @{entry.username}
                      </div>
                    </div>
                  </div>
                  <div className="flex items-center gap-1.5 shrink-0">
                    <span className="text-[11px] font-medium text-zinc-500 group-hover:text-zinc-950 dark:text-zinc-400 dark:group-hover:text-zinc-100">
                      进入
                    </span>
                    <button
                      type="button"
                      title="移除该快捷登录记录"
                      aria-label={`移除 ${entry.username} 快捷登录记录`}
                      onClick={(event) => handleForgetEntry(event, entry)}
                      className="rounded p-1 text-zinc-400 transition-colors hover:bg-red-500/10 hover:text-red-600 cursor-pointer dark:hover:text-red-400"
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

        <SegmentedTabs
          fullWidth
          value={mode}
          onChange={(nextMode) => {
            setMode(nextMode);
            setError("");
          }}
          options={[
            { id: "login", label: "账号登录" },
            {
              id: "register",
              label: regClosed ? "注册已关闭" : regNeedsInvite ? "凭邀请码注册" : "注册新账号",
              disabled: regClosed,
              title: regClosed ? "管理员已关闭公开注册" : undefined,
            },
          ]}
        />

        {!hasUsers && mode === "register" && (
          <StatusBanner tone="info">
            当前系统尚无注册用户，首个创建的账户将自动成为<strong>系统管理员</strong>。
          </StatusBanner>
        )}

        {error && <StatusBanner tone="error">{error}</StatusBanner>}

        <form className="space-y-3.5" method="post" onSubmit={handleSubmit}>
          <div className="space-y-1.5">
            <label htmlFor="auth-username" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
              用户名
            </label>
            <input
              id="auth-username"
              type="text"
              name="username"
              autoComplete="username"
              required
              minLength={2}
              value={username}
              onChange={(e) => setUsername(e.target.value)}
              placeholder="例如: alice"
              className={UI.input}
            />
          </div>

          {mode === "register" && (
            <div className="space-y-1.5">
              <label htmlFor="auth-display-name" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                显示名称（可选）
              </label>
              <input
                id="auth-display-name"
                type="text"
                name="display_name"
                autoComplete="nickname"
                value={displayName}
                onChange={(e) => setDisplayName(e.target.value)}
                placeholder="团队内展示的姓名或昵称"
                className={UI.input}
              />
            </div>
          )}

          {mode === "register" && regNeedsInvite && (
            <div className="space-y-1.5">
              <label htmlFor="auth-invite-code" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
                注册邀请码
              </label>
              <input
                id="auth-invite-code"
                type="text"
                name="invite_code"
                required
                value={inviteCode}
                onChange={(e) => setInviteCode(e.target.value)}
                placeholder="输入管理员提供的团队邀请码"
                className={UI.input}
              />
            </div>
          )}

          <div className="space-y-1.5">
            <label htmlFor="auth-password" className="block text-xs font-medium text-zinc-700 dark:text-zinc-300">
              密码
            </label>
            <input
              id="auth-password"
              type="password"
              name="password"
              autoComplete={mode === "login" ? "current-password" : "new-password"}
              required
              minLength={6}
              value={password}
              onChange={(e) => setPassword(e.target.value)}
              placeholder={mode === "register" ? "设置至少 6 位登录密码" : "输入登录密码"}
              className={UI.input}
            />
          </div>

          <label className="flex items-center gap-2 pt-0.5 text-xs text-zinc-600 cursor-pointer select-none dark:text-zinc-400">
            <input
              type="checkbox"
              checked={rememberQuick}
              onChange={(e) => setRememberQuick(e.target.checked)}
              className="h-3.5 w-3.5 rounded border-zinc-300 accent-zinc-950 dark:border-zinc-700 dark:accent-zinc-100"
            />
            <span>在此浏览器保存会话以便下次快捷登录</span>
          </label>

          <button type="submit" disabled={loading} className={`${UI.btnPrimary} w-full py-2.5 text-xs font-semibold`}>
            {loading ? "处理中..." : mode === "register" ? "注册并进入 AgentsTower" : "登录 AgentsTower"}
          </button>
        </form>
      </div>
    </div>
  );
}
