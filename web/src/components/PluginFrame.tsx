import { useEffect, useRef, useState } from "react";
import { ChevronLeft } from "lucide-react";

import { invokePluginUI, type PluginInstallation } from "../api";
import { bridgeFailure, bridgeSuccess, isRecord, parsePluginUIRequest } from "../pluginBridge";

export type PluginNavigationTarget = "plugins" | "nodes" | "users" | "authorizations" | "audit";

interface PluginFrameProps {
  plugin: PluginInstallation;
  onClose: () => void;
  onNavigate: (target: PluginNavigationTarget) => void;
}

export function PluginFrame({ plugin, onClose, onNavigate }: PluginFrameProps) {
  const frame = useRef<HTMLIFrameElement>(null);
  const [loading, setLoading] = useState(true);
  const [failed, setFailed] = useState(false);

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
            result = { plugin_id: plugin.plugin_id, theme: "light" };
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
    <section className="plugin-frame-view" aria-labelledby="plugin-frame-title">
      <div className="section-heading plugin-frame-heading">
        <div>
          <button className="quiet-button button-with-icon plugin-back-button" onClick={onClose} type="button">
            <ChevronLeft size={17} />Plugins
          </button>
          <h1 id="plugin-frame-title">{plugin.manifest.name}</h1>
        </div>
        <span className="version-label">v{plugin.active_version}</span>
      </div>
      <div className="plugin-frame-shell">
        {loading && !failed ? <div className="plugin-frame-state">Loading...</div> : null}
        {failed ? <div className="plugin-frame-state plugin-frame-state--error">Plugin page could not be loaded.</div> : null}
        <iframe
          ref={frame}
          className={loading || failed ? "plugin-frame plugin-frame--hidden" : "plugin-frame"}
          title={`${plugin.manifest.name} plugin`}
          src={`/api/v1/plugins/${encodeURIComponent(plugin.plugin_id)}/ui/index.html`}
          sandbox="allow-scripts"
          referrerPolicy="no-referrer"
          onLoad={() => setLoading(false)}
          onError={() => { setLoading(false); setFailed(true); }}
        />
      </div>
    </section>
  );
}

function isNavigationTarget(value: unknown): value is PluginNavigationTarget {
  return value === "plugins" || value === "nodes" || value === "users" || value === "authorizations" || value === "audit";
}
