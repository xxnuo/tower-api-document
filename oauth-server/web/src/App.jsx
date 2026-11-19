import { useEffect, useRef, useState } from "react";
import AdminPanel from "./AdminPanel.jsx";
import ApiExplorer from "./ApiExplorer.jsx";
import AuthScreen, { removeQuickLoginEntry, saveQuickLoginEntry } from "./AuthScreen.jsx";
import McpExplorer from "./McpExplorer.jsx";

const LEGACY_STORAGE_KEY = "tower_oauth_result_v1";
const ACCOUNTS_STORAGE_KEY = "tower_oauth_accounts_v1";
const THEME_STORAGE_KEY = "tower_oauth_theme_v1";

function getAccountId(data) {
  return (
    data?.account_id ||
    data?.id ||
    data?.user?.guid ||
    data?.user?.id ||
    data?.user?.email ||
    data?.token?.email ||
    data?.token?.access_token?.slice(0, 12) ||
    "default"
  );
}

function buildStoreFromServerAccounts(accounts) {
  const list = Array.isArray(accounts) ? accounts : [];
  const items = list
    .filter((item) => item?.token?.access_token)
    .map((item) => ({ ...item, id: getAccountId(item) }));
  return {
    activeId: items[0]?.id || null,
    items,
  };
}

function loadInitialStore() {
  try {
    const saved = localStorage.getItem(ACCOUNTS_STORAGE_KEY);
    if (saved) {
      const parsed = JSON.parse(saved);
      if (Array.isArray(parsed?.items)) {
        return {
          activeId: parsed.activeId || parsed.items[0]?.id || null,
          items: parsed.items,
        };
      }
    }
    const legacy = localStorage.getItem(LEGACY_STORAGE_KEY);
    if (legacy) {
      const parsed = JSON.parse(legacy);
      if (parsed?.token?.access_token) {
        const id = getAccountId(parsed);
        const migrated = { activeId: id, items: [{ ...parsed, id }] };
        localStorage.setItem(ACCOUNTS_STORAGE_KEY, JSON.stringify(migrated));
        localStorage.removeItem(LEGACY_STORAGE_KEY);
        return migrated;
      }
    }
  } catch {
    return { activeId: null, items: [] };
  }
  return { activeId: null, items: [] };
}

function maskToken(str) {
  if (!str) return "";
  if (str.length <= 12) return "*".repeat(str.length);
  return `${str.slice(0, 6)}${"*".repeat(str.length - 10)}${str.slice(-4)}`;
}

