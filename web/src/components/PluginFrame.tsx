import { useEffect, useRef, useState } from "react";
import { createPluginUISession, invokePluginUI, type PluginInstallation } from "../api";
import { useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { bridgeFailure, bridgeSuccess, isRecord, parsePluginUIRequest, PLUGIN_IFRAME_SANDBOX } from "../pluginBridge";

export type PluginNavigationTarget = "plugins" | "nodes" | "users" | "authorizations" | "audit";

interface PluginFrameProps {
  plugin: PluginInstallation;
  title: string;
  onNavigate: (target: PluginNavigationTarget) => void;
  nodeID?: string;
  embedded?: boolean;
}

export function PluginFrame({ plugin, title, onNavigate, nodeID, embedded = false }: PluginFrameProps) {
  const { locale, t } = useI18n();
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
  }, [locale, nodeID, plugin.plugin_id]);

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
              locale,
              ...(nodeID === undefined ? {} : { scope: { kind: "node", node_id: nodeID } }),
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
  }, [locale, nodeID, onNavigate, plugin.plugin_id]);

  return (
    <section aria-label={embedded ? title : undefined} aria-labelledby={embedded ? undefined : "plugin-frame-title"}>
      {!embedded ? <div className="mb-2 flex items-center justify-between gap-4 px-4 lg:px-6">
        <div>
          <h1 className="mb-0 text-2xl font-bold tracking-tight" id="plugin-frame-title">{title}</h1>
          <p className="mt-1 text-sm text-muted-foreground">{plugin.manifest.name}</p>
        </div>
        <span className="shrink-0 text-xs font-semibold text-muted-foreground">v{plugin.active_version}</span>
      </div> : null}
      <div className="relative h-[min(75vh,800px)] min-h-[520px] overflow-hidden max-[700px]:h-[calc(100vh-220px)] max-[700px]:min-h-[460px]">
        {loading && !failed ? <div className="absolute inset-0 flex items-center justify-center text-sm text-muted-foreground">{t("Loading...")}</div> : null}
        {failed ? <div className="absolute inset-0 flex items-center justify-center px-4 text-center text-sm text-destructive">{t("Plugin page could not be loaded.")}</div> : null}
        {source !== undefined ? (
          <iframe
            ref={frame}
            className={cn("block size-full border-0", (loading || failed) && "invisible")}
            title={t("{name} plugin", { name: plugin.manifest.name })}
            src={source}
            sandbox={PLUGIN_IFRAME_SANDBOX}
            referrerPolicy="no-referrer"
            onLoad={() => setLoading(false)}
            onError={() => { setLoading(false); setFailed(true); }}
          />
        ) : null}
      </div>
    </section>
  );
}

function isNavigationTarget(value: unknown): value is PluginNavigationTarget {
  return value === "plugins" || value === "nodes" || value === "users" || value === "authorizations" || value === "audit";
}
