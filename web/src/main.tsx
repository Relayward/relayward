import { setNonce } from "get-nonce";
import { StrictMode } from "react";
import { createRoot } from "react-dom/client";

import { App } from "./App";
import { TooltipProvider } from "./components/ui/tooltip";
import { I18nProvider } from "./i18n";
import "./tailwind.css";

const styleNonce = document.querySelector<HTMLMetaElement>('meta[name="relayward-style-nonce"]')?.content;
if (styleNonce) setNonce(styleNonce);

const storedTheme = window.localStorage.getItem("relayward.theme");
const darkTheme = storedTheme === "dark" || (storedTheme === null && window.matchMedia("(prefers-color-scheme: dark)").matches);
document.documentElement.classList.toggle("dark", darkTheme);

const root = document.getElementById("root");

if (root === null) {
  throw new Error("root element is missing");
}

createRoot(root).render(
  <StrictMode>
    <I18nProvider>
      <TooltipProvider delayDuration={350}>
        <App />
      </TooltipProvider>
    </I18nProvider>
  </StrictMode>,
);
