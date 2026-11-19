import { useEffect, useState } from "react";

export const UI = {
  panel:
    "rounded-lg border border-zinc-200 bg-white dark:border-zinc-800 dark:bg-[#111113]",
  subPanel:
    "rounded-md border border-zinc-200 bg-zinc-50/80 dark:border-zinc-800 dark:bg-[#09090B]",
  input:
    "w-full rounded-md border border-zinc-200 bg-zinc-50/70 px-3 py-2 text-sm text-zinc-900 placeholder:text-zinc-400 outline-none transition-colors focus:border-zinc-900 focus:bg-white dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-100 dark:placeholder:text-zinc-600 dark:focus:border-zinc-300 dark:focus:bg-[#111113]",
  inputSm:
    "w-full rounded-md border border-zinc-200 bg-zinc-50/70 px-2.5 py-1.5 text-xs text-zinc-900 placeholder:text-zinc-400 outline-none transition-colors focus:border-zinc-900 focus:bg-white dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-100 dark:placeholder:text-zinc-600 dark:focus:border-zinc-300 dark:focus:bg-[#111113]",
  btnPrimary:
    "inline-flex items-center justify-center gap-1.5 rounded-md bg-zinc-950 px-3.5 py-1.5 text-xs font-medium text-white transition-colors hover:bg-zinc-800 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer dark:bg-zinc-100 dark:text-zinc-950 dark:hover:bg-zinc-200",
  btnSecondary:
    "inline-flex items-center justify-center gap-1.5 rounded-md border border-zinc-200 bg-white px-3 py-1.5 text-xs font-medium text-zinc-700 transition-colors hover:border-zinc-300 hover:bg-zinc-50 hover:text-zinc-950 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer dark:border-zinc-800 dark:bg-[#111113] dark:text-zinc-300 dark:hover:border-zinc-700 dark:hover:bg-zinc-900 dark:hover:text-zinc-100",
  btnDanger:
    "inline-flex items-center justify-center gap-1.5 rounded-md bg-red-600 px-3.5 py-1.5 text-xs font-medium text-white transition-colors hover:bg-red-700 disabled:opacity-50 disabled:cursor-not-allowed cursor-pointer dark:bg-red-600 dark:hover:bg-red-500",
  btnDangerOutline:
    "inline-flex items-center justify-center gap-1.5 rounded-md border border-red-200 bg-white px-3 py-1.5 text-xs font-medium text-red-600 transition-colors hover:border-red-300 hover:bg-red-50 disabled:opacity-40 disabled:cursor-not-allowed cursor-pointer dark:border-red-900/60 dark:bg-[#111113] dark:text-red-400 dark:hover:border-red-800 dark:hover:bg-red-950/30",
  badgeNeutral:
    "inline-flex items-center gap-1 rounded border border-zinc-200 bg-zinc-100 px-1.5 py-0.5 text-[11px] font-medium text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900 dark:text-zinc-300",
  badgeAdmin:
    "inline-flex items-center gap-1 rounded border border-zinc-900/15 bg-zinc-900 text-white px-1.5 py-0.5 text-[11px] font-medium dark:border-zinc-100/20 dark:bg-zinc-100 dark:text-zinc-950",
  badgeSuccess:
    "inline-flex items-center gap-1.5 rounded border border-emerald-500/25 bg-emerald-500/10 px-2 py-0.5 text-[11px] font-medium text-emerald-700 dark:text-emerald-300",
  badgeWarning:
    "inline-flex items-center gap-1.5 rounded border border-amber-500/25 bg-amber-500/10 px-2 py-0.5 text-[11px] font-medium text-amber-700 dark:text-amber-300",
  badgeDanger:
    "inline-flex items-center gap-1.5 rounded border border-red-500/25 bg-red-500/10 px-2 py-0.5 text-[11px] font-medium text-red-700 dark:text-red-300",
  badgeInfo:
    "inline-flex items-center gap-1.5 rounded border border-sky-500/25 bg-sky-500/10 px-2 py-0.5 text-[11px] font-medium text-sky-700 dark:text-sky-300",
};

export function BrandMark({ className = "h-5 w-5" }) {
  return (
    <svg className={className} viewBox="0 0 24 24" fill="none" aria-hidden="true">
      <rect x="3" y="3" width="8" height="8" rx="1" fill="currentColor" />
      <rect x="13" y="3" width="8" height="8" rx="1" stroke="currentColor" strokeWidth="1.75" />
      <rect x="3" y="13" width="8" height="8" rx="1" stroke="currentColor" strokeWidth="1.75" />
      <rect x="13" y="13" width="8" height="8" rx="1" fill="currentColor" fillOpacity="0.35" stroke="currentColor" strokeWidth="1.75" />
    </svg>
  );
}

