import { useEffect, useRef, useState } from "react";
import {
  CaretUpDownIcon,
  CheckIcon,
  CopyIcon,
  CpuIcon,
  DesktopIcon,
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
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
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
import { BrandMark, ConfirmDialog, CopyField, StatusBanner } from "@/components/shared";

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
    .filter((item) => item?.mcp_api_key || item?.token?.access_token)
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
          setStatus("Tower 账号已绑定");
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
        text = text.replace("<在 AgentsTower 账号与接入获取的 at_sk-... 密钥>", mcpKey);
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
      setStatus("Tower 账号已绑定");
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

  function handleSelectAccount(id) {
    setStatus("");
    setError("");
    setConfirmAction("");
    persistStore({ ...store, activeId: id });
  }

  async function unlinkAccounts(items) {
    setLoading(true);
    setError("");
    setStatus("");
    const removed = new Set();
    try {
      for (const item of items) {
        const res = await fetch("/api/account", {
          method: "DELETE",
          headers: { "Content-Type": "application/json" },
          body: JSON.stringify({ credential: item.mcp_api_key || item.token?.access_token }),
        });
        if (!res.ok) throw new Error((await res.json()).error || `HTTP ${res.status}`);
        removed.add(item.id);
      }
      setStatus("已解除绑定");
    } catch (err) {
      setError(`解除绑定失败：${err.message}`);
    } finally {
      const remaining = store.items.filter(item => !removed.has(item.id));
      persistStore({ activeId: remaining.find(item => item.id === store.activeId)?.id || remaining[0]?.id || null, items: remaining });
      setConfirmAction("");
      setLoading(false);
    }
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
      setStatus("MCP 接入已启用");
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

  const user = creds?.user;
  const token = creds?.token;
  const mcpKey = creds?.mcp_api_key || "";
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
    { id: "creds", label: "账号与接入", icon: KeyIcon, badge: store.items.length > 0 ? `${store.items.length}` : null },
    { id: "mcp", label: "MCP 调试", icon: CpuIcon, badge: mcpKey ? "就绪" : null },
    ...(authUser.role === "admin" ? [
      { id: "explorer", label: "API 调试", icon: TerminalWindowIcon },
      { id: "admin", label: "成员管理", icon: UsersIcon },
    ] : []),
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
                      {item.user?.nickname || item.user?.email || item.token?.email || "Tower 成员"}
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
            accountKey={mcpKey}
            accountLabel={towerAccountLabel}
          />
        ) : (
          <div className="flex-1 min-h-0 overflow-y-auto">
            <div className="mx-auto w-full max-w-[1360px] px-4 py-5 sm:px-6 sm:py-6 space-y-5">
              <header className="flex flex-wrap items-center justify-between gap-4 border-b pb-4">
                <h1 className="font-heading text-lg font-semibold tracking-tight">账号与接入</h1>
                {creds && <Button type="button" size="sm" onClick={handleStartLogin}><PlusIcon />添加 Tower 账号</Button>}
              </header>
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

              {status && <StatusBanner tone="success">{status}</StatusBanner>}
              {creds ? (
                <div className="grid items-start gap-5 xl:grid-cols-[minmax(16rem,1fr)_minmax(0,2fr)]">
                  <Card size="sm">
                    <CardHeader className="border-b"><CardTitle>已绑定账号</CardTitle></CardHeader>
                    <CardContent className="space-y-2">
                      {store.items.map(item => (
                        <button type="button" key={item.id} aria-pressed={item.id === creds.id}
                          className="flex w-full items-center justify-between gap-2 rounded-md border p-3 text-left aria-pressed:border-primary aria-pressed:bg-muted/40"
                          onClick={() => handleSelectAccount(item.id)}>
                          <span className="min-w-0">
                            <span className="block truncate text-sm font-medium">{item.user?.nickname || item.user?.email || "Tower 成员"}</span>
                            <span className="block truncate text-xs text-muted-foreground">{item.user?.email}</span>
                          </span>
                          {item.id === creds.id && <CheckIcon aria-label="当前账号" />}
                        </button>
                      ))}
                      <Button size="sm" variant="ghost" onClick={() => setConfirmAction("logout")}>解除当前账号绑定</Button>
                      {store.items.length > 1 && <Button size="sm" variant="ghost" onClick={() => setConfirmAction("clear")}>解除全部绑定</Button>}
                    </CardContent>
                  </Card>
                  <Card size="sm" className="min-w-0">
                    <CardHeader className="border-b">
                      <CardTitle>MCP 接入</CardTitle>
                      <CardDescription>将「{towerAccountLabel}」的配置复制到 MCP 客户端，即可使用 Tower 工具。</CardDescription>
                    </CardHeader>
                    <CardContent className="space-y-4">
                      {!creds.web_connected && <div className="flex flex-wrap items-center gap-2 text-sm">
                        使用网页搜索前，请补全此账号的登录。
                        <Button variant="outline" size="sm" onClick={() => openTowerLogin(creds.id)}>连接 Tower 网页</Button>
                      </div>}
                      {mcpKey ? <>
                        <CopyField label="客户端配置" value={mcpConfigSnippet} displayValue={maskIfHidden(mcpConfigSnippet)} heightClass="min-h-44" />
                        <div className="flex flex-wrap gap-2">
                          <Button size="sm" onClick={() => navigateToTab("mcp")}>测试 MCP 连接</Button>
                          <Button size="sm" variant="outline" onClick={handleCopySkillMD}>{skillCopied ? "已复制" : "复制 Agent 指南"}</Button>
                          <a href="/api/skill.md?download=1" download="SKILL.md" className={buttonVariants({variant:"outline",size:"sm"})}>下载指南</a>
                        </div>
                        <details className="space-y-3 border-t pt-3">
                          <summary className="cursor-pointer text-xs font-medium">管理 MCP 密钥</summary>
                          <CopyField label="AGENTS_TOWER_API_KEY" value={mcpKey} displayValue={maskIfHidden(mcpKey)} heightClass="h-12" />
                          <div className="flex gap-2">
                            <Button size="sm" variant="outline" onClick={() => setShowSecrets(v => !v)}>{showSecrets ? "隐藏密钥" : "显示密钥"}</Button>
                            <Button size="sm" variant="outline" onClick={() => setConfirmAction("rotate_mcp")}>重置密钥</Button>
                          </div>
                        </details>
                      </> : <Button disabled={loading} onClick={handleSyncCurrent}>启用 MCP 接入</Button>}
                    </CardContent>
                  </Card>
                </div>
              ) : !otpCode && (
                <Card className="items-center py-10 text-center">
                  <CardHeader><CardTitle>添加你的第一个 Tower 账号</CardTitle>
                    <CardDescription>使用微信扫码或邮箱密码登录，绑定后即可复制 MCP 配置。</CardDescription></CardHeader>
                  <Button onClick={handleStartLogin}><PlusIcon />添加 Tower 账号</Button>
                </Card>
              )}
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
          confirmAction === "rotate_mcp"
              ? "确认重置 MCP 密钥"
              : confirmAction === "logout"
                ? "解除当前 Tower 账号绑定"
                : "解除全部 Tower 账号绑定"
        }
        description={
          confirmAction === "rotate_mcp"
              ? "重置后旧的 AGENTS_TOWER_API_KEY 将立即失效，所有已配置该密钥的 AI Agent 需更新新密钥后才能继续调用。"
              : confirmAction === "logout"
                ? `解除「${user?.nickname || user?.email || token?.email || "当前账号"}」的绑定？该账号关联的 MCP 密钥也将一并失效。`
                : `解除所有（${store.items.length} 个）Tower 账号的绑定？该操作不可恢复。`
        }
        confirmLabel={
          confirmAction === "rotate_mcp"
              ? "确认重置"
              : confirmAction === "logout"
                ? "解除绑定"
                : "解除全部绑定"
        }
        confirmVariant="destructive"
        disabled={loading}
        onConfirm={() => {
          if (confirmAction === "rotate_mcp") handleRotateMCPKey();
          else if (confirmAction === "logout") unlinkAccounts([creds]);
          else unlinkAccounts(store.items);
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
            <DialogDescription>{loginTarget ? "使用对应的 Tower 账号登录。" : "使用微信扫码或邮箱密码登录 Tower。"}</DialogDescription>
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
