import { useEffect, useState } from "react";
import { XIcon } from "@phosphor-icons/react";
import { Badge } from "@/components/ui/badge";
import { Button } from "@/components/ui/button";
import { Card, CardContent, CardDescription, CardHeader, CardTitle } from "@/components/ui/card";
import { Checkbox } from "@/components/ui/checkbox";
import { Input } from "@/components/ui/input";
import { Label } from "@/components/ui/label";
import { Tabs, TabsList, TabsTrigger } from "@/components/ui/tabs";
import { BrandMark, StatusBanner } from "@/components/shared";

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
  towerConnectedColleagues,
  onAuthenticated,
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
    <div className="min-h-dvh flex flex-col justify-center items-center px-4 py-10 bg-background bg-tech-grid">
      <div className="fixed top-4 right-4 z-10">
        <Button type="button" variant="outline" size="sm" onClick={onToggleTheme}>
          主题：{themeLabel}
        </Button>
      </div>

      <Card className="w-full max-w-[400px] py-6">
        <CardHeader className="border-b pb-5 px-6">
          <div className="flex items-center gap-3">
            <div className="flex size-9 shrink-0 items-center justify-center rounded-lg bg-primary text-primary-foreground">
              <BrandMark className="size-5" />
            </div>
            <div>
              <CardTitle className="text-base font-semibold tracking-tight">AgentsTower</CardTitle>
              <CardDescription className="text-xs">登录后绑定 Tower 账号，获取 MCP 接入配置。</CardDescription>
            </div>
          </div>
        </CardHeader>

        <CardContent className="px-6 space-y-5">
          {Number.isInteger(towerConnectedColleagues) && towerConnectedColleagues > 0 && (
            <p className="rounded-lg border bg-muted/30 px-3 py-2.5 text-sm leading-relaxed text-muted-foreground">
              已有 <strong className="font-semibold text-foreground">{towerConnectedColleagues}</strong> 位同事将自己的 Tower 接入了 AI 功能，你也快试试吧！
            </p>
          )}
          {savedLogins.length > 0 && (
            <div className="space-y-2">
              <div className="text-xs font-medium text-muted-foreground">已保存会话 · 快捷进入</div>
              <div className="divide-y overflow-hidden rounded-lg border bg-muted/30">
                {savedLogins.map((entry) => (
                  <div
                    key={entry.username}
                    onClick={() => !loading && handleQuickSelect(entry)}
                    role="button"
                    tabIndex={0}
                    onKeyDown={(e) => {
                      if (e.key === "Enter" || e.key === " ") handleQuickSelect(entry);
                    }}
                    className="group flex items-center justify-between gap-3 px-3 py-2.5 transition-colors hover:bg-muted/70 cursor-pointer"
                  >
                    <div className="flex items-center gap-2.5 min-w-0">
                      <div className="flex size-7 shrink-0 items-center justify-center rounded-md border bg-background font-mono text-xs font-semibold">
                        {(entry.display_name || entry.username).slice(0, 1).toUpperCase()}
                      </div>
                      <div className="min-w-0">
                        <div className="flex items-center gap-1.5">
                          <span className="truncate text-xs font-medium">
                            {entry.display_name || entry.username}
                          </span>
                          <Badge variant={entry.role === "admin" ? "default" : "secondary"} className="h-4 px-1.5 text-[10px]">
                            {entry.role === "admin" ? "管理员" : "成员"}
                          </Badge>
                        </div>
                        <div className="truncate font-mono text-[11px] text-muted-foreground">
                          @{entry.username}
                        </div>
                      </div>
                    </div>
                    <div className="flex items-center gap-1 shrink-0">
                      <span className="text-[11px] font-medium text-muted-foreground group-hover:text-foreground">
                        进入
                      </span>
                      <Button
                        type="button"
                        variant="ghost"
                        size="icon-xs"
                        title="移除该快捷登录记录"
                        aria-label={`移除 ${entry.username} 快捷登录记录`}
                        onClick={(event) => handleForgetEntry(event, entry)}
                        className="text-muted-foreground hover:text-destructive"
                      >
                        <XIcon />
                      </Button>
                    </div>
                  </div>
                ))}
              </div>
            </div>
          )}

          <Tabs
            value={mode}
            onValueChange={(nextMode) => {
              if (nextMode === "register" && regClosed) return;
              setMode(nextMode);
              setError("");
            }}
          >
            <TabsList className="w-full">
              <TabsTrigger value="login">账号登录</TabsTrigger>
              <TabsTrigger
                value="register"
                disabled={regClosed}
                title={regClosed ? "管理员已关闭公开注册" : undefined}
              >
                {regClosed ? "注册已关闭" : regNeedsInvite ? "凭邀请码注册" : "注册新账号"}
              </TabsTrigger>
            </TabsList>
          </Tabs>

          {!hasUsers && mode === "register" && (
            <StatusBanner tone="info">
              当前系统尚无注册用户，首个创建的账户将自动成为<strong>系统管理员</strong>。
            </StatusBanner>
          )}

          {error && <StatusBanner tone="error">{error}</StatusBanner>}

          <form className="space-y-3.5" method="post" onSubmit={handleSubmit}>
            <div className="space-y-1.5">
              <Label htmlFor="auth-username" className="text-xs">用户名</Label>
              <Input
                id="auth-username"
                type="text"
                name="username"
                autoComplete="username"
                required
                minLength={2}
                value={username}
                onChange={(e) => setUsername(e.target.value)}
                placeholder="例如: alice"
              />
            </div>

            {mode === "register" && (
              <div className="space-y-1.5">
                <Label htmlFor="auth-display-name" className="text-xs">显示名称（可选）</Label>
                <Input
                  id="auth-display-name"
                  type="text"
                  name="display_name"
                  autoComplete="nickname"
                  value={displayName}
                  onChange={(e) => setDisplayName(e.target.value)}
                  placeholder="团队内展示的姓名或昵称"
                />
              </div>
            )}

            {mode === "register" && regNeedsInvite && (
              <div className="space-y-1.5">
                <Label htmlFor="auth-invite-code" className="text-xs">注册邀请码</Label>
                <Input
                  id="auth-invite-code"
                  type="text"
                  name="invite_code"
                  required
                  value={inviteCode}
                  onChange={(e) => setInviteCode(e.target.value)}
                  placeholder="输入管理员提供的团队邀请码"
                />
              </div>
            )}

            <div className="space-y-1.5">
              <Label htmlFor="auth-password" className="text-xs">密码</Label>
              <Input
                id="auth-password"
                type="password"
                name="password"
                autoComplete={mode === "login" ? "current-password" : "new-password"}
                required
                minLength={6}
                value={password}
                onChange={(e) => setPassword(e.target.value)}
                placeholder={mode === "register" ? "设置至少 6 位登录密码" : "输入登录密码"}
              />
            </div>

            <div className="flex items-center gap-2 pt-0.5">
              <Checkbox
                id="auth-remember-quick"
                checked={rememberQuick}
                onCheckedChange={(checked) => setRememberQuick(Boolean(checked))}
              />
              <Label
                htmlFor="auth-remember-quick"
                className="text-xs font-normal text-muted-foreground cursor-pointer"
              >
                在此浏览器保存会话以便下次快捷登录
              </Label>
            </div>

            <Button type="submit" disabled={loading} size="lg" className="w-full">
              {loading ? "处理中..." : mode === "register" ? "注册并进入 AgentsTower" : "登录 AgentsTower"}
            </Button>
          </form>
        </CardContent>
      </Card>
    </div>
  );
}
