import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App.jsx";
import { ClipboardProvider } from "./components/clipboard.jsx";

createRoot(document.getElementById("root")).render(
  <StrictMode>
    {import.meta.env.DEV && (
      <div className="pointer-events-none fixed top-0 left-0 z-50 size-14 select-none bg-amber-400 font-sans text-xs leading-none font-bold text-amber-950 [clip-path:polygon(0_0,100%_0,0_100%)]">
        <span className="absolute top-3 left-1.5 -rotate-45">Dev</span>
      </div>
    )}
    <ClipboardProvider><App /></ClipboardProvider>
  </StrictMode>
);
