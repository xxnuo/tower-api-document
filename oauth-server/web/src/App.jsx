import { useEffect, useRef, useState } from "react";

const LEGACY_STORAGE_KEY = "tower_oauth_result_v1";
const ACCOUNTS_STORAGE_KEY = "tower_oauth_accounts_v1";

function getAccountId(data) {
  return (
    data?.id ||
    data?.user?.guid ||
    data?.user?.email ||
    data?.token?.email ||
    data?.token?.access_token?.slice(0, 12) ||
    "default"
  );
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
  if (str.length <= 12) return "••••••••";
  return `${str.slice(0, 6)}••••••${str.slice(-4)}`;
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

function CopyField({ label, value, displayValue, maxHeightClass = "max-h-24" }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  }

  return (
    <div className="min-w-0 space-y-1">
      <div className="flex items-center justify-between text-xs font-medium text-slate-500">
        <span>{label}</span>
        <button
          type="button"
          onClick={handleCopy}
          className="rounded-md border border-slate-200 bg-white px-2 py-0.5 text-xs text-slate-700 hover:bg-slate-100 cursor-pointer"
        >
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre
        className={`${maxHeightClass} overflow-y-auto break-all whitespace-pre-wrap rounded-lg bg-slate-900 p-2.5 font-mono text-xs leading-relaxed text-slate-100`}
      >
        {displayValue ?? value}
      </pre>
    </div>
  );
}

