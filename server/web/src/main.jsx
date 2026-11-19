import { StrictMode } from "react";
import { createRoot } from "react-dom/client";
import "./index.css";
import App from "./App.jsx";
import { ClipboardProvider } from "./components/clipboard.jsx";

createRoot(document.getElementById("root")).render(
  <StrictMode>
    {import.meta.env.DEV && (
      <div className="pointer-events-none fixed top-0 right-0 z-50 size-20 select-none bg-amber-400 font-sans text-base leading-none font-bold text-amber-950 [clip-path:polygon(0_0,100%_0,100%_100%)]">
        <span className="absolute top-4 right-2 rotate-45">Dev</span>
      </div>
    )}
    <ClipboardProvider><App /></ClipboardProvider>
  </StrictMode>
);
