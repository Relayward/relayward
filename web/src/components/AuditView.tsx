import { useEffect, useState } from "react";

import { APIError, listAudit, type AuditEntry } from "../api";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";

const pageSize = 100;

export function AuditView() {
  const { t, formatDateTime } = useI18n();
  const [items, setItems] = useState<AuditEntry[]>([]);
  const [loading, setLoading] = useState(true);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    listAudit(undefined, pageSize).then((values) => {
      if (active) {
        setItems(values);
        setHasMore(values.length === pageSize);
      }
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, []);

  async function loadOlder() {
    const oldest = items.at(-1);
    if (!oldest) return;
    setLoadingOlder(true);
    setError(undefined);
    try {
      const older = await listAudit(oldest.id, pageSize);
      setItems((current) => [...current, ...older]);
      setHasMore(older.length === pageSize);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoadingOlder(false);
    }
  }

  return (
    <section aria-labelledby="audit-title">
      <div className="section-heading"><div><p className="eyebrow">{t("System")}</p><h1 id="audit-title">{t("Audit")}</h1></div></div>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <div className="table-frame">
        <table className="resource-table audit-table">
          <thead><tr><th>{t("Time")}</th><th>{t("Action")}</th><th>{t("Target")}</th><th>{t("Actor")}</th><th>{t("Outcome")}</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={5} className="empty-cell">{t("Loading...")}</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={5} className="empty-cell">{t("No audit entries.")}</td></tr> : null}
            {items.map((entry) => (
              <tr key={entry.id}>
                <td className="secondary-cell audit-time">{formatDateTime(entry.occurred_at)}</td>
                <td><code>{entry.action}</code></td>
                <td className="secondary-cell">{entry.target_type}{entry.target_id ? ` / ${shortID(entry.target_id)}` : ""}</td>
                <td className="secondary-cell">{entry.actor_type}</td>
                <td><span className="inline-status"><span className={`status-dot status-dot--${entry.outcome === "success" ? "ok" : "warning"}`} />{t(titleCase(entry.outcome))}</span></td>
              </tr>
            ))}
          </tbody>
        </table>
      </div>
      {hasMore ? <div className="list-footer"><button className="secondary-button" disabled={loadingOlder} onClick={loadOlder}>{loadingOlder ? t("Loading...") : t("Load older")}</button></div> : null}
    </section>
  );
}

function shortID(value: string): string {
  return value.length > 12 ? `${value.slice(0, 8)}...` : value;
}

function titleCase(value: string): string {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
