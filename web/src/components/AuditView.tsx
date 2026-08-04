import { useEffect, useState } from "react";

import { APIError, listAudit, type AuditEntry } from "../api";
import { useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { FormError } from "./AuthScreen";
import { Button } from "./ui/button";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";

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
      <div className="mb-6"><p className="m-0 text-xs font-semibold text-muted-foreground">{t("System")}</p><h1 className="mt-0.5 mb-0 text-[25px] font-semibold" id="audit-title">{t("Audit")}</h1></div>
      {error ? <div className="mb-3"><FormError message={t(error)} /></div> : null}
      <div className="overflow-hidden rounded-md border border-border bg-card">
        <Table className="min-w-[760px]">
          <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Time")}</TableHead><TableHead>{t("Action")}</TableHead><TableHead>{t("Target")}</TableHead><TableHead>{t("Actor")}</TableHead><TableHead>{t("Outcome")}</TableHead></TableRow></TableHeader>
          <TableBody>
            {items.map((entry) => (
              <TableRow key={entry.id}>
                <TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(entry.occurred_at)}</TableCell>
                <TableCell><code className="rounded-sm bg-muted px-1.5 py-1 text-xs">{entry.action}</code></TableCell>
                <TableCell className="text-muted-foreground">{entry.target_type}{entry.target_id ? ` / ${shortID(entry.target_id)}` : ""}</TableCell>
                <TableCell className="text-muted-foreground">{entry.actor_type}</TableCell>
                <TableCell><AuditOutcome value={entry.outcome} /></TableCell>
              </TableRow>
            ))}
          </TableBody>
        </Table>
        {loading ? <div className="flex h-24 items-center justify-center px-4 text-center text-[13px] text-muted-foreground">{t("Loading...")}</div> : null}
        {!loading && items.length === 0 ? <div className="flex h-24 items-center justify-center px-4 text-center text-[13px] text-muted-foreground">{t("No audit entries.")}</div> : null}
      </div>
      {hasMore ? <div className="flex justify-center pt-4"><Button variant="secondary" disabled={loadingOlder} onClick={loadOlder} type="button">{loadingOlder ? t("Loading...") : t("Load older")}</Button></div> : null}
    </section>
  );
}

function AuditOutcome({ value }: { value: string }) {
  const { t } = useI18n();
  return <span className="inline-flex items-center gap-1.5 whitespace-nowrap"><span className={cn("size-2 shrink-0 rounded-full", value === "success" ? "bg-success" : "bg-warning")} />{t(titleCase(value))}</span>;
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