function extractCode(raw) {
  const trimmed = raw.trim().replace(/^["']|["']$/g, "");
  if (!trimmed) return "";
  try {
    const url = new URL(trimmed);
    const code = url.searchParams.get("code");
    if (code) return code.trim();
  } catch {
    const match = trimmed.match(/(?:^|[?&])code=([^&\s]+)/);
    if (match?.[1]) return decodeURIComponent(match[1]);
  }
  return trimmed;
}

function maskSecretsInText(text, secrets) {
  let out = text;
  for (const secret of secrets) {
    if (secret) {
      out = out.split(secret).join(maskToken(secret));
    }
  }
  return out;
}

function formatRemaining(token, now) {
  if (!token?.created_at || !token?.expires_in) return null;
  const diffSec = Math.floor((token.created_at + token.expires_in) * 1000 - now) / 1000;
  if (diffSec <= 0) return { expired: true, label: "已过期" };
  const hours = Math.floor(diffSec / 3600);
  const mins = Math.floor((diffSec % 3600) / 60);
  if (hours > 0) return { expired: false, label: `剩余 ${hours}小时${mins}分` };
  if (mins > 0) return { expired: false, label: `剩余 ${mins}分钟` };
  return { expired: false, label: "即将过期" };
}

function CopyField({ label, value, displayValue, heightClass = "h-16" }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  }

  return (
    <div className="min-w-0 space-y-1">
      <div className="flex items-center justify-between text-xs font-medium text-slate-500 dark:text-slate-400">
        <span>{label}</span>
        <button
          type="button"
          onClick={handleCopy}
          className="rounded-full border border-slate-200 bg-white px-2.5 py-0.5 text-[11px] font-medium text-slate-600 hover:border-violet-300 hover:text-violet-600 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:hover:border-violet-600 dark:hover:text-violet-300"
        >
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre
        className={`${heightClass} overflow-y-auto break-all whitespace-pre-wrap rounded-xl border border-slate-200/80 bg-[#f8f8fc] p-3 font-mono text-xs leading-relaxed text-slate-800 dark:border-slate-800 dark:bg-[#0e0d16] dark:text-slate-100`}
      >
        {displayValue ?? value}
      </pre>
    </div>
  );
}

export default function App() {
  const [config, setConfig] = useState({ is_oob: false });
  const [authChecking, setAuthChecking] = useState(true);
  const [hasUsers, setHasUsers] = useState(true);
  const [authUser, setAuthUser] = useState(null);
  const [store, setStore] = useState({ activeId: null, items: [] });
  const [codeInput, setCodeInput] = useState("");
  const [awaitingCode, setAwaitingCode] = useState(false);
  const [otpCode, setOtpCode] = useState("");
  const [captcha, setCaptcha] = useState("");
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [loading, setLoading] = useState(false);
  const [confirmAction, setConfirmAction] = useState("");
  const [showSecrets, setShowSecrets] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [activeTab, setActiveTab] = useState("creds");
  const [theme, setTheme] = useState(() => localStorage.getItem(THEME_STORAGE_KEY) || "system");
  const [showPasswordModal, setShowPasswordModal] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [skillCopied, setSkillCopied] = useState(false);
  const syncedIdsRef = useRef(new Set());

  const creds = store.items.find((item) => item.id === store.activeId) || store.items[0] || null;

  useEffect(() => {
    localStorage.setItem(THEME_STORAGE_KEY, theme);
    const media = window.matchMedia("(prefers-color-scheme: dark)");
    function applyTheme() {
      const isDark = theme === "dark" || (theme === "system" && media.matches);
      document.documentElement.classList.toggle("dark", isDark);
    }
    applyTheme();
    media.addEventListener("change", applyTheme);
    return () => media.removeEventListener("change", applyTheme);
  }, [theme]);

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30000);
    return () => clearInterval(timer);
  }, []);

  function handleAuthLoaded(data) {
    setAuthUser(data.user);
    setHasUsers(true);
    const serverStore = buildStoreFromServerAccounts(data.accounts);
    if (serverStore.items.length > 0) {
      setStore(serverStore);
      return;
    }
    if (data.user?.id === 1) {
      const legacy = loadInitialStore();
      if (legacy.items.length > 0) {
        setStore(legacy);
        return;
      }
    }
    setStore({ activeId: null, items: [] });
  }

  useEffect(() => {
    const configRequest = fetch("/api/config")
      .then((res) => res.json())
      .then((data) => setConfig(data))
      .catch(() => {});

    const authRequest = fetch("/api/auth/me")
      .then((res) => res.json())
      .then((data) => {
        setHasUsers(Boolean(data?.has_users));
        if (data?.registration_mode) {
          setConfig((prev) => ({ ...prev, registration_mode: data.registration_mode }));
        }
        if (data?.authenticated && data?.user) {
          if (data.session_token) {
            saveQuickLoginEntry(data.user, data.session_token);
          }
          handleAuthLoaded(data);
        }
      })
      .catch(() => {});
    Promise.all([configRequest, authRequest]).finally(() => setAuthChecking(false));
  }, []);

  useEffect(() => {
    if (!authUser) return;
    fetch("/api/auth/result")
      .then((res) => (res.status === 204 || !res.ok ? null : res.json()))
      .then((data) => {
        if (!data) return;
        if (data.error) {
          setError(data.error);
        } else if (data.otp_required) {
          setOtpCode(data.code || "");
        } else if (data.token?.access_token) {
          saveCreds(data);
          setStatus("授权登录成功");
        }
      })
      .catch(() => {});
  }, [authUser?.id]);

  useEffect(() => {
    if (!authUser) return;
    if (!creds?.token?.access_token || creds?.mcp_api_key) return;
    if (syncedIdsRef.current.has(creds.id)) return;
    syncedIdsRef.current.add(creds.id);

    fetch("/api/accounts/sync", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ token: creds.token }),
    })
      .then((res) => (res.ok ? res.json() : null))
      .then((data) => {
        if (data?.mcp_api_key) {
          saveCreds({ ...creds, ...data });
        }
      })
      .catch(() => {});
  }, [authUser?.id, creds?.id, creds?.token?.access_token, creds?.mcp_api_key]);

  useEffect(() => {
    if (!confirmAction) return;
    function onKeyDown(event) {
      if (event.key === "Escape") setConfirmAction("");
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [confirmAction]);

  function persistStore(nextStore) {
    setStore(nextStore);
    if (!authUser) return;
    const scopedKey = `${ACCOUNTS_STORAGE_KEY}_u${authUser.id}`;
    if (nextStore.items.length === 0) {
      localStorage.removeItem(scopedKey);
      if (authUser.id === 1) {
        localStorage.removeItem(ACCOUNTS_STORAGE_KEY);
        localStorage.removeItem(LEGACY_STORAGE_KEY);
      }
      return;
    }
    localStorage.setItem(scopedKey, JSON.stringify(nextStore));
  }

  function saveCreds(data) {
    const id = getAccountId(data);
    const entry = { ...data, id };
    setStore((prev) => {
      const exists = prev.items.some((item) => item.id === id);
      const items = exists
        ? prev.items.map((item) => (item.id === id ? entry : item))
        : [...prev.items, entry];
      const nextStore = { activeId: id, items };
      if (authUser) {
        localStorage.setItem(`${ACCOUNTS_STORAGE_KEY}_u${authUser.id}`, JSON.stringify(nextStore));
      }
      return nextStore;
    });
  }

  async function handleSignOutUser() {
    await fetch("/api/auth/logout", {
      method: "POST",
      headers: { "Content-Type": "application/json" },
      body: JSON.stringify({ revoke: false }),
    }).catch(() => {});
    setAuthUser(null);
    setStore({ activeId: null, items: [] });
    setActiveTab("creds");
    setError("");
    setStatus("");
  }

  async function handleSignOutAllDevices() {
    const uname = authUser?.username;
    await fetch("/api/auth/logout-all", { method: "POST" }).catch(() => {});
    if (uname) removeQuickLoginEntry(uname);
    setAuthUser(null);
    setStore({ activeId: null, items: [] });
    setActiveTab("creds");
    setError("");
    setStatus("");
  }

  async function handleChangeSelfPassword(event) {
    event.preventDefault();
    setLoading(true);
    setError("");
    setStatus("");
    try {
      const res = await fetch("/api/auth/password", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          current_password: currentPassword,
          new_password: newPassword,
        }),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      if (authUser && data.session_token) {
        saveQuickLoginEntry(authUser, data.session_token);
      }
      setShowPasswordModal(false);
      setCurrentPassword("");
      setNewPassword("");
      setStatus("登录密码已修改，其他设备会话已失效");
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  async function handleCopySkillMD() {
    try {
      const res = await fetch("/api/skill.md");
      let text = await res.text();
      if (mcpKey) {
        text = text.replace("<在 AgentsTower 凭据中心获取的 at_sk-... 密钥>", mcpKey);
      }
      await navigator.clipboard.writeText(text);
      setSkillCopied(true);
      setTimeout(() => setSkillCopied(false), 1800);
    } catch (err) {
      setError(`复制 SKILL.md 失败: ${err.message}`);
    }
  }

  async function exchangeCode(path, bodyObj) {
    setLoading(true);
    setError("");
    setStatus("");
    try {
      const body = new URLSearchParams(bodyObj);
      const res = await fetch(path, {
        method: "POST",
        headers: {
          "Content-Type": "application/x-www-form-urlencoded",
          Accept: "application/json",
        },
        body,
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      if (data.otp_required) {
        setOtpCode(data.code || bodyObj.code || "");
        return;
      }
      saveCreds(data);
      setAwaitingCode(false);
      setOtpCode("");
      setCaptcha("");
      setCodeInput("");
      setStatus("凭据获取成功");
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  function handleStartLogin() {
    setError("");
    setStatus("");
    setOtpCode("");
    setConfirmAction("");
    if (config.is_oob) {
      window.open("/login", "_blank", "noopener,noreferrer");
      setAwaitingCode(true);
      return;
    }
    window.location.href = "/login";
  }

  async function handleRefresh() {
    if (!creds?.token?.refresh_token) return;
    setLoading(true);
    setError("");
    setConfirmAction("");
    setStatus("正在刷新...");
    try {
      const res = await fetch("/oauth2/refresh", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          refresh_token: creds.token.refresh_token,
          access_token: creds.token.access_token,
        }),
      });
      const data = await res.json();
      if (!res.ok || data.error || !data.access_token) {
        throw new Error(data.error_description || data.error || `HTTP ${res.status}`);
      }
      saveCreds({ ...creds, token: data });
      setStatus("Token 已刷新");
    } catch (err) {
      setStatus("");
      setError(`刷新失败: ${err.message}`);
    } finally {
      setLoading(false);
    }
  }

  function handleSelectAccount(id) {
    setStatus("");
    setError("");
    setConfirmAction("");
    persistStore({ ...store, activeId: id });
  }

  function handleLogoutCurrent() {
    if (!creds) return;
    const credToDelete = creds.mcp_api_key || creds.token?.access_token || "";
    if (credToDelete) {
      fetch("/api/account", {
        method: "DELETE",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ credential: credToDelete }),
      }).catch(() => {});
    }
    const remaining = store.items.filter((item) => item.id !== creds.id);
    setConfirmAction("");
    setStatus(remaining.length > 0 ? "已登出并移除服务端凭据" : "");
    setError("");
    persistStore({
      activeId: remaining[0]?.id || null,
      items: remaining,
    });
  }

  function handleClearAll() {
    for (const item of store.items) {
      const credToDelete = item.mcp_api_key || item.token?.access_token || "";
      if (credToDelete) {
        fetch("/api/account", {
          method: "DELETE",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ credential: credToDelete }),
        }).catch(() => {});
      }
    }
    setAwaitingCode(false);
    setConfirmAction("");
    setStatus("");
    setError("");
    persistStore({ activeId: null, items: [] });
  }

  async function handleSyncCurrent() {
    if (!creds?.token?.access_token) return;
    setLoading(true);
    setError("");
    setStatus("正在同步至 AgentsTower 服务端...");
    try {
      const res = await fetch("/api/accounts/sync", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ token: creds.token }),
      });
      const data = await res.json();
      if (!res.ok || data.error) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      saveCreds({ ...creds, ...data });
      setStatus("凭据已同步至服务端并生成 MCP 密钥");
    } catch (err) {
      setStatus("");
      setError(`同步失败: ${err.message}`);
    } finally {
      setLoading(false);
    }
  }

  async function handleRotateMCPKey() {
    const cred = creds?.mcp_api_key || creds?.token?.access_token;
    if (!cred) return;
    setLoading(true);
    setError("");
    setConfirmAction("");
    try {
      const res = await fetch("/api/mcp-key/rotate", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ credential: cred }),
      });
      const data = await res.json();
      if (!res.ok || data.error || !data.mcp_api_key) {
        throw new Error(data.error || `HTTP ${res.status}`);
      }
      saveCreds({ ...creds, mcp_api_key: data.mcp_api_key });
      setStatus("MCP 密钥已重置，请同步更新 Agent 客户端配置");
    } catch (err) {
      setError(`重置密钥失败: ${err.message}`);
    } finally {
      setLoading(false);
    }
  }

  async function handleVerify() {
    if (!creds?.token?.access_token) return;
    setLoading(true);
    setError("");
    setStatus("正在检测凭据...");
    try {
      const res = await fetch("/api/verify", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ access_token: creds.token.access_token }),
      });
      const data = await res.json();
      if (!data.valid) {
        setStatus("");
        setError(`凭据已失效: ${data.error || "请刷新或重新登录"}`);
        return;
      }
      if (data.user) {
        saveCreds({ ...creds, user: data.user });
      }
      setStatus("凭据有效，可正常调用 Tower API");
    } catch (err) {
      setStatus("");
      setError(`检测失败: ${err.message}`);
    } finally {
      setLoading(false);
    }
  }

  const user = creds?.user;
  const token = creds?.token;
  const mcpKey = creds?.mcp_api_key || "";
  const remaining = formatRemaining(token, now);
  const isTokenExpired = Boolean(
    token?.created_at &&
      token?.expires_in &&
      now >= (token.created_at + token.expires_in) * 1000
  );
  const expiryText =
    token?.created_at && token?.expires_in
      ? `过期时间: ${new Date((token.created_at + token.expires_in) * 1000).toLocaleString()}`
      : token?.expires_in
        ? `有效期: ${token.expires_in} 秒`
        : "";
  const secretValues = [token?.access_token, token?.refresh_token, mcpKey].filter(Boolean);
  const maskIfHidden = (text) => (showSecrets ? text : maskSecretsInText(text, secretValues));
  const mcpServerURL = new URL(config.mcp_url || "/mcp", window.location.origin).href;
  const mcpConfigSnippet = JSON.stringify(
    {
      mcpServers: {
        "agents-tower": {
          type: "http",
          url: mcpServerURL,
          headers: {
            AGENTS_TOWER_API_KEY: mcpKey || "<登录后自动生成>",
          },
        },
      },
    },
    null,
    2
  );
  const envSnippet = token?.access_token
    ? `AGENTS_TOWER_API_KEY=${mcpKey}\nTOWER_ACCESS_TOKEN=${token.access_token}\nTOWER_REFRESH_TOKEN=${token.refresh_token || ""}`
    : "";
  const curlSnippet = token?.access_token
    ? `curl -H "Authorization: Bearer ${token.access_token}" https://tower.im/api/v1/user`
    : "";
  const fullJSON = creds ? JSON.stringify(creds, null, 2) : "";
  const displayName =
    authUser?.display_name || authUser?.username || user?.nickname || user?.email || "开发者";
  const towerAccountLabel = user?.nickname || user?.email || token?.email || "当前账号";
  const themeMeta = {
    system: { label: "系统", next: "light", title: "当前：跟随系统（点击切换浅色）" },
    light: { label: "浅色", next: "dark", title: "当前：浅色模式（点击切换深色）" },
    dark: { label: "深色", next: "system", title: "当前：深色模式（点击切换系统）" },
  }[theme] || { label: "系统", next: "light", title: "切换主题" };

  if (authChecking) {
    return (
      <div className="min-h-dvh flex items-center justify-center bg-[#f6f6fb] text-sm text-slate-500 dark:bg-[#0d0c14] dark:text-slate-400">
        加载中...
      </div>
    );
  }

  if (!authUser) {
    return (
      <AuthScreen
        hasUsers={hasUsers}
        registrationMode={config.registration_mode || "open"}
        onAuthenticated={handleAuthLoaded}
        theme={theme}
        onToggleTheme={() => setTheme(themeMeta.next)}
        themeLabel={themeMeta.label}
      />
    );
  }

  const kpiCards = [
    {
      label: "已保存账号",
      value: `${store.items.length}`,
      sub: store.items.length > 0 ? "服务端 SQLite 托管" : "暂无账号",
      icon: (
        <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M16 21v-2a4 4 0 0 0-4-4H6a4 4 0 0 0-4 4v2" />
          <circle cx="9" cy="7" r="4" />
          <path d="M22 21v-2a4 4 0 0 0-3-3.87" />
          <path d="M16 3.13a4 4 0 0 1 0 7.75" />
        </svg>
      ),
    },
    {
      label: "MCP 服务状态",
      value: mcpKey ? "已就绪" : token?.access_token ? "待同步" : "未登录",
      sub: `${config.mcp_tools || 50} 个 Tower 工具可用`,
      icon: (
        <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <path d="M12 22s8-4 8-10V5l-8-3-8 3v7c0 6 8 10 8 10z" />
        </svg>
      ),
    },
    {
      label: "Token 有效期",
      value: remaining ? remaining.label : "—",
      sub: token?.refresh_token ? "MCP 调用自动续期" : "需重新登录",
      icon: (
        <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <circle cx="12" cy="12" r="10" />
          <polyline points="12 6 12 12 16 14" />
        </svg>
      ),
    },
    {
      label: "授权模式",
      value: config.is_oob ? "OOB 授权码" : "自动回调",
      sub: config.is_oob ? "手动粘贴 code" : "自动回跳本页",
      icon: (
        <svg className="h-4 w-4" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
          <rect x="3" y="11" width="18" height="11" rx="2" ry="2" />
          <path d="M7 11V7a5 5 0 0 1 10 0v4" />
        </svg>
      ),
    },
  ];

  return (
    <div className="min-h-dvh lg:h-dvh lg:overflow-hidden flex flex-col lg:flex-row bg-[#f6f6fb] dark:bg-[#0d0c14]">
      <aside className="hidden lg:flex lg:w-[72px] shrink-0 flex-col items-center justify-between bg-[#14121d] px-1.5 py-5 text-slate-400 border-r border-white/5">
        <div className="flex w-full flex-col items-center gap-5">
          <div className="flex flex-col items-center gap-1">
            <div
              title="AgentsTower"
              className="flex h-10 w-10 items-center justify-center rounded-xl bg-gradient-to-br from-violet-500 to-purple-600 text-white shadow-lg shadow-violet-600/30"
            >
              <svg className="h-5 w-5" viewBox="0 0 24 24" fill="currentColor">
                <rect x="4" y="4" width="7" height="7" rx="2" />
                <rect x="13" y="4" width="7" height="7" rx="2" opacity="0.7" />
                <rect x="4" y="13" width="7" height="7" rx="2" opacity="0.7" />
                <rect x="13" y="13" width="7" height="7" rx="2" />
              </svg>
            </div>
            <span className="text-[10px] font-medium leading-tight text-slate-300">Agents</span>
          </div>
          <nav className="flex w-full flex-col items-center gap-2">
            <button
              type="button"
              title="凭据中心"
              onClick={() => setActiveTab("creds")}
              className={`flex w-full flex-col items-center gap-1 rounded-xl py-2 text-[11px] font-medium leading-tight transition-all cursor-pointer ${
                activeTab === "creds"
                  ? "bg-violet-600 text-white shadow-lg shadow-violet-600/40"
                  : "text-slate-400 hover:bg-white/10 hover:text-white"
              }`}
            >
              <svg className="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <rect x="3" y="3" width="7" height="7" rx="1.5" />
                <rect x="14" y="3" width="7" height="7" rx="1.5" />
                <rect x="3" y="14" width="7" height="7" rx="1.5" />
                <rect x="14" y="14" width="7" height="7" rx="1.5" />
              </svg>
              <span>凭据中心</span>
            </button>
            <button
              type="button"
              title="MCP 调试工具"
              onClick={() => setActiveTab("mcp")}
              className={`flex w-full flex-col items-center gap-1 rounded-xl py-2 text-[11px] font-medium leading-tight transition-all cursor-pointer ${
                activeTab === "mcp"
                  ? "bg-violet-600 text-white shadow-lg shadow-violet-600/40"
                  : "text-slate-400 hover:bg-white/10 hover:text-white"
              }`}
            >
              <svg className="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="3" />
                <circle cx="19" cy="5" r="2" />
                <circle cx="5" cy="5" r="2" />
                <circle cx="12" cy="20" r="2" />
                <path d="M14.2 9.8 17.5 6.5M9.8 9.8 6.5 6.5M12 15v3" />
              </svg>
              <span>MCP 调试</span>
            </button>
            <button
              type="button"
              title="API 调试工具"
              onClick={() => setActiveTab("explorer")}
              className={`flex w-full flex-col items-center gap-1 rounded-xl py-2 text-[11px] font-medium leading-tight transition-all cursor-pointer ${
                activeTab === "explorer"
                  ? "bg-violet-600 text-white shadow-lg shadow-violet-600/40"
                  : "text-slate-400 hover:bg-white/10 hover:text-white"
              }`}
            >
              <svg className="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <polyline points="16 18 22 12 16 6" />
                <polyline points="8 6 2 12 8 18" />
              </svg>
              <span>API 调试</span>
            </button>
            {authUser.role === "admin" && (
              <button
                type="button"
                title="成员与权限管理"
                onClick={() => setActiveTab("admin")}
                className={`flex w-full flex-col items-center gap-1 rounded-xl py-2 text-[11px] font-medium leading-tight transition-all cursor-pointer ${
                  activeTab === "admin"
                    ? "bg-violet-600 text-white shadow-lg shadow-violet-600/40"
                    : "text-slate-400 hover:bg-white/10 hover:text-white"
                }`}
              >
                <svg className="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                  <path d="M17 21v-2a4 4 0 0 0-4-4H5a4 4 0 0 0-4 4v2" />
                  <circle cx="9" cy="7" r="4" />
                  <path d="M23 21v-2a4 4 0 0 0-3-3.87" />
                  <path d="M16 3.13a4 4 0 0 1 0 7.75" />
                </svg>
                <span>成员管理</span>
              </button>
            )}
          </nav>
        </div>
        <div className="flex w-full flex-col items-center gap-2">
          <button
            type="button"
            title={themeMeta.title}
            onClick={() => setTheme(themeMeta.next)}
            className="flex w-full flex-col items-center gap-1 rounded-xl py-2 text-[11px] font-medium leading-tight text-slate-400 hover:bg-white/10 hover:text-white transition-all cursor-pointer"
          >
            {theme === "light" ? (
              <svg className="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <circle cx="12" cy="12" r="4" />
                <path d="M12 2v2M12 20v2M4.93 4.93l1.41 1.41M17.66 17.66l1.41 1.41M2 12h2M20 12h2M6.34 17.66l-1.41 1.41M19.07 4.93l-1.41 1.41" />
              </svg>
            ) : theme === "dark" ? (
              <svg className="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <path d="M21 12.79A9 9 0 1 1 11.21 3 7 7 0 0 0 21 12.79z" />
              </svg>
            ) : (
              <svg className="h-4 w-4 shrink-0" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <rect x="2" y="3" width="20" height="14" rx="2" />
                <line x1="8" y1="21" x2="16" y2="21" />
                <line x1="12" y1="17" x2="12" y2="21" />
              </svg>
            )}
            <span>{themeMeta.label}</span>
          </button>

          {store.items.length > 0 && (
            <div
              title={`切换 Tower 账号（当前：${towerAccountLabel}）`}
              className="relative flex w-full flex-col items-center gap-1 rounded-xl border border-white/10 py-2 text-[10px] font-medium leading-tight text-slate-300 hover:bg-white/10 hover:text-white transition-all"
            >
              {user?.gavatar ? (
                <img src={user.gavatar} alt="" className="h-6 w-6 rounded-full object-cover" />
              ) : (
                <div className="flex h-6 w-6 items-center justify-center rounded-full bg-violet-600/80 text-[11px] font-semibold text-white">
                  {displayName.slice(0, 1).toUpperCase()}
                </div>
              )}
              <span className="max-w-[58px] truncate">{towerAccountLabel}</span>
              <select
                aria-label="切换当前账号"
                value={creds?.id || ""}
                onChange={(event) => handleSelectAccount(event.target.value)}
                className="absolute inset-0 h-full w-full cursor-pointer opacity-0"
              >
                {store.items.map((item) => (
                  <option key={item.id} value={item.id} className="bg-slate-900 text-slate-100">
                    {item.user?.nickname || item.user?.email || item.token?.email || "已授权成员"}
                  </option>
                ))}
              </select>
            </div>
          )}
          <button
            type="button"
            title={displayName}
            popoverTarget="panel-user-actions"
            className="flex w-full flex-col items-center gap-1 rounded-xl border border-white/10 py-2 text-[10px] font-medium leading-tight text-slate-300 hover:bg-white/10 hover:text-white transition-all cursor-pointer"
          >
            <span aria-hidden="true" className="flex h-6 w-6 items-center justify-center rounded-full bg-violet-600/80 text-[11px] font-semibold text-white">
              {displayName.slice(0, 1).toUpperCase()}
            </span>
            <span className="max-w-full truncate px-1">{displayName}</span>
          </button>
          <div
            id="panel-user-actions"
            popover="auto"
            className="fixed inset-auto bottom-5 left-20 m-0 w-40 rounded-xl border border-white/10 bg-[#14121d] p-1.5 text-slate-300 shadow-lg space-y-1"
          >
            <button
              type="button"
              onClick={() => setShowPasswordModal(true)}
              className="w-full cursor-pointer rounded-lg px-3 py-2 text-left text-xs font-medium hover:bg-white/10 hover:text-white"
            >
              修改密码
            </button>
            <button
              type="button"
              onClick={handleSignOutUser}
              className="w-full cursor-pointer rounded-lg px-3 py-2 text-left text-xs font-medium hover:bg-white/10 hover:text-white"
            >
              切换用户
            </button>
            <button
              type="button"
              onClick={handleSignOutAllDevices}
              className="w-full cursor-pointer rounded-lg px-3 py-2 text-left text-xs font-medium text-red-400 hover:bg-red-950/50 hover:text-red-300"
            >
              登出全部设备
            </button>
          </div>
        </div>
      </aside>

      <div className="flex-1 min-w-0 min-h-0 flex flex-col">
        <header className="lg:hidden shrink-0 border-b border-slate-200/70 bg-white/95 px-4 py-3 backdrop-blur sm:px-7 dark:border-slate-800/80 dark:bg-[#14121d]/95">
          <div className="flex flex-wrap items-center justify-between gap-3">
            <div className="flex flex-wrap items-center gap-3">
              <div className="flex items-center gap-2.5">
                <div className="flex lg:hidden h-8 w-8 items-center justify-center rounded-xl bg-gradient-to-br from-violet-500 to-purple-600 text-xs font-bold text-white shadow-sm">
                  AT
                </div>
                <h1 className="text-base sm:text-lg font-semibold tracking-tight text-slate-900 dark:text-slate-100">
                  {activeTab === "creds"
                    ? "凭据中心"
                    : activeTab === "mcp"
                      ? "MCP 调试工具"
                      : activeTab === "explorer"
                        ? "API 调试工具"
                        : "成员管理"}
                </h1>
              </div>
              <div className="inline-flex lg:hidden rounded-full border border-slate-200 bg-slate-100/80 p-0.5 dark:border-slate-800 dark:bg-slate-950">
                <button
                  type="button"
                  onClick={() => setActiveTab("creds")}
                  className={`rounded-full px-3 py-1 text-xs font-medium transition-all cursor-pointer ${
                    activeTab === "creds"
                      ? "bg-violet-600 text-white shadow-xs"
                      : "text-slate-600 dark:text-slate-400"
                  }`}
                >
                  凭据中心
                </button>
                <button
                  type="button"
                  onClick={() => setActiveTab("mcp")}
                  className={`rounded-full px-3 py-1 text-xs font-medium transition-all cursor-pointer ${
                    activeTab === "mcp"
                      ? "bg-violet-600 text-white shadow-xs"
                      : "text-slate-600 dark:text-slate-400"
                  }`}
                >
                  MCP 调试
                </button>
                <button
                  type="button"
                  onClick={() => setActiveTab("explorer")}
                  className={`rounded-full px-3 py-1 text-xs font-medium transition-all cursor-pointer ${
                    activeTab === "explorer"
                      ? "bg-violet-600 text-white shadow-xs"
                      : "text-slate-600 dark:text-slate-400"
                  }`}
                >
                  API 调试
                </button>
                {authUser.role === "admin" && (
                  <button
                    type="button"
                    onClick={() => setActiveTab("admin")}
                    className={`rounded-full px-3 py-1 text-xs font-medium transition-all cursor-pointer ${
                      activeTab === "admin"
                        ? "bg-violet-600 text-white shadow-xs"
                        : "text-slate-600 dark:text-slate-400"
                    }`}
                  >
                    成员管理
                  </button>
                )}
              </div>
            </div>

            <div className="flex lg:hidden flex-wrap items-center gap-2.5">
              <div className="inline-flex rounded-full border border-slate-200/90 bg-slate-100/70 p-0.5 dark:border-slate-800 dark:bg-slate-950">
                {[
                  { id: "system", label: "系统" },
                  { id: "light", label: "浅色" },
                  { id: "dark", label: "深色" },
                ].map((opt) => (
                  <button
                    key={opt.id}
                    type="button"
                    onClick={() => setTheme(opt.id)}
                    className={`rounded-full px-2.5 py-1 text-xs font-medium transition-all cursor-pointer ${
                      theme === opt.id
                        ? "bg-white text-slate-900 shadow-xs dark:bg-slate-800 dark:text-slate-100"
                        : "text-slate-500 hover:text-slate-900 dark:text-slate-400 dark:hover:text-slate-200"
                    }`}
                  >
                    {opt.label}
                  </button>
                ))}
              </div>

              {store.items.length > 0 && (
                <div className="flex items-center gap-2 rounded-full border border-slate-200/80 bg-white py-1 pl-1.5 pr-2.5 dark:border-slate-800 dark:bg-slate-900">
                  {user?.gavatar ? (
                    <img src={user.gavatar} alt="" className="h-6 w-6 rounded-full object-cover" />
                  ) : (
                    <div className="flex h-6 w-6 items-center justify-center rounded-full bg-violet-100 text-[11px] font-semibold text-violet-700 dark:bg-violet-950 dark:text-violet-300">
                      {displayName.slice(0, 1).toUpperCase()}
                    </div>
                  )}
                  <select
                    aria-label="切换当前账号"
                    value={creds?.id || ""}
                    onChange={(event) => handleSelectAccount(event.target.value)}
                    className="max-w-[140px] bg-transparent text-xs font-medium text-slate-700 outline-none cursor-pointer dark:text-slate-200"
                  >
                    {store.items.map((item) => (
                      <option key={item.id} value={item.id} className="dark:bg-slate-900">
                        {item.user?.nickname || item.user?.email || item.token?.email || "已授权成员"}
                      </option>
                    ))}
                  </select>
                </div>
              )}
              <button
                type="button"
                onClick={() => setShowPasswordModal(true)}
                className="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-600 hover:text-slate-900 cursor-pointer dark:border-slate-800 dark:bg-slate-900 dark:text-slate-300"
              >
                改密
              </button>
              <button
                type="button"
                onClick={handleSignOutUser}
                className="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-600 hover:text-slate-900 cursor-pointer dark:border-slate-800 dark:bg-slate-900 dark:text-slate-300"
              >
                切换用户
              </button>
            </div>
          </div>
        </header>

      <main className="flex-1 min-h-0 flex flex-col">
      {activeTab === "admin" && authUser.role === "admin" ? (
        <AdminPanel currentUser={authUser} />
      ) : activeTab === "mcp" ? (
        <McpExplorer
          mcpKey={mcpKey}
          mcpServerURL={mcpServerURL}
          accountLabel={towerAccountLabel}
          onGoCreds={() => setActiveTab("creds")}
        />
      ) : activeTab === "explorer" ? (
        <ApiExplorer
          accessToken={token?.access_token || ""}
          accountLabel={towerAccountLabel}
        />
      ) : (
        <div className="flex-1 min-h-0 overflow-y-auto px-4 py-5 sm:px-7 sm:py-6 space-y-5">
          <div className="flex flex-wrap items-center justify-between gap-4">
            <div>
              <div className="flex flex-wrap items-center gap-2.5">
                <h2 className="text-xl sm:text-2xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
                  Hi, {displayName}
                </h2>
                <span
                  className={`rounded-full px-2.5 py-0.5 text-xs font-medium ${
                    authUser.role === "admin"
                      ? "bg-violet-100 text-violet-700 dark:bg-violet-950 dark:text-violet-300"
                      : "bg-slate-200/70 text-slate-600 dark:bg-slate-800 dark:text-slate-300"
                  }`}
                >
                  {authUser.role === "admin" ? "系统管理员" : "普通用户"}
                </span>
              </div>
              <p className="mt-0.5 text-xs sm:text-sm text-slate-500 dark:text-slate-400">
                欢迎回到 AgentsTower 控制台 · 成员凭据已托管至服务端 SQLite 并支持 Agent MCP 接入
              </p>
            </div>
          </div>

          <div className="grid grid-cols-2 xl:grid-cols-4 gap-4">
            {kpiCards.map((card) => (
              <div
                key={card.label}
                className="rounded-2xl border border-slate-100/90 bg-white p-4 sm:p-5 shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] dark:border-slate-800/80 dark:bg-[#171522]"
              >
                <div className="flex items-center gap-2.5">
                  <div className="flex h-8 w-8 shrink-0 items-center justify-center rounded-full bg-violet-100 text-violet-600 dark:bg-violet-950/80 dark:text-violet-300">
                    {card.icon}
                  </div>
                  <span className="text-xs font-medium text-slate-500 dark:text-slate-400">
                    {card.label}
                  </span>
                </div>
                <div className="mt-3 truncate text-lg sm:text-xl font-bold tracking-tight text-slate-900 dark:text-slate-100">
                  {card.value}
                </div>
                <div className="mt-0.5 truncate text-[11px] text-slate-400 dark:text-slate-500">
                  {card.sub}
                </div>
              </div>
            ))}
          </div>

        <div className="grid grid-cols-1 xl:grid-cols-12 gap-5 items-start">
        <div className="xl:col-span-9 min-w-0 rounded-2xl border border-slate-100/90 bg-white p-4 sm:p-6 shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] space-y-5 dark:border-slate-800/80 dark:bg-[#171522]">

        {store.items.length > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs font-medium text-slate-500 dark:text-slate-400 mr-1">已保存账号：</span>
            {store.items.map((item) => {
              const active = item.id === creds?.id;
              const label = item.user?.nickname || item.user?.email || item.token?.email || "已授权成员";
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => handleSelectAccount(item.id)}
                  className={`inline-flex items-center gap-2 rounded-full border px-3 py-1 text-xs font-medium cursor-pointer transition-colors ${
                    active
                      ? "border-violet-600 bg-violet-50 text-violet-700 dark:bg-violet-950/60 dark:text-violet-300"
                      : "border-slate-200 bg-white text-slate-700 hover:bg-slate-50 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
                  }`}
                >
                  {item.user?.gavatar && (
                    <img src={item.user.gavatar} alt="" className="h-4 w-4 rounded-full object-cover" />
                  )}
                  <span>{label}</span>
                </button>
              );
            })}
            <button
              type="button"
              onClick={handleStartLogin}
              className="inline-flex shrink-0 items-center gap-1.5 rounded-full bg-violet-600 px-3 py-1 text-xs font-medium text-white shadow-sm shadow-violet-600/25 hover:bg-violet-700 cursor-pointer"
            >
              <span aria-hidden="true">+</span>
              添加账号
            </button>
          </div>
        )}

        {error && (
          <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700 dark:border-red-900/60 dark:bg-red-950/40 dark:text-red-300">
            {error}
          </div>
        )}

        {otpCode ? (
          <form
            className="space-y-3 rounded-xl border border-amber-200 bg-amber-50/60 p-4 dark:border-amber-900/60 dark:bg-amber-950/30"
            onSubmit={(event) => {
              event.preventDefault();
              exchangeCode("/oauth2/otp", { code: otpCode, captcha });
            }}
          >
            <div className="text-sm font-medium text-slate-800 dark:text-slate-200">
              该账号开启了两步认证，请输入 Tower 发送的验证码：
            </div>
            <div className="flex flex-wrap gap-2">
              <input
                type="text"
                value={captcha}
                onChange={(event) => setCaptcha(event.target.value)}
                placeholder="输入验证码"
                required
                autoFocus
                className="w-60 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
              <button
                type="submit"
                disabled={loading}
                className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60 cursor-pointer"
              >
                提交验证
              </button>
              <button
                type="button"
                onClick={() => {
                  setOtpCode("");
                  setCaptcha("");
                }}
                className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
              >
                取消
              </button>
            </div>
          </form>
        ) : awaitingCode ? (
          <form
            className="space-y-3 rounded-xl border border-blue-200 bg-blue-50/50 p-4 dark:border-blue-900/60 dark:bg-blue-950/30"
            onSubmit={(event) => {
              event.preventDefault();
              exchangeCode("/oauth2/code", { code: extractCode(codeInput) });
            }}
          >
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <span className="font-medium text-slate-800 dark:text-slate-200">
                在新打开的 Tower 页面完成登录并同意授权后，页面上会显示一串授权码（即 code）：
              </span>
              <a
                href="/login"
                target="_blank"
                rel="noopener noreferrer"
                className="text-xs text-blue-600 hover:underline dark:text-blue-400"
              >
                重新打开授权页
              </a>
            </div>
            <p className="text-xs text-slate-600 dark:text-slate-400">
              请完整复制 Tower 页面中显示的授权码并粘贴到下方输入框，点击「获取凭据」即可换取 Token（如授权页自动登录了旧账号，请先在 tower.im 退出或使用无痕窗口）。
            </p>
            <div className="flex flex-wrap gap-2">
              <input
                type="text"
                value={codeInput}
                onChange={(event) => setCodeInput(event.target.value)}
                placeholder="粘贴授权码 code"
                required
                autoFocus
                className="min-w-64 flex-1 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
              />
              <button
                type="submit"
                disabled={loading}
                className="rounded-lg bg-blue-600 px-4 py-2 text-sm font-medium text-white hover:bg-blue-700 disabled:opacity-60 cursor-pointer"
              >
                {loading ? "获取中..." : "获取凭据"}
              </button>
              <button
                type="button"
                onClick={() => {
                  setAwaitingCode(false);
                  setCodeInput("");
                }}
                className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-300 dark:hover:bg-slate-700"
              >
                取消
              </button>
            </div>
          </form>
        ) : null}

        {token?.access_token ? (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-slate-200 bg-slate-50 p-3 dark:border-slate-800 dark:bg-slate-950/60">
              <div className="flex items-center gap-3 min-w-0">
                {user?.gavatar && (
                  <img
                    src={user.gavatar}
                    alt="avatar"
                    className="h-9 w-9 rounded-full object-cover shrink-0"
                  />
                )}
                <div className="min-w-0">
                  <div className="flex flex-wrap items-center gap-2">
                    <span className="truncate font-semibold text-sm text-slate-900 dark:text-slate-100">
                      {user?.nickname || token.email || "已授权成员"}
                    </span>
                    {remaining && (
                      <span
                        className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
                          remaining.expired
                            ? "bg-red-100 text-red-700 dark:bg-red-950 dark:text-red-300"
                            : "bg-emerald-100 text-emerald-700 dark:bg-emerald-950 dark:text-emerald-300"
                        }`}
                      >
                        {remaining.label}
                      </span>
                    )}
                  </div>
                  <div className="truncate text-xs text-slate-500 dark:text-slate-400">
                    {[user?.email || token.email, expiryText].filter(Boolean).join(" · ")}
                  </div>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  onClick={() => setShowSecrets((prev) => !prev)}
                  className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
                >
                  {showSecrets ? "隐藏明文" : "显示明文"}
                </button>
                <button
                  type="button"
                  disabled={loading}
                  onClick={handleVerify}
                  className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 disabled:opacity-60 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
                >
                  检测有效性
                </button>
                <button
                  type="button"
                  disabled={loading || !token.refresh_token}
                  onClick={() => (isTokenExpired ? handleRefresh() : setConfirmAction("refresh"))}
                  className="rounded-full bg-violet-600 px-3.5 py-1.5 text-xs font-medium text-white hover:bg-violet-700 disabled:opacity-60 cursor-pointer"
                >
                  刷新 Token
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmAction("logout")}
                  className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
                >
                  登出当前账号
                </button>
                {store.items.length > 1 && (
                  <button
                    type="button"
                    onClick={() => setConfirmAction("clear")}
                    className="rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs text-red-600 hover:bg-red-50 cursor-pointer dark:border-red-900/60 dark:bg-slate-800 dark:text-red-400 dark:hover:bg-red-950/40"
                  >
                    清空全部账号
                  </button>
                )}
              </div>
            </div>

            {status && <div className="text-xs font-medium text-emerald-600 dark:text-emerald-400">{status}</div>}

            <div className="rounded-xl border border-violet-200/80 bg-violet-50/40 p-4 space-y-3 dark:border-violet-900/50 dark:bg-violet-950/20">
              <div className="flex flex-wrap items-center justify-between gap-2">
                <div>
                  <div className="flex items-center gap-2">
                    <h3 className="text-sm font-semibold text-slate-900 dark:text-slate-100">
                      Agent MCP 接入配置
                    </h3>
                    <span className="rounded-full bg-violet-100 px-2 py-0.5 font-mono text-[11px] font-medium text-violet-700 dark:bg-violet-950 dark:text-violet-300">
                      Streamable HTTP
                    </span>
                  </div>
                  <p className="mt-0.5 text-xs text-slate-500 dark:text-slate-400">
                    将以下配置添加到 Claude Code、Codex、Cursor 等客户端即可让 Agent 直接操作你的 Tower 账号（Token 过期由服务端自动续期）。
                  </p>
                </div>
                <div className="flex flex-wrap items-center gap-2">
                  <button
                    type="button"
                    onClick={handleCopySkillMD}
                    className="rounded-full border border-violet-200 bg-white px-3 py-1 text-xs font-medium text-violet-700 hover:bg-violet-50 cursor-pointer dark:border-violet-800 dark:bg-slate-800 dark:text-violet-300"
                  >
                    {skillCopied ? "已复制 SKILL.md" : "复制 SKILL.md"}
                  </button>
                  <a
                    href="/api/skill.md?download=1"
                    download="SKILL.md"
                    className="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:border-violet-300 hover:text-violet-600 dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
                  >
                    下载 SKILL.md
                  </a>
                  {mcpKey ? (
                    <button
                      type="button"
                      disabled={loading}
                      onClick={() => setConfirmAction("rotate_mcp")}
                      className="rounded-full border border-slate-200 bg-white px-3 py-1 text-xs font-medium text-slate-700 hover:border-red-300 hover:text-red-600 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:border-red-700 dark:hover:text-red-400"
                    >
                      重置 MCP 密钥
                    </button>
                  ) : (
                    <button
                      type="button"
                      disabled={loading}
                      onClick={handleSyncCurrent}
                      className="rounded-full bg-violet-600 px-3.5 py-1 text-xs font-medium text-white hover:bg-violet-700 cursor-pointer"
                    >
                      同步生成 MCP 密钥
                    </button>
                  )}
                </div>
              </div>

              {mcpKey && (
                <CopyField
                  label="AGENTS_TOWER_API_KEY（服务端内部鉴权密钥）"
                  value={mcpKey}
                  displayValue={maskIfHidden(mcpKey)}
                  heightClass="h-12"
                />
              )}

              <CopyField
                label="mcpServers 客户端配置（直接粘贴到 mcp.json）"
                value={mcpConfigSnippet}
                displayValue={maskIfHidden(mcpConfigSnippet)}
                heightClass="h-44"
              />
            </div>

            <details className="group rounded-xl border border-slate-200/80 bg-slate-50/50 dark:border-slate-800 dark:bg-slate-950/40">
              <summary className="flex list-none items-center justify-between gap-2 px-4 py-3 text-xs font-medium text-slate-600 hover:text-slate-900 cursor-pointer select-none dark:text-slate-400 dark:hover:text-slate-200 [&::-webkit-details-marker]:hidden">
                <span>原始 OAuth 凭据详情（Access / Refresh Token、.env、curl）</span>
                <svg
                  className="h-4 w-4 shrink-0 text-slate-400 transition-transform group-open:rotate-180"
                  viewBox="0 0 24 24"
                  fill="none"
                  stroke="currentColor"
                  strokeWidth="2"
                >
                  <polyline points="6 9 12 15 18 9" />
                </svg>
              </summary>
              <div className="space-y-3 border-t border-slate-200/70 p-4 dark:border-slate-800">
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <CopyField
                label="Access Token"
                value={token.access_token}
                displayValue={maskIfHidden(token.access_token)}
                heightClass="h-16"
              />
              <CopyField
                label="Refresh Token"
                value={token.refresh_token || "(无)"}
                displayValue={token.refresh_token ? maskIfHidden(token.refresh_token) : "(无)"}
                heightClass="h-16"
              />
                </div>
            <CopyField
              label="Authorization 请求头"
              value={`Bearer ${token.access_token}`}
              displayValue={maskIfHidden(`Bearer ${token.access_token}`)}
              heightClass="h-14"
            />
                <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <CopyField
                label=".env 变量"
                value={envSnippet}
                displayValue={maskIfHidden(envSnippet)}
                heightClass="h-16"
              />
              <CopyField
                label="curl 测试命令"
                value={curlSnippet}
                displayValue={maskIfHidden(curlSnippet)}
                heightClass="h-16"
              />
                </div>
            <CopyField
              label="完整 JSON 凭据"
              value={fullJSON}
              displayValue={maskIfHidden(fullJSON)}
              heightClass="h-40"
            />
                <div className="space-y-1.5 rounded-lg border border-slate-200 bg-slate-50 px-3.5 py-2.5 text-sm dark:border-slate-800 dark:bg-slate-950/60">
                  <div className="font-medium text-slate-800 dark:text-slate-200">官方 API 文档：</div>
                  <div className="flex flex-col gap-1">
                    <a
                      href="https://docs.tower.im/"
                      target="_blank"
                      rel="noopener noreferrer"
                      className="break-all font-medium text-violet-600 hover:underline dark:text-violet-400"
                    >
                      https://docs.tower.im/
                    </a>
                    <a
                      href="https://github.com/mycolorway/tower-api-document"
                      target="_blank"
                      rel="noopener noreferrer"
                      className="break-all font-medium text-violet-600 hover:underline dark:text-violet-400"
                    >
                      https://github.com/mycolorway/tower-api-document
                    </a>
                  </div>
                </div>
              </div>
            </details>
          </div>
        ) : (
          !awaitingCode &&
          !otpCode && (
            <div className="rounded-xl border border-dashed border-slate-200 p-6 text-center space-y-3 dark:border-slate-700">
              <p className="text-sm text-slate-500 dark:text-slate-400">
                点击下方按钮会新开 Tower 授权页；登录并同意授权后，页面会显示一串授权码（code），将其复制粘贴回本页即可获取接口凭据。
              </p>
              <button
                type="button"
                onClick={handleStartLogin}
                className="inline-flex items-center rounded-lg bg-blue-600 px-5 py-2.5 text-sm font-medium text-white hover:bg-blue-700 cursor-pointer"
              >
                登录 Tower 账号
              </button>
            </div>
          )
        )}
      </div>

      <div className="xl:col-span-3 min-w-0 rounded-2xl border border-slate-100/90 bg-white p-4 sm:p-5 shadow-[0_2px_12px_-4px_rgba(15,23,42,0.04)] space-y-3 dark:border-slate-800/80 dark:bg-[#171522]">
        <h2 className="text-sm font-semibold text-slate-900 dark:text-slate-100">使用说明</h2>
        <ul className="list-disc pl-5 space-y-1.5 text-sm text-slate-600 dark:text-slate-300">
          <li>登录 Tower 账号并完成授权后，AgentsTower 会将 OAuth 凭据加密保存至服务端 SQLite，并为你生成专属的内部鉴权密钥 <code className="rounded bg-slate-100 px-1 py-0.5 font-mono text-xs dark:bg-slate-800">AGENTS_TOWER_API_KEY</code>。</li>
          <li>复制左侧「mcpServers 客户端配置」到你的 AI Agent（Claude Code、Codex、Cursor 等），即可通过 HTTP MCP 调用全部 Tower 工具。</li>
          <li>Agent 调用 MCP 时若 Tower Access Token 过期，服务端会自动通过 Refresh Token 静默续期，无需人工干预。</li>
          <li>
            直接调试原始 Tower API 时，可在请求头携带{" "}
            <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs text-slate-800 dark:bg-slate-800 dark:text-slate-200">
              Authorization: Bearer &lt;access_token&gt;
            </code>
            。
          </li>
        </ul>
      </div>
      </div>
      </div>
      )}
      </main>
      </div>

      {confirmAction && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 px-4 dark:bg-black/60"
          onClick={() => setConfirmAction("")}
        >
          <div
            role="dialog"
            aria-modal="true"
            className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-6 shadow-xl space-y-4 dark:border-slate-700 dark:bg-slate-900"
            onClick={(event) => event.stopPropagation()}
          >
            <h3 className="text-base font-semibold text-slate-900 dark:text-slate-100">
              {confirmAction === "refresh"
                ? "确认刷新 Token"
                : confirmAction === "rotate_mcp"
                  ? "确认重置 MCP 密钥"
                : confirmAction === "logout"
                  ? "确认登出当前账号"
                  : "确认清空全部账号"}
            </h3>
            <p className="text-sm text-slate-600 dark:text-slate-300">
              {confirmAction === "refresh"
                ? "当前 Access Token 尚未过期，刷新后旧 Token 将立即失效。是否继续刷新？"
                : confirmAction === "rotate_mcp"
                  ? "重置后旧的 AGENTS_TOWER_API_KEY 将立即失效，所有已配置该密钥的 AI Agent 需更新新密钥后才能继续调用。"
                : confirmAction === "logout"
                  ? `确定移除「${user?.nickname || user?.email || token?.email || "当前账号"}」的本地与服务端凭据吗？该账号关联的 MCP 密钥也将一并失效。`
                  : `确定清空所有（${store.items.length} 个）账号的本地与服务端凭据吗？该操作不可恢复。`}
            </p>
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={() => setConfirmAction("")}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm text-slate-700 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200 dark:hover:bg-slate-700"
              >
                取消
              </button>
              <button
                type="button"
                disabled={loading}
                onClick={() => {
                  if (confirmAction === "refresh") handleRefresh();
                  else if (confirmAction === "rotate_mcp") handleRotateMCPKey();
                  else if (confirmAction === "logout") handleLogoutCurrent();
                  else handleClearAll();
                }}
                className={`rounded-lg px-4 py-2 text-sm font-medium text-white disabled:opacity-60 cursor-pointer ${
                  confirmAction === "refresh"
                    ? "bg-amber-600 hover:bg-amber-700"
                    : "bg-red-600 hover:bg-red-700"
                }`}
              >
                {confirmAction === "refresh"
                  ? "确认刷新"
                  : confirmAction === "rotate_mcp"
                    ? "确认重置"
                  : confirmAction === "logout"
                    ? "确认登出"
                    : "确认清空"}
              </button>
            </div>
          </div>
        </div>
      )}

      {showPasswordModal && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/50 px-4 dark:bg-black/60"
          onClick={() => setShowPasswordModal(false)}
        >
          <form
            role="dialog"
            aria-modal="true"
            onSubmit={handleChangeSelfPassword}
            className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-6 shadow-xl space-y-4 dark:border-slate-700 dark:bg-slate-900"
            onClick={(event) => event.stopPropagation()}
          >
            <h3 className="text-base font-semibold text-slate-900 dark:text-slate-100">
              修改当前账号密码
            </h3>
            <div className="space-y-3">
              <div className="space-y-1">
                <label className="text-xs font-medium text-slate-600 dark:text-slate-300">
                  当前密码
                </label>
                <input
                  type="password"
                  required
                  autoFocus
                  autoComplete="current-password"
                  value={currentPassword}
                  onChange={(e) => setCurrentPassword(e.target.value)}
                  placeholder="输入当前登录密码"
                  className="w-full rounded-xl border border-slate-300 bg-white px-3.5 py-2 text-sm outline-none focus:border-violet-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
              </div>
              <div className="space-y-1">
                <label className="text-xs font-medium text-slate-600 dark:text-slate-300">
                  新密码（至少 6 位）
                </label>
                <input
                  type="password"
                  required
                  minLength={6}
                  autoComplete="new-password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  placeholder="输入新密码"
                  className="w-full rounded-xl border border-slate-300 bg-white px-3.5 py-2 text-sm outline-none focus:border-violet-500 dark:border-slate-700 dark:bg-slate-950 dark:text-slate-100"
                />
              </div>
            </div>
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={() => setShowPasswordModal(false)}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm text-slate-700 hover:bg-slate-50 cursor-pointer dark:border-slate-700 dark:bg-slate-800 dark:text-slate-200"
              >
                取消
              </button>
              <button
                type="submit"
                disabled={loading}
                className="rounded-lg bg-violet-600 px-4 py-2 text-sm font-medium text-white hover:bg-violet-700 disabled:opacity-60 cursor-pointer"
              >
                {loading ? "保存中..." : "确认修改"}
              </button>
            </div>
          </form>
        </div>
      )}
    </div>
  );
}