export function StatusBanner({ tone = "info", children, className = "" }) {
  const toneStyles = {
    error:
      "border-red-500/25 bg-red-500/8 text-red-700 dark:border-red-500/30 dark:bg-red-950/30 dark:text-red-300",
    success:
      "border-emerald-500/25 bg-emerald-500/8 text-emerald-700 dark:border-emerald-500/30 dark:bg-emerald-950/30 dark:text-emerald-300",
    warning:
      "border-amber-500/25 bg-amber-500/8 text-amber-800 dark:border-amber-500/30 dark:bg-amber-950/30 dark:text-amber-300",
    info:
      "border-zinc-200 bg-zinc-50 text-zinc-700 dark:border-zinc-800 dark:bg-zinc-900/70 dark:text-zinc-300",
  }[tone];

  return (
    <div className={`flex items-start gap-2 rounded-md border px-3.5 py-2.5 text-xs leading-relaxed ${toneStyles} ${className}`}>
      <svg
        className="mt-0.5 h-3.5 w-3.5 shrink-0"
        viewBox="0 0 24 24"
        fill="none"
        stroke="currentColor"
        strokeWidth="2"
        aria-hidden="true"
      >
        {tone === "error" ? (
          <>
            <circle cx="12" cy="12" r="10" />
            <line x1="15" y1="9" x2="9" y2="15" />
            <line x1="9" y1="9" x2="15" y2="15" />
          </>
        ) : tone === "success" ? (
          <>
            <circle cx="12" cy="12" r="10" />
            <polyline points="8.5 12.5 11 15 16 9.5" />
          </>
        ) : tone === "warning" ? (
          <>
            <path d="M10.29 3.86 1.82 18a2 2 0 0 0 1.71 3h16.94a2 2 0 0 0 1.71-3L13.71 3.86a2 2 0 0 0-3.42 0z" />
            <line x1="12" y1="9" x2="12" y2="13" />
            <line x1="12" y1="17" x2="12.01" y2="17" />
          </>
        ) : (
          <>
            <circle cx="12" cy="12" r="10" />
            <line x1="12" y1="16" x2="12" y2="12" />
            <line x1="12" y1="8" x2="12.01" y2="8" />
          </>
        )}
      </svg>
      <div className="min-w-0 flex-1">{children}</div>
    </div>
  );
}

export function SegmentedTabs({ options, value, onChange, size = "sm", fullWidth = false }) {
  return (
    <div
      className={`${fullWidth ? "grid" : "inline-flex"} rounded-md border border-zinc-200 bg-zinc-100/80 p-0.5 dark:border-zinc-800 dark:bg-[#09090B]`}
      style={fullWidth ? { gridTemplateColumns: `repeat(${options.length}, minmax(0, 1fr))` } : undefined}
    >
      {options.map((opt) => {
        const active = value === opt.id;
        return (
          <button
            key={opt.id}
            type="button"
            disabled={opt.disabled}
            title={opt.title}
            onClick={() => !opt.disabled && onChange(opt.id)}
            className={`rounded-[4px] font-medium transition-colors ${
              size === "xs" ? "px-2 py-0.5 text-[11px]" : "px-3 py-1 text-xs"
            } ${
              active
                ? "bg-white text-zinc-950 shadow-[0_1px_2px_rgba(0,0,0,0.06)] dark:bg-zinc-800 dark:text-zinc-100"
                : opt.disabled
                  ? "text-zinc-400 opacity-50 cursor-not-allowed dark:text-zinc-600"
                  : "text-zinc-500 hover:text-zinc-900 cursor-pointer dark:text-zinc-400 dark:hover:text-zinc-200"
            }`}
          >
            {opt.label}
          </button>
        );
      })}
    </div>
  );
}

export function CopyField({ label, value, displayValue, heightClass = "h-16" }) {
  const [copied, setCopied] = useState(false);

  async function handleCopy() {
    await navigator.clipboard.writeText(value);
    setCopied(true);
    setTimeout(() => setCopied(false), 1200);
  }

  return (
    <div className="min-w-0 space-y-1.5">
      <div className="flex items-center justify-between gap-2 text-xs font-medium text-zinc-500 dark:text-zinc-400">
        <span className="truncate">{label}</span>
        <button
          type="button"
          onClick={handleCopy}
          className="inline-flex shrink-0 items-center gap-1 rounded border border-zinc-200 bg-white px-2 py-0.5 text-[11px] font-medium text-zinc-600 transition-colors hover:border-zinc-300 hover:text-zinc-950 cursor-pointer dark:border-zinc-800 dark:bg-[#111113] dark:text-zinc-300 dark:hover:border-zinc-700 dark:hover:text-zinc-100"
        >
          {copied ? "已复制" : "复制"}
        </button>
      </div>
      <pre
        className={`${heightClass} overflow-y-auto break-all whitespace-pre-wrap rounded-md border border-zinc-200 bg-zinc-50/80 p-3 font-mono text-xs leading-relaxed text-zinc-800 dark:border-zinc-800 dark:bg-[#09090B] dark:text-zinc-200`}
      >
        {displayValue ?? value}
      </pre>
    </div>
  );
}

export function ModalDialog({ open, onClose, title, children }) {
  useEffect(() => {
    if (!open) return;
    function onKeyDown(event) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", onKeyDown);
    return () => window.removeEventListener("keydown", onKeyDown);
  }, [open, onClose]);

  if (!open) return null;

  return (
    <div
      className="fixed inset-0 z-50 flex items-center justify-center bg-zinc-950/45 px-4 dark:bg-black/70"
      onClick={onClose}
    >
      <div
        role="dialog"
        aria-modal="true"
        className="w-full max-w-md rounded-lg border border-zinc-200 bg-white p-5 shadow-lg space-y-4 dark:border-zinc-800 dark:bg-[#111113]"
        onClick={(event) => event.stopPropagation()}
      >
        {title && (
          <div className="flex items-center justify-between border-b border-zinc-100 pb-3 dark:border-zinc-800/80">
            <h3 className="text-sm font-semibold text-zinc-950 dark:text-zinc-100">{title}</h3>
            <button
              type="button"
              onClick={onClose}
              aria-label="关闭弹窗"
              className="rounded p-1 text-zinc-400 transition-colors hover:bg-zinc-100 hover:text-zinc-700 cursor-pointer dark:hover:bg-zinc-800 dark:hover:text-zinc-200"
            >
              <svg className="h-3.5 w-3.5" viewBox="0 0 24 24" fill="none" stroke="currentColor" strokeWidth="2">
                <line x1="18" y1="6" x2="6" y2="18" />
                <line x1="6" y1="6" x2="18" y2="18" />
              </svg>
            </button>
          </div>
        )}
        {children}
      </div>
    </div>
  );
}
