import { useEffect, useRef, useState } from "react";
import {
  CaretUpDownIcon,
  CheckIcon,
  CopyIcon,
  CpuIcon,
  DesktopIcon,
  DownloadSimpleIcon,
  KeyIcon,
  LockKeyIcon,
  MoonIcon,
  PlusIcon,
  SignOutIcon,
  SunIcon,
  TerminalWindowIcon,
  UsersIcon,
} from "@phosphor-icons/react";
import AdminPanel from "./AdminPanel.jsx";
import ApiExplorer from "./ApiExplorer.jsx";
import AuthScreen, { removeQuickLoginEntry, saveQuickLoginEntry } from "./AuthScreen.jsx";
import McpExplorer from "./McpExplorer.jsx";
import { Badge } from "@/components/ui/badge";
import { Button, buttonVariants } from "@/components/ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import {
  Dialog,
  DialogContent,
  DialogFooter,
  DialogHeader,
  DialogDescription,
  DialogTitle,
} from "@/components/ui/dialog";
import {
  DropdownMenu,
  DropdownMenuContent,
  DropdownMenuGroup,
  DropdownMenuItem,
  DropdownMenuLabel,
  DropdownMenuSeparator,
  DropdownMenuTrigger,
} from "@/components/ui/dropdown-menu";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import {
  Select,
  SelectContent,
  SelectItem,
  SelectTrigger,
  SelectValue,
} from "@/components/ui/select";
import { Separator } from "@/components/ui/separator";
import { BrandMark, ConfirmDialog, CopyField, StatusBanner, TONE_BADGE } from "@/components/shared";

const LEGACY_STORAGE_KEY = "tower_oauth_result_v1";
const ACCOUNTS_STORAGE_KEY = "tower_oauth_accounts_v1";
const THEME_STORAGE_KEY = "tower_oauth_theme_v1";

function readTabFromHash() {
  const tab = window.location.hash.slice(1);
  return ["mcp", "explorer", "admin"].includes(tab) ? tab : "creds";
}

function navigateToTab(tab) {
  window.location.hash = tab;
}

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
  if (diffSec <= 0) return { expired: true, soon: false, label: "已过期" };
  const hours = Math.floor(diffSec / 3600);
  const mins = Math.floor((diffSec % 3600) / 60);
  if (hours > 0) return { expired: false, soon: false, label: `剩余 ${hours}小时${mins}分` };
  if (mins > 0) return { expired: false, soon: mins < 15, label: `剩余 ${mins}分钟` };
  return { expired: false, soon: true, label: "即将过期" };
}

