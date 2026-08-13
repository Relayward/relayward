import { useEffect, useId, useMemo, useState } from "react";
import { RefreshCw } from "lucide-react";

import { APIError, listAudit, type AuditEntry } from "../api";
import { auditActionLabel, auditActorLabel, auditOutcomeLabel, auditTargetTypeLabel, shortAuditID } from "../auditPresentation";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { PageHeader, StatusBadge } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "./ui/card";
import { Combobox } from "./ui/combobox";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";

const pageSize = 100;

export function AuditView() {
  const { t, formatDateTime } = useI18n();
  const outcomeFilterID = useId();
  const outcomeFilterLabelID = `${outcomeFilterID}-label`;
  const [items, setItems] = useState<AuditEntry[]>([]);
  const [outcome, setOutcome] = useState("");
  const [refreshKey, setRefreshKey] = useState(0);
  const [loading, setLoading] = useState(true);
  const [loadingOlder, setLoadingOlder] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(undefined);
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
  }, [refreshKey]);

  const outcomes = useMemo(() => [...new Set(items.map((item) => item.outcome))].sort(), [items]);
  const visibleItems = useMemo(() => outcome === "" ? items : items.filter((item) => item.outcome === outcome), [items, outcome]);

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
      <PageHeader id="audit-title" eyebrow={t("System")} title={t("Audit")} description={t("Review administrator and system operations recorded by the control plane.")} />
      <Card className="min-w-0 h-fit">
        <CardHeader className="flex flex-col items-start justify-between space-y-0 gap-4 pb-4 sm:flex-row sm:items-center">
          <div className="min-w-0"><CardTitle>{t("Audit log")}</CardTitle><CardDescription>{t("{count} loaded entries", { count: visibleItems.length })}</CardDescription></div>
          <div className="flex w-full flex-col gap-3 sm:w-auto sm:flex-row sm:items-center">
            <label className="flex items-center gap-2 max-[520px]:w-full" htmlFor={outcomeFilterID}>
              <span className="shrink-0 whitespace-nowrap text-xs font-semibold text-muted-foreground" id={outcomeFilterLabelID}>{t("Outcome")}</span>
              <Combobox
                value={outcome || "all"}
                onValueChange={(value) => setOutcome(value === "all" ? "" : value)}
                options={[{ value: "all", label: t("All outcomes") }, ...outcomes.map((value) => ({ value, label: auditOutcomeLabel(value, t) }))]}
                searchPlaceholder={t("Search options...")}
                emptyText={t("No matching options.")}
                className="h-9 min-w-40 max-[520px]:flex-1"
                id={outcomeFilterID}
                aria-labelledby={outcomeFilterLabelID}
              />
            </label>
            <Button className="shrink-0" variant="outline" onClick={() => setRefreshKey((value) => value + 1)} type="button"><RefreshCw size={16} />{t("Refresh")}</Button>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {error ? <FormError message={t(error)} /> : null}
          <div className="rounded-lg border bg-card">
            <Table className="min-w-[760px]">
              <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Time")}</TableHead><TableHead>{t("Action")}</TableHead><TableHead>{t("Target")}</TableHead><TableHead>{t("Actor")}</TableHead><TableHead>{t("Outcome")}</TableHead></TableRow></TableHeader>
              <TableBody>
                {!loading ? visibleItems.map((entry) => (
                  <TableRow key={entry.id}>
                    <TableCell className="whitespace-nowrap text-muted-foreground">{formatDateTime(entry.occurred_at)}</TableCell>
                    <TableCell><span title={entry.action}>{auditActionLabel(entry.action, t)}</span></TableCell>
                    <TableCell className="text-muted-foreground" title={entry.target_type}>{auditTargetTypeLabel(entry.target_type, t)}{entry.target_id ? ` / ${shortAuditID(entry.target_id)}` : ""}</TableCell>
                    <TableCell className="text-muted-foreground" title={entry.actor_type}>{auditActorLabel(entry.actor_type, t)}</TableCell>
                    <TableCell><AuditOutcome value={entry.outcome} /></TableCell>
                  </TableRow>
                )) : null}
                {loading ? <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{t("Loading...")}</TableCell></TableRow> : null}
                {!loading && visibleItems.length === 0 ? <TableRow><TableCell colSpan={5} className="h-24 text-center text-muted-foreground">{t("No audit entries.")}</TableCell></TableRow> : null}
              </TableBody>
            </Table>
          </div>
        </CardContent>
        {hasMore ? <CardFooter className="justify-center"><Button variant="outline" disabled={loadingOlder} onClick={loadOlder} type="button">{loadingOlder ? t("Loading...") : t("Load older")}</Button></CardFooter> : null}
      </Card>
    </section>
  );
}

function AuditOutcome({ value }: { value: string }) {
  const { t } = useI18n();
  return <StatusBadge tone={value === "success" ? "success" : "danger"}>{auditOutcomeLabel(value, t)}</StatusBadge>;
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
