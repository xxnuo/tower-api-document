import { createContext, useContext, useRef, useState } from "react";
import { Button } from "@/components/ui/button";
import { Dialog, DialogContent, DialogDescription, DialogFooter, DialogHeader, DialogTitle } from "@/components/ui/dialog";
import { Textarea } from "@/components/ui/textarea";

const ClipboardContext = createContext(null);

export function useClipboard() {
  return useContext(ClipboardContext);
}

export function ClipboardProvider({ children }) {
  const [manualText, setManualText] = useState(null);
  const field = useRef(null);

  async function copy(text) {
    if (window.isSecureContext && navigator.clipboard?.writeText) {
      try {
        await navigator.clipboard.writeText(text);
        return true;
      } catch {
        // Continue with manual copying when clipboard access is denied.
      }
    }
    setManualText(text);
    return false;
  }

  return (
    <ClipboardContext.Provider value={copy}>
      {children}
      <Dialog open={manualText !== null} onOpenChange={(open) => { if (!open) setManualText(null); }}>
        <DialogContent initialFocus={field} className="sm:max-w-xl">
          <DialogHeader>
            <DialogTitle>手动复制</DialogTitle>
            <DialogDescription>
              浏览器限制了自动复制。内容已全选，请按 Ctrl+C 或 ⌘+C 复制。手机上可长按文字，选择「复制」。
            </DialogDescription>
          </DialogHeader>
          <Textarea
            ref={field}
            aria-label="待复制的内容"
            readOnly
            value={manualText ?? ""}
            onFocus={(event) => event.currentTarget.select()}
            className="h-48 max-h-[40dvh] resize-none overflow-auto font-mono text-xs"
          />
          <DialogFooter>
            <Button type="button" variant="outline" onClick={() => setManualText(null)}>关闭</Button>
          </DialogFooter>
        </DialogContent>
      </Dialog>
    </ClipboardContext.Provider>
  );
}