export default function App() {
  const [config, setConfig] = useState({ is_oob: false });
  const [authChecking, setAuthChecking] = useState(true);
  const [hasUsers, setHasUsers] = useState(true);
  const [authUser, setAuthUser] = useState(null);
  const [store, setStore] = useState({ activeId: null, items: [] });
  const [otpCode, setOtpCode] = useState("");
  const [captcha, setCaptcha] = useState("");
  const [error, setError] = useState("");
  const [status, setStatus] = useState("");
  const [loading, setLoading] = useState(false);
  const [confirmAction, setConfirmAction] = useState("");
  const [showSecrets, setShowSecrets] = useState(false);
  const [now, setNow] = useState(() => Date.now());
  const [requestedTab, setRequestedTab] = useState(readTabFromHash);
  const [theme, setTheme] = useState(() => localStorage.getItem(THEME_STORAGE_KEY) || "system");
  const [showPasswordModal, setShowPasswordModal] = useState(false);
  const [currentPassword, setCurrentPassword] = useState("");
  const [newPassword, setNewPassword] = useState("");
  const [skillCopied, setSkillCopied] = useState(false);
  const [loginTarget, setLoginTarget] = useState(null);
  const [grant, setGrant] = useState(null);
  const [webEmail, setWebEmail] = useState("");
  const [webPassword, setWebPassword] = useState("");
  const [webMessage, setWebMessage] = useState("");
  const [webQR, setWebQR] = useState("");
  const webPollRef = useRef(null);
  const syncedIdsRef = useRef(new Set());

  const activeTab = ["admin", "explorer"].includes(requestedTab) && authUser?.role !== "admin" ? "creds" : requestedTab;
  const creds = store.items.find((item) => item.id === store.activeId) || store.items[0] || null;

  useEffect(() => {
    function syncTab() {
      setRequestedTab(readTabFromHash());
    }
    window.addEventListener("hashchange", syncTab);
    return () => window.removeEventListener("hashchange", syncTab);
  }, []);

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

  useEffect(() => {
    setWebQR("");
    setWebMessage("");
    return () => webPollRef.current?.abort();
  }, [creds?.id, activeTab, authUser?.id]);

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
    window.location.replace("#creds");
    setError("");
    setStatus("");
  }

  async function handleSignOutAllDevices() {
    const uname = authUser?.username;
    await fetch("/api/auth/logout-all", { method: "POST" }).catch(() => {});
    if (uname) removeQuickLoginEntry(uname);
    setAuthUser(null);
    setStore({ activeId: null, items: [] });
    window.location.replace("#creds");
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
      setOtpCode("");
      setCaptcha("");
      setStatus("凭据获取成功");
    } catch (err) {
      setError(err.message);
    } finally {
      setLoading(false);
    }
  }

  function openTowerLogin(accountId) {
    stopWebPoll();
    setGrant(null);
    setCaptcha("");
    setWebEmail(accountId ? user?.email || token?.email || "" : "");
    setWebPassword("");
    setWebMessage("");
    setWebQR("");
    setLoginTarget(accountId);
  }

  function handleStartLogin() {
    openTowerLogin("");
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

  function stopWebPoll() {
    webPollRef.current?.abort();
    webPollRef.current = null;
  }

  async function reloadAccounts(accountId) {
    const res = await fetch("/api/accounts");
    const data = await res.json();
    if (!res.ok || data.error) throw new Error(data.error || `HTTP ${res.status}`);
    const next = buildStoreFromServerAccounts(data.accounts);
    setStore((prev) => ({
      activeId: accountId || (next.items.some((item) => item.id === prev.activeId) ? prev.activeId : next.activeId),
      items: next.items,
    }));
  }

  async function acceptWebResult(result) {
    setWebQR("");
    setWebPassword("");
    setWebMessage("");
    if (result.challenge) {
      setGrant(result);
      setCaptcha("");
      return;
    }
    if (result.status !== "connected") throw new Error("未确认账号绑定");
    await reloadAccounts(result.account_id);
    setLoginTarget(null);
    setStatus("Tower 账号已连接");
  }

  async function continueGrant(event) {
    event.preventDefault();
    setLoading(true);
    try {
      const res = await fetch("/api/accounts/web/continue", {
        method: "POST", headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ challenge: grant.challenge, approve: grant.status === "consent", captcha }),
      });
      const data = await res.json();
      if (!res.ok) throw new Error(data.error);
      await acceptWebResult(data);
    } catch (err) {
      setGrant(null);
      setWebMessage(err.message);
    } finally { setLoading(false); }
  }

  async function handleWebEmailLogin(event) {
    event.preventDefault();
    stopWebPoll();
    setWebQR("");
    setLoading(true);
    setError("");
    setWebMessage("");
    try {
      const res = await fetch("/api/accounts/web/email", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({
          account_id: loginTarget,
          email: webEmail,
          password: webPassword,
        }),
      });
      const data = await res.json();
      if (!res.ok || data.error) throw new Error(data.error || `HTTP ${res.status}`);
      setWebPassword("");
      setWebQR("");
      stopWebPoll();
      await acceptWebResult(data);
    } catch (err) {
      setWebMessage(`网页登录失败: ${err.message}`);
    } finally {
      setLoading(false);
    }
  }

  async function handleWebWechat() {
    stopWebPoll();
    const controller = new AbortController();
    webPollRef.current = controller;
    const { signal } = controller;
    setLoading(true);
    setError("");
    setWebQR("");
    setWebMessage("正在获取二维码…");
    try {
      const res = await fetch("/api/accounts/web/wechat", {
        method: "POST",
        headers: { "Content-Type": "application/json" },
        body: JSON.stringify({ account_id: loginTarget }),
        signal,
      });
      const data = await res.json();
      if (!res.ok || !data.id || !data.image) throw new Error(data.error || "没有收到二维码");
      if (signal.aborted) return;
      setWebQR(data.image);
      setWebMessage("请用微信扫描二维码");
      setLoading(false);
      while (!signal.aborted) {
        const response = await fetch(`/api/accounts/web/wechat/${data.id}`, { signal, cache: "no-store" });
        const result = await response.json();
        if (signal.aborted) return;
        if (!response.ok || result.error) throw new Error(result.error || `HTTP ${response.status}`);
        if (result.status === "connected" || result.challenge) {
          await acceptWebResult(result);
          return;
        }
        if (result.status === "cancelled") throw new Error("已在微信中取消登录");
        if (result.status === "expired") throw new Error("二维码已过期，请重新获取");
        if (result.status === "scanned") setWebMessage("已扫码，请在微信中确认");
        else if (result.status !== "pending") throw new Error("无法识别扫码状态，请重新获取二维码");
        await new Promise((resolve) => setTimeout(resolve, 2000));
      }
    } catch (err) {
      if (!signal.aborted) {
        setWebQR("");
        setWebMessage(`扫码登录失败：${err.message}`);
      }
    } finally {
      if (webPollRef.current === controller) {
        webPollRef.current = null;
        setLoading(false);
      }
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
      <div className="min-h-dvh flex items-center justify-center bg-background text-xs font-mono text-muted-foreground">
        INITIALIZING...
      </div>
    );
  }

  if (!authUser) {
    return (
      <AuthScreen
        hasUsers={hasUsers}
        registrationMode={config.registration_mode || "open"}
        onAuthenticated={handleAuthLoaded}
        onToggleTheme={() => setTheme(themeMeta.next)}
        themeLabel={themeMeta.label}
      />
    );
  }

  const navItems = [
    { id: "creds", label: "凭据中心", icon: KeyIcon, badge: store.items.length > 0 ? `${store.items.length}` : null },
    { id: "mcp", label: "MCP 调试", icon: CpuIcon, badge: mcpKey ? "就绪" : null },
    ...(authUser.role === "admin" ? [
      { id: "explorer", label: "API 调试", icon: TerminalWindowIcon },
      { id: "admin", label: "成员管理", icon: UsersIcon },
    ] : []),
  ];

  const telemetryCells = [
    {
      label: "已托管账号",
      value: `${store.items.length}`,
      sub: store.items.length > 0 ? "服务端 SQLite 持久化" : "尚未绑定 Tower 账号",
    },
    {
      label: "MCP 服务状态",
      value: mcpKey ? "已就绪" : token?.access_token ? "待同步" : "未登录",
      sub: `${config.mcp_tools || 50} 个 Tower 工具可用`,
    },
    {
      label: "Token 有效期",
      value: remaining ? remaining.label : "—",
      sub: token?.refresh_token ? "MCP 调用自动静默续期" : "需重新完成授权",
    },
    {
      label: "授权模式",
      value: "网页登录 + OAuth",
      sub: "由网关完成授权码交换",
    },
  ];

  return (
    <div className="min-h-dvh lg:h-dvh lg:overflow-hidden flex flex-col md:flex-row bg-background text-foreground">
      {/* 桌面端精致侧边栏 */}
      <aside className="hidden md:flex md:w-64 shrink-0 flex-col border-r border-sidebar-border bg-sidebar text-sidebar-foreground select-none">
        {/* 顶部 Brand 区域 */}
        <div className="p-4 border-b border-sidebar-border flex items-center justify-between gap-2">
          <a
            href="#creds"
            onClick={(e) => {
              e.preventDefault();
              navigateToTab("creds");
            }}
            className="flex items-center gap-2.5 min-w-0 group"
          >
            <span className="flex size-7 items-center justify-center rounded-lg bg-primary text-primary-foreground shadow-xs transition-transform group-hover:scale-105">
              <BrandMark className="size-4" />
            </span>
            <div className="flex flex-col min-w-0">
              <span className="font-heading text-sm font-semibold tracking-tight text-sidebar-foreground leading-tight">AgentsTower</span>
              <span className="text-[10px] text-muted-foreground font-mono leading-none mt-0.5">Tower MCP Gateway</span>
            </div>
          </a>
          <Badge variant="outline" className="text-[10px] px-1.5 py-0 font-mono text-muted-foreground">
            v0.1
          </Badge>
        </div>

        {/* 中间主要区域：Tower 账号卡片 + 导航链接 */}
        <div className="flex-1 overflow-y-auto p-3 space-y-4">
          {store.items.length > 0 && (
            <div className="space-y-1.5">
              <div className="flex items-center justify-between px-1 text-[11px] font-medium text-muted-foreground">
                <span>当前 Tower 账号</span>
                <span className="font-mono text-[10px]">{store.items.length} 个可用</span>
              </div>
              <Select value={creds?.id || ""} onValueChange={handleSelectAccount}>
                <SelectTrigger size="sm" aria-label="切换当前 Tower 账号" className="w-full text-xs h-8">
                  {user?.gavatar ? (
                    <img src={user.gavatar} alt="" className="size-3.5 rounded object-cover shrink-0" />
                  ) : (
                    <span className="flex size-3.5 shrink-0 items-center justify-center rounded bg-muted font-mono text-[9px] font-semibold">
                      {towerAccountLabel.slice(0, 1).toUpperCase()}
                    </span>
                  )}
                  <SelectValue>{towerAccountLabel}</SelectValue>
                </SelectTrigger>
                <SelectContent align="start" className="w-56">
                  {store.items.map((item) => (
                    <SelectItem key={item.id} value={item.id} className="text-xs">
                      {item.user?.nickname || item.user?.email || item.token?.email || "已授权成员"}
                    </SelectItem>
                  ))}
                </SelectContent>
              </Select>
            </div>
          )}

          <nav aria-label="主导航" className="space-y-1">
            <div className="px-1 pb-1 text-[11px] font-medium text-muted-foreground">功能导航</div>
            {navItems.map((item) => {
              const active = activeTab === item.id;
              const Icon = item.icon;
              return (
                <button
                  key={item.id}
                  type="button"
                  onClick={() => navigateToTab(item.id)}
                  className={`w-full flex items-center justify-between rounded-lg px-2.5 py-2 text-sm font-medium transition-colors cursor-pointer group ${
                    active
                      ? "bg-sidebar-accent text-sidebar-accent-foreground font-semibold shadow-xs"
                      : "text-muted-foreground hover:bg-sidebar-accent/60 hover:text-sidebar-foreground"
                  }`}
                >
                  <div className="flex items-center gap-2.5 min-w-0">
                    <Icon className={`size-4 shrink-0 transition-transform group-hover:scale-110 ${active ? "text-sidebar-primary" : "text-muted-foreground"}`} />
                    <span className="truncate">{item.label}</span>
                  </div>
                  {item.badge && (
                    <span
                      className={`text-[10px] font-mono px-1.5 py-0.2 rounded-full font-medium ${
                        active
                          ? "bg-primary text-primary-foreground"
                          : "bg-muted text-muted-foreground"
                      }`}
                    >
                      {item.badge}
                    </span>
                  )}
                </button>
              );
            })}
          </nav>
        </div>

        {/* 底部用户信息与快捷操作 */}
        <div className="p-3 border-t border-sidebar-border bg-sidebar/50 space-y-2">
          <div className="flex items-center gap-1.5">
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button
                    type="button"
                    variant="ghost"
                    size="sm"
                    className="flex-1 justify-between h-9 px-2 hover:bg-sidebar-accent text-left font-normal"
                    title={displayName}
                  />
                }
              >
                <div className="flex items-center gap-2 min-w-0">
                  <span
                    aria-hidden="true"
                    className="flex size-6 shrink-0 items-center justify-center rounded-md bg-primary font-mono text-xs font-semibold text-primary-foreground shadow-xs"
                  >
                    {displayName.slice(0, 1).toUpperCase()}
                  </span>
                  <div className="flex flex-col min-w-0 text-left">
                    <span className="truncate text-xs font-medium text-sidebar-foreground leading-tight">
                      {displayName}
                    </span>
                    <span className="text-[10px] text-muted-foreground leading-none mt-0.5">
                      {authUser.role === "admin" ? "管理员" : "成员"}
                    </span>
                  </div>
                </div>
                <CaretUpDownIcon className="size-3.5 text-muted-foreground shrink-0 ml-1" />
              </DropdownMenuTrigger>
              <DropdownMenuContent align="start" side="top" className="w-52">
                <DropdownMenuGroup>
                  <DropdownMenuLabel className="space-y-0.5">
                    <div className="truncate text-xs font-medium text-foreground">{displayName}</div>
                    <div className="text-[10px] font-normal text-muted-foreground">
                      {authUser.role === "admin" ? "系统管理员权限" : "团队成员权限"}
                    </div>
                  </DropdownMenuLabel>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => setShowPasswordModal(true)}>
                  <LockKeyIcon className="size-3.5 mr-1" />
                  修改密码
                </DropdownMenuItem>
                <DropdownMenuItem onClick={handleSignOutUser}>
                  <SignOutIcon className="size-3.5 mr-1" />
                  切换用户
                </DropdownMenuItem>
                <DropdownMenuSeparator />
                <DropdownMenuItem variant="destructive" onClick={handleSignOutAllDevices}>
                  <SignOutIcon className="size-3.5 mr-1" />
                  登出全部设备
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>

            <Button
              type="button"
              variant="outline"
              size="icon-sm"
              className="shrink-0 size-8"
              title={`切换主题（当前: ${themeMeta.label}）`}
              onClick={() => setTheme(themeMeta.next)}
            >
              {theme === "light" ? (
                <SunIcon className="size-4" />
              ) : theme === "dark" ? (
                <MoonIcon className="size-4" />
              ) : (
                <DesktopIcon className="size-4" />
              )}
            </Button>
          </div>
        </div>
      </aside>

      {/* 移动端顶部紧凑导航条 */}
      <div className="flex md:hidden flex-col border-b border-sidebar-border bg-sidebar shrink-0">
        <div className="flex items-center justify-between px-3 py-2 border-b border-sidebar-border/60">
          <div className="flex items-center gap-2">
            <span className="flex size-6 items-center justify-center rounded-md bg-primary text-primary-foreground shadow-xs">
              <BrandMark className="size-3.5" />
            </span>
            <span className="font-heading text-sm font-semibold tracking-tight">AgentsTower</span>
          </div>
          <div className="flex items-center gap-1.5">
            <Button
              type="button"
              variant="ghost"
              size="icon-sm"
              className="size-7"
              title={themeMeta.title}
              onClick={() => setTheme(themeMeta.next)}
            >
              {theme === "light" ? (
                <SunIcon className="size-3.5" />
              ) : theme === "dark" ? (
                <MoonIcon className="size-3.5" />
              ) : (
                <DesktopIcon className="size-3.5" />
              )}
            </Button>
            <DropdownMenu>
              <DropdownMenuTrigger
                render={
                  <Button type="button" variant="ghost" size="icon-sm" className="size-7" title={displayName} />
                }
              >
                <span
                  aria-hidden="true"
                  className="flex size-5 shrink-0 items-center justify-center rounded bg-primary font-mono text-[10px] font-semibold text-primary-foreground"
                >
                  {displayName.slice(0, 1).toUpperCase()}
                </span>
              </DropdownMenuTrigger>
              <DropdownMenuContent align="end" className="w-48">
                <DropdownMenuGroup>
                  <DropdownMenuLabel className="space-y-0.5">
                    <div className="truncate text-xs font-medium text-foreground">{displayName}</div>
                    <div className="text-[10px] font-normal text-muted-foreground">
                      {authUser.role === "admin" ? "系统管理员" : "团队成员"}
                    </div>
                  </DropdownMenuLabel>
                </DropdownMenuGroup>
                <DropdownMenuSeparator />
                <DropdownMenuItem onClick={() => setShowPasswordModal(true)}>修改密码</DropdownMenuItem>
                <DropdownMenuItem onClick={handleSignOutUser}>切换用户</DropdownMenuItem>
                <DropdownMenuItem variant="destructive" onClick={handleSignOutAllDevices}>
                  登出全部设备
                </DropdownMenuItem>
              </DropdownMenuContent>
            </DropdownMenu>
          </div>
        </div>
        <nav
          aria-label="移动端主导航"
          className="flex items-center gap-1 overflow-x-auto px-2 py-1.5"
        >
          {navItems.map((item) => {
            const active = activeTab === item.id;
            const Icon = item.icon;
            return (
              <button
                key={item.id}
                type="button"
                onClick={() => navigateToTab(item.id)}
                className={`flex items-center gap-1.5 shrink-0 rounded-md px-2.5 py-1.5 text-xs font-medium transition-colors cursor-pointer ${
                  active ? "bg-sidebar-accent text-sidebar-accent-foreground font-semibold" : "text-muted-foreground"
                }`}
              >
                <Icon className="size-3.5 shrink-0" />
                <span>{item.label}</span>
                {item.badge && (
                  <span className="text-[9px] font-mono px-1 py-0.2 rounded-full bg-muted text-muted-foreground">
                    {item.badge}
                  </span>
                )}
              </button>
            );
          })}
        </nav>
      </div>

      <main className="flex-1 min-h-0 min-w-0 flex flex-col">
        {activeTab === "admin" && authUser.role === "admin" ? (
          <AdminPanel currentUser={authUser} />
        ) : activeTab === "mcp" ? (
          <McpExplorer
            mcpKey={mcpKey}
            mcpServerURL={mcpServerURL}
            accountLabel={towerAccountLabel}
            onGoCreds={() => navigateToTab("creds")}
          />
        ) : activeTab === "explorer" ? (
          <ApiExplorer
            accessToken={token?.access_token || ""}
            accountLabel={towerAccountLabel}
          />
        ) : (
          <div className="flex-1 min-h-0 overflow-y-auto">
            <div className="mx-auto w-full max-w-[1360px] px-4 py-5 sm:px-6 sm:py-6 space-y-5">
              <div className="flex flex-wrap items-center justify-between gap-4 border-b pb-4">
                <div>
                  <div className="flex flex-wrap items-center gap-2">
                    <h1 className="font-heading text-lg font-semibold tracking-tight">凭据中心</h1>
                    <Badge variant={authUser.role === "admin" ? "default" : "secondary"}>
                      {authUser.role === "admin" ? "系统管理员" : "普通用户"}
                    </Badge>
                    <span className="font-mono text-xs text-muted-foreground">
                      @{authUser.username || displayName}
                    </span>
                  </div>
                  <p className="mt-1 text-xs text-muted-foreground">
                    成员 Tower OAuth 2.0 凭据托管至服务端 SQLite，并签发专属 AI Agent MCP 鉴权密钥。
                  </p>
                </div>
                <Button type="button" size="sm" onClick={handleStartLogin}>
                  <PlusIcon />
                  绑定 Tower 账号
                </Button>
              </div>

              <div className="grid grid-cols-2 lg:grid-cols-4 gap-px overflow-hidden rounded-xl border bg-border">
                {telemetryCells.map((cell) => (
                  <div key={cell.label} className="bg-card px-4 py-3.5">
                    <div className="text-[11px] font-medium text-muted-foreground">{cell.label}</div>
                    <div className="mt-1 truncate font-heading text-base font-semibold tracking-tight tabular-nums">
                      {cell.value}
                    </div>
                    <div className="mt-0.5 truncate text-[11px] text-muted-foreground">{cell.sub}</div>
                  </div>
                ))}
              </div>

              <div className="grid grid-cols-1 xl:grid-cols-12 gap-5 items-start">
                <Card className="xl:col-span-8 min-w-0">
                  <CardContent className="space-y-4">
                    {store.items.length > 0 && (
                      <div className="flex flex-wrap items-center gap-1.5 border-b pb-3.5">
                        <span className="mr-1 text-xs font-medium text-muted-foreground">已绑定账号</span>
                        {store.items.map((item) => {
                          const active = item.id === creds?.id;
                          const label =
                            item.user?.nickname || item.user?.email || item.token?.email || "已授权成员";
                          return (
                            <Button
                              key={item.id}
                              type="button"
                              size="sm"
                              variant={active ? "default" : "outline"}
                              onClick={() => handleSelectAccount(item.id)}
                            >
                              {item.user?.gavatar && (
                                <img src={item.user.gavatar} alt="" className="size-3.5 rounded-sm object-cover" />
                              )}
                              <span>{label}</span>
                            </Button>
                          );
                        })}
                        <Button type="button" size="sm" variant="outline" onClick={handleStartLogin}>
                          <PlusIcon />
                          添加账号
                        </Button>
                      </div>
                    )}

                    {error && <StatusBanner tone="error">{error}</StatusBanner>}

                    {otpCode ? (
                      <form
                        className="space-y-3 rounded-lg border border-amber-500/30 bg-amber-500/8 p-4"
                        onSubmit={(event) => {
                          event.preventDefault();
                          exchangeCode("/oauth2/otp", { code: otpCode, captcha });
                        }}
                      >
                        <div className="text-xs font-medium">
                          该账号开启了两步认证，请输入 Tower 发送的验证码：
                        </div>
                        <div className="flex flex-wrap gap-2">
                          <Input
                            type="text"
                            value={captcha}
                            onChange={(event) => setCaptcha(event.target.value)}
                            placeholder="输入验证码"
                            required
                            autoFocus
                            className="w-56 font-mono text-xs"
                          />
                          <Button type="submit" size="sm" disabled={loading}>
                            提交验证
                          </Button>
                          <Button
                            type="button"
                            variant="outline"
                            size="sm"
                            onClick={() => {
                              setOtpCode("");
                              setCaptcha("");
                            }}
                          >
                            取消
                          </Button>
                        </div>
                      </form>
                    ) : null}

                    {token?.access_token ? (
                      <div className="space-y-4">
                        <div className="flex flex-wrap items-center justify-between gap-3 rounded-lg border bg-muted/30 p-3.5">
                          <div className="flex items-center gap-3 min-w-0">
                            {user?.gavatar ? (
                              <img
                                src={user.gavatar}
                                alt="avatar"
                                className="size-9 rounded-md border object-cover shrink-0"
                              />
                            ) : (
                              <div className="flex size-9 shrink-0 items-center justify-center rounded-md border bg-background font-mono text-xs font-semibold">
                                {towerAccountLabel.slice(0, 1).toUpperCase()}
                              </div>
                            )}
                            <div className="min-w-0">
                              <div className="flex flex-wrap items-center gap-2">
                                <span className="truncate text-sm font-semibold">
                                  {user?.nickname || token.email || "已授权成员"}
                                </span>
                                {remaining && (
                                  <Badge
                                    variant="outline"
                                    className={
                                      remaining.expired
                                        ? TONE_BADGE.danger
                                        : remaining.soon
                                          ? TONE_BADGE.warning
                                          : TONE_BADGE.success
                                    }
                                  >
                                    <span
                                      className={`size-1.5 rounded-full ${
                                        remaining.expired
                                          ? "bg-red-500"
                                          : remaining.soon
                                            ? "bg-amber-500"
                                            : "bg-emerald-500"
                                      }`}
                                    />
                                    <span className="tabular-nums">{remaining.label}</span>
                                  </Badge>
                                )}
                              </div>
                              <div className="mt-0.5 truncate font-mono text-[11px] tabular-nums text-muted-foreground">
                                {[user?.email || token.email, expiryText].filter(Boolean).join(" · ")}
                              </div>
                            </div>
                          </div>

                          <div className="flex flex-wrap items-center gap-1.5">
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              onClick={() => setShowSecrets((prev) => !prev)}
                            >
                              {showSecrets ? "隐藏明文" : "显示明文"}
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              disabled={loading}
                              onClick={handleVerify}
                            >
                              检测有效性
                            </Button>
                            <Button
                              type="button"
                              size="sm"
                              disabled={loading || !token.refresh_token}
                              onClick={() => (isTokenExpired ? handleRefresh() : setConfirmAction("refresh"))}
                            >
                              刷新 Token
                            </Button>
                            <Button
                              type="button"
                              variant="outline"
                              size="sm"
                              onClick={() => setConfirmAction("logout")}
                            >
                              登出当前账号
                            </Button>
                            {store.items.length > 1 && (
                              <Button
                                type="button"
                                variant="destructive"
                                size="sm"
                                onClick={() => setConfirmAction("clear")}
                              >
                                清空全部
                              </Button>
                            )}
                          </div>
                        </div>

                        {status && <StatusBanner tone="success">{status}</StatusBanner>}

                        <div className="flex items-center justify-between gap-2 text-xs">
                          <span>{creds.web_connected ? "Tower 网页已连接" : "此账号尚未连接 Tower 网页"}</span>
                          <Button type="button" size="sm" variant="outline" onClick={() => openTowerLogin(creds.id)}>
                            {creds.web_connected ? "重新连接网页" : "连接网页"}
                          </Button>
                        </div>

                        <Card size="sm" className="shadow-none">
                          <CardHeader className="border-b">
                            <div className="flex items-center gap-2">
                              <CardTitle className="text-sm">Agent MCP 接入配置</CardTitle>
                              <Badge variant="secondary" className="font-mono">
                                Streamable HTTP
                              </Badge>
                            </div>
                            <CardDescription className="text-xs">
                              添加到 Claude Code、Codex、Cursor 等客户端后，Agent 可直接调用 Tower 工具，Token 过期由服务端自动续期。
                            </CardDescription>
                            <CardAction className="flex flex-wrap items-center gap-1.5">
                              <Button type="button" variant="outline" size="sm" onClick={handleCopySkillMD}>
                                {skillCopied ? <CheckIcon /> : <CopyIcon />}
                                {skillCopied ? "已复制 SKILL.md" : "复制 SKILL.md"}
                              </Button>
                              <a
                                href="/api/skill.md?download=1"
                                download="SKILL.md"
                                className={buttonVariants({ variant: "outline", size: "sm" })}
                              >
                                <DownloadSimpleIcon />
                                下载 SKILL.md
                              </a>
                              {mcpKey ? (
                                <Button
                                  type="button"
                                  variant="destructive"
                                  size="sm"
                                  disabled={loading}
                                  onClick={() => setConfirmAction("rotate_mcp")}
                                >
                                  重置 MCP 密钥
                                </Button>
                              ) : (
                                <Button
                                  type="button"
                                  size="sm"
                                  disabled={loading}
                                  onClick={handleSyncCurrent}
                                >
                                  同步生成 MCP 密钥
                                </Button>
                              )}
                            </CardAction>
                          </CardHeader>

                          <CardContent className="space-y-3.5">
                            {mcpKey && (
                              <CopyField
                                label="AGENTS_TOWER_API_KEY（服务端内部鉴权密钥）"
                                value={mcpKey}
                                displayValue={maskIfHidden(mcpKey)}
                                heightClass="h-11"
                              />
                            )}

                            <CopyField
                              label="mcpServers 客户端配置（直接粘贴到 mcp.json）"
                              value={mcpConfigSnippet}
                              displayValue={maskIfHidden(mcpConfigSnippet)}
                              heightClass="min-h-44 max-h-64"
                            />
                          </CardContent>
                        </Card>

                        {authUser.role === "admin" && (
                          <details className="group rounded-lg border bg-muted/20">
                            <summary className="flex list-none items-center justify-between gap-2 px-4 py-3 text-xs font-medium text-muted-foreground hover:text-foreground cursor-pointer select-none [&::-webkit-details-marker]:hidden">
                              <span>原始 OAuth 凭据详情（Access / Refresh Token、.env、curl）</span>
                              <svg
                                className="size-3.5 shrink-0 transition-transform group-open:rotate-180"
                                viewBox="0 0 24 24"
                                fill="none"
                                stroke="currentColor"
                                strokeWidth="2"
                              >
                                <polyline points="6 9 12 15 18 9" />
                              </svg>
                            </summary>
                            <div className="space-y-3 border-t p-4">
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
                                heightClass="h-12"
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
                              <div className="rounded-lg border bg-card px-3.5 py-2.5 text-xs">
                                <div className="font-medium">官方 API 文档</div>
                                <div className="mt-1 flex flex-col gap-1 font-mono text-[11px]">
                                  <a
                                    href="https://docs.tower.im/"
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="break-all text-muted-foreground underline underline-offset-2 hover:text-foreground"
                                  >
                                    https://docs.tower.im/
                                  </a>
                                  <a
                                    href="https://github.com/mycolorway/tower-api-document"
                                    target="_blank"
                                    rel="noopener noreferrer"
                                    className="break-all text-muted-foreground underline underline-offset-2 hover:text-foreground"
                                  >
                                    https://github.com/mycolorway/tower-api-document
                                  </a>
                                </div>
                              </div>
                            </div>
                          </details>
                        )}
                      </div>
                    ) : (
                      !otpCode && (
                        <div className="rounded-lg border border-dashed p-8 text-center space-y-3.5">
                          <p className="mx-auto max-w-md text-xs leading-relaxed text-muted-foreground">
                            使用邮箱或微信登录 Tower 后，即可在本页管理凭据、获取{" "}
                            <code className="font-mono text-foreground">at_sk-...</code> 密钥并接入 AI Agent。
                          </p>
                          <Button type="button" onClick={handleStartLogin}>
                            登录 Tower 账号
                          </Button>
                        </div>
                      )
                    )}
                  </CardContent>
                </Card>

                <Card className="xl:col-span-4 min-w-0">
                  <CardHeader className="border-b">
                    <CardTitle className="text-sm">使用说明</CardTitle>
                  </CardHeader>
                  <CardContent>
                    <ol className="space-y-3 text-xs leading-relaxed text-muted-foreground">
                      <li className="flex gap-2.5">
                        <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded border font-mono text-[10px] font-semibold text-foreground">
                          1
                        </span>
                        <span>
                          登录 Tower 账号并完成授权后，AgentsTower 会将 OAuth 凭据加密保存至服务端 SQLite，并签发专属内部鉴权密钥{" "}
                          <code className="rounded border bg-muted px-1 py-0.5 font-mono text-[11px] text-foreground">
                            AGENTS_TOWER_API_KEY
                          </code>
                          。
                        </span>
                      </li>
                      <li className="flex gap-2.5">
                        <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded border font-mono text-[10px] font-semibold text-foreground">
                          2
                        </span>
                        <span>
                          复制左侧「mcpServers 客户端配置」到 Claude Code、Codex、Cursor 等客户端，即可通过 Streamable HTTP MCP 调用全部 Tower 工具。
                        </span>
                      </li>
                      <li className="flex gap-2.5">
                        <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded border font-mono text-[10px] font-semibold text-foreground">
                          3
                        </span>
                        <span>
                          Agent 调用 MCP 时若 Access Token 过期，服务端会自动通过 Refresh Token 静默续期，无需人工干预。
                        </span>
                      </li>
                      <li className="flex gap-2.5">
                        <span className="mt-0.5 flex size-4 shrink-0 items-center justify-center rounded border font-mono text-[10px] font-semibold text-foreground">
                          4
                        </span>
                        <span>
                          直接调试原始 Tower REST API 时，可在请求头携带{" "}
                          <code className="rounded border bg-muted px-1 py-0.5 font-mono text-[11px] text-foreground">
                            Authorization: Bearer &lt;access_token&gt;
                          </code>
                          。
                        </span>
                      </li>
                    </ol>
                  </CardContent>
                </Card>
              </div>
            </div>
          </div>
        )}
      </main>

      <ConfirmDialog
        open={Boolean(confirmAction)}
        onOpenChange={(open) => {
          if (!open) setConfirmAction("");
        }}
        title={
          confirmAction === "refresh"
            ? "确认刷新 Token"
            : confirmAction === "rotate_mcp"
              ? "确认重置 MCP 密钥"
              : confirmAction === "logout"
                ? "确认登出当前账号"
                : "确认清空全部账号"
        }
        description={
          confirmAction === "refresh"
            ? "当前 Access Token 尚未过期，刷新后旧 Token 将立即失效。是否继续刷新？"
            : confirmAction === "rotate_mcp"
              ? "重置后旧的 AGENTS_TOWER_API_KEY 将立即失效，所有已配置该密钥的 AI Agent 需更新新密钥后才能继续调用。"
              : confirmAction === "logout"
                ? `确定移除「${user?.nickname || user?.email || token?.email || "当前账号"}」的本地与服务端凭据吗？该账号关联的 MCP 密钥也将一并失效。`
                : `确定清空所有（${store.items.length} 个）账号的本地与服务端凭据吗？该操作不可恢复。`
        }
        confirmLabel={
          confirmAction === "refresh"
            ? "确认刷新"
            : confirmAction === "rotate_mcp"
              ? "确认重置"
              : confirmAction === "logout"
                ? "确认登出"
                : "确认清空"
        }
        confirmVariant={confirmAction === "refresh" ? "default" : "destructive"}
        disabled={loading}
        onConfirm={() => {
          if (confirmAction === "refresh") handleRefresh();
          else if (confirmAction === "rotate_mcp") handleRotateMCPKey();
          else if (confirmAction === "logout") handleLogoutCurrent();
          else handleClearAll();
        }}
      />

      <Dialog open={loginTarget !== null} onOpenChange={(open) => {
        if (!open && !loading) {
          stopWebPoll();
          setLoginTarget(null);
          setWebPassword("");
          setGrant(null);
        }
      }}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>{loginTarget ? "连接 Tower 网页" : "添加 Tower 账号"}</DialogTitle>
            <DialogDescription>{loginTarget ? "使用对应的 Tower 账号登录。" : "使用微信或邮箱登录，同时连接 Tower 网页和 API。"}</DialogDescription>
          </DialogHeader>
          {grant ? (
            <form className="space-y-3" onSubmit={continueGrant}>
              <p className="text-sm">{grant.message}</p>
              {grant.status === "otp" && <Input required autoComplete="one-time-code" aria-label="验证码" value={captcha} onChange={e => setCaptcha(e.target.value)} />}
              <Button type="submit" disabled={loading}>{grant.status === "otp" ? "提交验证码" : "同意授权"}</Button>
            </form>
          ) : (
            <>
              <form className="space-y-3" onSubmit={handleWebEmailLogin}>
                <Input type="email" required autoComplete="username" aria-label="Tower 邮箱" placeholder="Tower 邮箱" value={webEmail} onChange={e => setWebEmail(e.target.value)} />
                <Input type="password" required autoComplete="current-password" aria-label="Tower 密码" placeholder="密码" value={webPassword} onChange={e => setWebPassword(e.target.value)} />
                <div className="flex gap-2">
                  <Button type="submit" disabled={loading}>邮箱登录</Button>
                  <Button type="button" variant="outline" disabled={loading} onClick={handleWebWechat}>微信扫码</Button>
                </div>
              </form>
              {webQR && <img src={webQR} alt="微信登录二维码" className="size-40 rounded-md border bg-white" />}
            </>
          )}
          {webMessage && <div role="status" className="text-xs text-muted-foreground">{webMessage}</div>}
        </DialogContent>
      </Dialog>

      <Dialog open={showPasswordModal} onOpenChange={setShowPasswordModal}>
        <DialogContent className="sm:max-w-md">
          <DialogHeader>
            <DialogTitle>修改当前账号密码</DialogTitle>
          </DialogHeader>
          <form onSubmit={handleChangeSelfPassword} className="space-y-4">
            <div className="space-y-3">
              <div className="space-y-1.5">
                <Label htmlFor="self-current-pw" className="text-xs">
                  当前密码
                </Label>
                <Input
                  id="self-current-pw"
                  type="password"
                  required
                  autoFocus
                  autoComplete="current-password"
                  value={currentPassword}
                  onChange={(e) => setCurrentPassword(e.target.value)}
                  placeholder="输入当前登录密码"
                />
              </div>
              <div className="space-y-1.5">
                <Label htmlFor="self-new-pw" className="text-xs">
                  新密码（至少 6 位）
                </Label>
                <Input
                  id="self-new-pw"
                  type="password"
                  required
                  minLength={6}
                  autoComplete="new-password"
                  value={newPassword}
                  onChange={(e) => setNewPassword(e.target.value)}
                  placeholder="输入新密码"
                />
              </div>
            </div>
            <DialogFooter>
              <Button type="button" variant="outline" onClick={() => setShowPasswordModal(false)}>
                取消
              </Button>
              <Button type="submit" disabled={loading}>
                {loading ? "保存中..." : "确认修改"}
              </Button>
            </DialogFooter>
          </form>
        </DialogContent>
      </Dialog>
    </div>
  );
}
