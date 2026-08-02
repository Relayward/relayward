import { useEffect, useState } from "react";

import { productName } from "./config";
import { loadSystemInfo, type SystemInfo } from "./system";

type LoadState =
  | { phase: "loading" }
  | { phase: "ready"; info: SystemInfo }
  | { phase: "error" };

export function App() {
  const [state, setState] = useState<LoadState>({ phase: "loading" });

  useEffect(() => {
    const controller = new AbortController();
    loadSystemInfo(controller.signal).then(
      (info) => setState({ phase: "ready", info }),
      () => {
        if (!controller.signal.aborted) {
          setState({ phase: "error" });
        }
      },
    );
    return () => controller.abort();
  }, []);

  const available = state.phase === "ready";

  return (
    <div className="app-shell">
      <header className="topbar">
        <strong>{productName}</strong>
        <span className="environment">Control plane</span>
      </header>
      <main>
        <h1>System status</h1>
        <dl className="status-list">
          <div>
            <dt>Control plane</dt>
            <dd>
              <span className={`status-dot status-dot--${state.phase}`} />
              {state.phase === "loading" ? "Checking" : available ? "Available" : "Unavailable"}
            </dd>
          </div>
          <div>
            <dt>Version</dt>
            <dd>{available ? state.info.version : "-"}</dd>
          </div>
        </dl>
      </main>
    </div>
  );
}
