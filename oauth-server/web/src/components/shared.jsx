import { useState } from "react";
import {
  CheckCircleIcon,
  CheckIcon,
  CopyIcon,
  InfoIcon,
  WarningIcon,
  XCircleIcon,
} from "@phosphor-icons/react";
import { Alert, AlertDescription } from "@/components/ui/alert";
import { Button } from "@/components/ui/button";
import {
  Dialog,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogTitle,
} from "@/components/ui/dialog";

export const TONE_BADGE = {
  success: "border-emerald-500/25 bg-emerald-500/10 text-emerald-700 dark:text-emerald-300",
  warning: "border-amber-500/25 bg-amber-500/10 text-amber-700 dark:text-amber-300",
  danger: "border-red-500/25 bg-red-500/10 text-red-700 dark:text-red-300",
  info: "border-sky-500/25 bg-sky-500/10 text-sky-700 dark:text-sky-300",
};

export function BrandMark({ className = "size-4" }) {
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
  const Icon =
    tone === "error"
      ? XCircleIcon
      : tone === "success"
        ? CheckCircleIcon
        : tone === "warning"
          ? WarningIcon
          : InfoIcon;

  const toneClass =
    tone === "error"
      ? "border-destructive/30 bg-destructive/5 text-destructive"
      : tone === "success"
        ? "border-emerald-500/30 bg-emerald-500/8 text-emerald-700 dark:text-emerald-300"
        : tone === "warning"
          ? "border-amber-500/30 bg-amber-500/8 text-amber-800 dark:text-amber-300"
          : "border-border bg-muted/50 text-muted-foreground";

  return (
    <Alert className={`${toneClass} ${className}`}>
      <Icon weight="regular" />
      <AlertDescription className="text-xs leading-relaxed text-current">
        {children}
      </AlertDescription>
    </Alert>
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
      <div className="flex items-center justify-between gap-2">
        <span className="truncate text-xs font-medium text-muted-foreground">{label}</span>
        <Button type="button" variant="outline" size="xs" onClick={handleCopy}>
          {copied ? <CheckIcon /> : <CopyIcon />}
          {copied ? "已复制" : "复制"}
        </Button>
      </div>
      <pre
        className={`${heightClass} overflow-y-auto break-all whitespace-pre-wrap rounded-lg border bg-muted/40 p-3 font-mono text-xs leading-relaxed text-foreground`}
      >
        {displayValue ?? value}
      </pre>
    </div>
  );
}

export function ConfirmDialog({
  open,
  onOpenChange,
  title,
  description,
  children,
  confirmLabel = "确认",
  confirmVariant = "default",
  disabled = false,
  onConfirm,
}) {
  return (
    <Dialog open={open} onOpenChange={onOpenChange}>
      <DialogContent className="sm:max-w-md">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          {description && <DialogDescription>{description}</DialogDescription>}
        </DialogHeader>
        {children}
        <DialogFooter>
          <Button type="button" variant="outline" onClick={() => onOpenChange(false)}>
            取消
          </Button>
          <Button type="button" variant={confirmVariant} disabled={disabled} onClick={onConfirm}>
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}
