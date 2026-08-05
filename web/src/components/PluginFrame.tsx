import { useEffect, useRef, useState } from "react";
import { ChevronLeft } from "lucide-react";

import { createPluginUISession, invokePluginUI, type PluginInstallation } from "../api";
import { useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { bridgeFailure, bridgeSuccess, isRecord, parsePluginUIRequest } from "../pluginBridge";
import { Button } from "./ui/button";
import { Card } from "./ui/card";

export type PluginNavigationTarget = "plugins" | "nodes" | "users" | "authorizations" | "audit";

interface PluginFrameProps {
  plugin: PluginInstallation;
  onClose: () => void;
  onNavigate: (target: PluginNavigationTarget) => void;
}

export function PluginFrame({ plugin, onClose, onNavigate }: PluginFrameProps) {
  const { t } = useI18n();
  const frame = useRef<HTMLIFrameElement>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);
  const [source, setSource] = useState<string>();

  useEffect(() => {
    let active = true;
    setLoading(true);
    setFailed(false);
    setSource(undefined);
    createPluginUISession(plugin.plugin_id).then((url) => {
      if (active) setSource(url);
    }, () => {
      if (active) {
        setLoading(false);
        setFailed(true);
      }
    });
    return () => { active = false; };
  }, [plugin.plugin_id]);

  useEffect(() => {
    async function receive(event: MessageEvent<unknown>) {
      const target = frame.current?.contentWindow;
      if (target == null || event.source !== target) return;
      const request = parsePluginUIRequest(event.data);
      if (request === undefined) return;
      try {
        let result: unknown;
        switch (request.method) {
          case "context":
            result = {
              plugin_id: plugin.plugin_id,
              theme: document.documentElement.classList.contains("dark") ? "dark" : "light",
            };
            break;
          case "rpc": {
            if (!isRecord(request.payload) || typeof request.payload.method !== "string" || !isRecord(request.payload.parameters)) {
              throw new Error("Invalid plugin RPC request");
            }
            result = await invokePluginUI(plugin.plugin_id, request.payload.method, request.payload.parameters);
            break;
          }
          case "navigate": {
            if (!isRecord(request.payload) || !isNavigationTarget(request.payload.target)) {
              throw new Error("Invalid plugin navigation request");
            }
            onNavigate(request.payload.target);
            result = null;
            break;
          }
          case "confirm": {
            if (!isRecord(request.payload) || typeof request.payload.title !== "string" ||
                typeof request.payload.message !== "string" || request.payload.title.length > 120 || request.payload.message.length > 1000) {
              throw new Error("Invalid plugin confirmation request");
            }
            result = window.confirm(`${request.payload.title}\n\n${request.payload.message}`);
            break;
          }
        }
        if (frame.current?.contentWindow === target) target.postMessage(bridgeSuccess(request.id, result), "*");
      } catch (cause) {
        if (frame.current?.contentWindow === target) target.postMessage(bridgeFailure(request.id, cause), "*");
      }
    }
    window.addEventListener("message", receive);
    return () => window.removeEventListener("message", receive);
  }, [onNavigate, plugin.plugin_id]);

  return (
    <section aria-labelledby="plugin-frame-title">
      <div className="mb-6 flex items-center justify-between gap-4">
        <div>
          <Button className="-ml-2 h-8 px-2" variant="ghost" size="sm" onClick={onClose} type="button">
            <ChevronLeft size={17} />{t("Plugins")}
          </Button>
          <h1 className="mt-1.5 mb-0 text-2xl font-bold tracking-tight" id="plugin-frame-title">{plugin.manifest.name}</h1>
        </div>
        <span className="shrink-0 text-xs font-semibold text-muted-foreground">v{plugin.active_version}</span>
      </div>
      <Card className="relative h-[min(75vh,800px)] min-h-[520px] overflow-hidden py-0 max-[700px]:h-[calc(100vh-220px)] max-[700px]:min-h-[460px]">
        {loading && !failed ? <div className="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">{t("Loading...")}</div> : null}
        {failed ? <div className="absolute inset-0 flex items-center justify-center px-4 text-center text-sm text-destructive">{t("Plugin page could not be loaded.")}</div> : null}
        {source !== undefined ? (
          <iframe
            ref={frame}
            className={cn("block size-full border-0", (loading || failed) && "invisible")}
            title={t("{name} plugin", { name: plugin.manifest.name })}
            src={source}
            sandbox="allow-scripts"
            referrerPolicy="no-referrer"
            onLoad={() => setLoading(false)}
            onError={() => { setLoading(false); setFailed(true); }}
          />
        ) : null}
      </Card>
    </section>
  );
}

function isNavigationTarget(value: unknown): value is PluginNavigationTarget {
  return value === "plugins" || value === "nodes" || value === "users" || value === "authorizations" || value === "audit";
}