export default function App() {
  const [config, setConfig] = useState({ is_oob: false });
  const [store, setStore] = useState(loadInitialStore);
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
  const fileInputRef = useRef(null);

  const creds = store.items.find((item) => item.id === store.activeId) || store.items[0] || null;

  useEffect(() => {
    const timer = setInterval(() => setNow(Date.now()), 30000);
    return () => clearInterval(timer);
  }, []);

  useEffect(() => {
    fetch("/api/config")
      .then((res) => res.json())
      .then((data) => setConfig(data))
      .catch(() => {});

    fetch("/api/auth/result")
      .then((res) => (res.status === 204 ? null : res.json()))
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
  }, []);

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
    if (nextStore.items.length === 0) {
      localStorage.removeItem(ACCOUNTS_STORAGE_KEY);
      localStorage.removeItem(LEGACY_STORAGE_KEY);
      return;
    }
    localStorage.setItem(ACCOUNTS_STORAGE_KEY, JSON.stringify(nextStore));
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
      localStorage.setItem(ACCOUNTS_STORAGE_KEY, JSON.stringify(nextStore));
      return nextStore;
    });
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
    const remaining = store.items.filter((item) => item.id !== creds.id);
    setConfirmAction("");
    setStatus(remaining.length > 0 ? "已登出该账号" : "");
    setError("");
    persistStore({
      activeId: remaining[0]?.id || null,
      items: remaining,
    });
  }

  function handleClearAll() {
    setAwaitingCode(false);
    setConfirmAction("");
    setStatus("");
    setError("");
    persistStore({ activeId: null, items: [] });
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

  function handleExport() {
    if (store.items.length === 0) return;
    const blob = new Blob([JSON.stringify({ version: 1, ...store }, null, 2)], {
      type: "application/json",
    });
    const url = URL.createObjectURL(blob);
    const anchor = document.createElement("a");
    anchor.href = url;
    anchor.download = "tower-oauth-accounts.json";
    anchor.click();
    URL.revokeObjectURL(url);
    setStatus("已导出本地账号配置");
  }

  function handleImportFile(event) {
    const file = event.target.files?.[0];
    event.target.value = "";
    if (!file) return;
    const reader = new FileReader();
    reader.onload = () => {
      try {
        const parsed = JSON.parse(String(reader.result || ""));
        const rawItems = Array.isArray(parsed?.items)
          ? parsed.items
          : parsed?.token?.access_token
            ? [parsed]
            : [];
        const validItems = rawItems.filter((item) => item?.token?.access_token);
        if (validItems.length === 0) {
          setError("导入失败：文件中未找到有效凭据");
          return;
        }
        const merged = [...store.items];
        for (const item of validItems) {
          const id = getAccountId(item);
          const entry = { ...item, id };
          const idx = merged.findIndex((existing) => existing.id === id);
          if (idx >= 0) merged[idx] = entry;
          else merged.push(entry);
        }
        const nextActive = parsed?.activeId && merged.some((i) => i.id === parsed.activeId)
          ? parsed.activeId
          : merged[0].id;
        persistStore({ activeId: nextActive, items: merged });
        setError("");
        setStatus(`已导入 ${validItems.length} 个账号凭据`);
      } catch {
        setError("导入失败：JSON 格式无效");
      }
    };
    reader.readAsText(file);
  }

  const user = creds?.user;
  const token = creds?.token;
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
  const secretValues = [token?.access_token, token?.refresh_token].filter(Boolean);
  const maskIfHidden = (text) => (showSecrets ? text : maskSecretsInText(text, secretValues));
  const envSnippet = token?.access_token
    ? `TOWER_ACCESS_TOKEN=${token.access_token}\nTOWER_REFRESH_TOKEN=${token.refresh_token || ""}`
    : "";
  const curlSnippet = token?.access_token
    ? `curl -H "Authorization: Bearer ${token.access_token}" https://tower.im/api/v1/user`
    : "";
  const fullJSON = creds ? JSON.stringify(creds, null, 2) : "";

  return (
    <div className="mx-auto max-w-6xl px-3 py-4 sm:px-6 sm:py-6">
      <div className="grid grid-cols-1 lg:grid-cols-12 gap-4 items-start">
        <div className="lg:col-span-8 min-w-0 rounded-2xl border border-slate-200 bg-white p-4 sm:p-5 shadow-sm space-y-4">
        <div className="flex flex-wrap items-center justify-between gap-3 border-b border-slate-100 pb-3.5">
          <div className="min-w-0">
            <div className="flex flex-wrap items-center gap-2">
              <h1 className="text-lg sm:text-xl font-semibold text-slate-900">Tower OAuth 凭据中心</h1>
              <span className="rounded-full border border-slate-200 bg-slate-50 px-2 py-0.5 text-[11px] font-medium text-slate-600">
                {config.is_oob ? "OOB 授权码模式" : "自动回调模式"}
              </span>
            </div>
            <p className="mt-0.5 text-xs sm:text-sm text-slate-500">
              本页面为纯静态前端，登录凭据等数据仅存储在用户本地浏览器中，服务器不做存储。
            </p>
          </div>
          <div className="flex flex-wrap items-center gap-2 shrink-0">
            <input
              ref={fileInputRef}
              type="file"
              accept=".json,application/json"
              onChange={handleImportFile}
              className="hidden"
            />
            <button
              type="button"
              onClick={() => fileInputRef.current?.click()}
              className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-700 hover:bg-slate-50 cursor-pointer"
            >
              导入配置
            </button>
            {store.items.length > 0 && (
              <>
                <button
                  type="button"
                  onClick={handleExport}
                  className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-xs font-medium text-slate-700 hover:bg-slate-50 cursor-pointer"
                >
                  导出配置
                </button>
                <button
                  type="button"
                  onClick={handleStartLogin}
                  className="inline-flex items-center rounded-lg bg-blue-600 px-3.5 py-2 text-sm font-medium text-white hover:bg-blue-700 cursor-pointer"
                >
                  添加账号
                </button>
              </>
            )}
          </div>
        </div>

        {store.items.length > 0 && (
          <div className="flex flex-wrap items-center gap-2">
            <span className="text-xs font-medium text-slate-500 mr-1">已保存账号：</span>
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
                      ? "border-blue-600 bg-blue-50 text-blue-700"
                      : "border-slate-200 bg-white text-slate-700 hover:bg-slate-50"
                  }`}
                >
                  {item.user?.gavatar && (
                    <img src={item.user.gavatar} alt="" className="h-4 w-4 rounded-full object-cover" />
                  )}
                  <span>{label}</span>
                </button>
              );
            })}
          </div>
        )}

        {error && (
          <div className="rounded-lg border border-red-200 bg-red-50 px-4 py-3 text-sm text-red-700">
            {error}
          </div>
        )}

        {otpCode ? (
          <form
            className="space-y-3 rounded-xl border border-amber-200 bg-amber-50/60 p-4"
            onSubmit={(event) => {
              event.preventDefault();
              exchangeCode("/oauth2/otp", { code: otpCode, captcha });
            }}
          >
            <div className="text-sm font-medium text-slate-800">
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
                className="w-60 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500"
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
                className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 hover:bg-slate-50 cursor-pointer"
              >
                取消
              </button>
            </div>
          </form>
        ) : awaitingCode ? (
          <form
            className="space-y-3 rounded-xl border border-blue-200 bg-blue-50/50 p-4"
            onSubmit={(event) => {
              event.preventDefault();
              exchangeCode("/oauth2/code", { code: codeInput.trim() });
            }}
          >
            <div className="flex flex-wrap items-center justify-between gap-2 text-sm">
              <span className="font-medium text-slate-800">
                在新打开的 Tower 页面完成登录并同意授权后，页面上会显示一串授权码（即 code）：
              </span>
              <a
                href="/login"
                target="_blank"
                rel="noopener noreferrer"
                className="text-xs text-blue-600 hover:underline"
              >
                重新打开授权页
              </a>
            </div>
            <p className="text-xs text-slate-600">
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
                className="min-w-64 flex-1 rounded-lg border border-slate-300 bg-white px-3 py-2 text-sm outline-none focus:border-blue-500"
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
                className="rounded-lg border border-slate-200 bg-white px-3 py-2 text-sm text-slate-600 hover:bg-slate-50 cursor-pointer"
              >
                取消
              </button>
            </div>
          </form>
        ) : null}

        {token?.access_token ? (
          <div className="space-y-3">
            <div className="flex flex-wrap items-center justify-between gap-3 rounded-xl border border-slate-200 bg-slate-50 p-3">
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
                    <span className="truncate font-semibold text-sm text-slate-900">
                      {user?.nickname || token.email || "已授权成员"}
                    </span>
                    {remaining && (
                      <span
                        className={`rounded-full px-2 py-0.5 text-[11px] font-medium ${
                          remaining.expired
                            ? "bg-red-100 text-red-700"
                            : "bg-emerald-100 text-emerald-700"
                        }`}
                      >
                        {remaining.label}
                      </span>
                    )}
                  </div>
                  <div className="truncate text-xs text-slate-500">
                    {[user?.email || token.email, expiryText].filter(Boolean).join(" · ")}
                  </div>
                </div>
              </div>
              <div className="flex flex-wrap items-center gap-2">
                <button
                  type="button"
                  onClick={() => setShowSecrets((prev) => !prev)}
                  className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 cursor-pointer"
                >
                  {showSecrets ? "隐藏明文" : "显示明文"}
                </button>
                <button
                  type="button"
                  disabled={loading}
                  onClick={handleVerify}
                  className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 disabled:opacity-60 cursor-pointer"
                >
                  检测有效性
                </button>
                <button
                  type="button"
                  disabled={loading || !token.refresh_token}
                  onClick={() => (isTokenExpired ? handleRefresh() : setConfirmAction("refresh"))}
                  className="rounded-lg bg-blue-600 px-3 py-1.5 text-xs font-medium text-white hover:bg-blue-700 disabled:opacity-60 cursor-pointer"
                >
                  刷新 Token
                </button>
                <button
                  type="button"
                  onClick={() => setConfirmAction("logout")}
                  className="rounded-lg border border-slate-200 bg-white px-3 py-1.5 text-xs text-slate-700 hover:bg-slate-50 cursor-pointer"
                >
                  登出当前账号
                </button>
                {store.items.length > 1 && (
                  <button
                    type="button"
                    onClick={() => setConfirmAction("clear")}
                    className="rounded-lg border border-red-200 bg-white px-3 py-1.5 text-xs text-red-600 hover:bg-red-50 cursor-pointer"
                  >
                    清空全部账号
                  </button>
                )}
              </div>
            </div>

            {status && <div className="text-xs font-medium text-emerald-600">{status}</div>}

            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <CopyField
                label="Access Token"
                value={token.access_token}
                displayValue={maskIfHidden(token.access_token)}
                maxHeightClass="max-h-20"
              />
              <CopyField
                label="Refresh Token"
                value={token.refresh_token || "(无)"}
                displayValue={token.refresh_token ? maskIfHidden(token.refresh_token) : "(无)"}
                maxHeightClass="max-h-20"
              />
            </div>
            <CopyField
              label="Authorization 请求头"
              value={`Bearer ${token.access_token}`}
              displayValue={maskIfHidden(`Bearer ${token.access_token}`)}
              maxHeightClass="max-h-20"
            />
            <div className="grid grid-cols-1 sm:grid-cols-2 gap-3">
              <CopyField
                label=".env 变量"
                value={envSnippet}
                displayValue={maskIfHidden(envSnippet)}
                maxHeightClass="max-h-20"
              />
              <CopyField
                label="curl 测试命令"
                value={curlSnippet}
                displayValue={maskIfHidden(curlSnippet)}
                maxHeightClass="max-h-20"
              />
            </div>
            <CopyField
              label="完整 JSON 凭据"
              value={fullJSON}
              displayValue={maskIfHidden(fullJSON)}
              maxHeightClass="max-h-40"
            />
          </div>
        ) : (
          !awaitingCode &&
          !otpCode && (
            <div className="rounded-xl border border-dashed border-slate-200 p-6 text-center space-y-3">
              <p className="text-sm text-slate-500">
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

      <div className="lg:col-span-4 min-w-0 rounded-2xl border border-slate-200 bg-white p-4 sm:p-5 shadow-sm space-y-3">
        <h2 className="text-sm font-semibold text-slate-900">使用说明</h2>
        <div className="space-y-1.5 rounded-lg border border-slate-200 bg-slate-50 px-3.5 py-2.5 text-sm">
          <div className="font-medium text-slate-800">官方 API 文档：</div>
          <div className="flex flex-col gap-1">
            <a
              href="https://docs.tower.im/"
              target="_blank"
              rel="noopener noreferrer"
              className="break-all font-medium text-blue-600 hover:underline"
            >
              https://docs.tower.im/
            </a>
            <a
              href="https://github.com/mycolorway/tower-api-document"
              target="_blank"
              rel="noopener noreferrer"
              className="break-all font-medium text-blue-600 hover:underline"
            >
              https://github.com/mycolorway/tower-api-document
            </a>
          </div>
        </div>
        <ul className="list-disc pl-5 space-y-1.5 text-sm text-slate-600">
          <li>点击「登录 Tower 账号」或「添加账号」后，在新打开的 Tower 页面完成登录并同意授权；页面上显示的授权码即 code，复制粘贴回本页提交即可获得 Access Token 与 Refresh Token。</li>
          <li>
            调用 Tower API 时，在 HTTP 请求头中携带{" "}
            <code className="rounded bg-slate-100 px-1.5 py-0.5 font-mono text-xs text-slate-800">
              Authorization: Bearer &lt;access_token&gt;
            </code>
            （可直接复制上方「Authorization 请求头」）。
          </li>
          <li>Access Token 过期后，可直接使用页面上的「刷新 Token」按钮续期，无需重新登录。</li>
        </ul>
      </div>
      </div>

      {confirmAction && (
        <div
          className="fixed inset-0 z-50 flex items-center justify-center bg-slate-900/40 px-4"
          onClick={() => setConfirmAction("")}
        >
          <div
            role="dialog"
            aria-modal="true"
            className="w-full max-w-md rounded-2xl border border-slate-200 bg-white p-6 shadow-xl space-y-4"
            onClick={(event) => event.stopPropagation()}
          >
            <h3 className="text-base font-semibold text-slate-900">
              {confirmAction === "refresh"
                ? "确认刷新 Token"
                : confirmAction === "logout"
                  ? "确认登出当前账号"
                  : "确认清空全部账号"}
            </h3>
            <p className="text-sm text-slate-600">
              {confirmAction === "refresh"
                ? "当前 Access Token 尚未过期，刷新后旧 Token 将立即失效。是否继续刷新？"
                : confirmAction === "logout"
                  ? `确定从当前浏览器移除「${user?.nickname || user?.email || token?.email || "当前账号"}」的本地凭据吗？移除后需重新授权获取。`
                  : `确定清空当前浏览器中已保存的所有（${store.items.length} 个）账号凭据吗？该操作不可恢复。`}
            </p>
            <div className="flex justify-end gap-2 pt-2">
              <button
                type="button"
                onClick={() => setConfirmAction("")}
                className="rounded-lg border border-slate-200 bg-white px-4 py-2 text-sm text-slate-700 hover:bg-slate-50 cursor-pointer"
              >
                取消
              </button>
              <button
                type="button"
                disabled={loading}
                onClick={() => {
                  if (confirmAction === "refresh") handleRefresh();
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
                  : confirmAction === "logout"
                    ? "确认登出"
                    : "确认清空"}
              </button>
            </div>
          </div>
        </div>
      )}
    </div>
  );
}
