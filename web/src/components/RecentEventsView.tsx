import { useEffect, useId, useMemo, useState } from "react";
import { RefreshCw } from "lucide-react";

import {
  APIError,
  listAccessEvents,
  listAuthorizations,
  listNodes,
  listUsers,
  type AccessEvent,
  type Authorization,
  type Node,
  type User,
} from "../api";
import { FormError } from "./AuthScreen";
import { useI18n } from "../i18n";
import { PageHeader, StatusBadge } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "./ui/card";
import { Combobox } from "./ui/combobox";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const pageSize = 100;

export function RecentEventsView() {
  const { t, formatDateTime } = useI18n();
  const nodeFilterID = useId();
  const actionFilterID = useId();
  const nodeFilterLabelID = `${nodeFilterID}-label`;
  const actionFilterLabelID = `${actionFilterID}-label`;
  const [items, setItems] = useState<AccessEvent[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [authorizations, setAuthorizations] = useState<Authorization[]>([]);
  const [nodeID, setNodeID] = useState("");
  const [action, setAction] = useState<"" | AccessEvent["action"]>("");
  const [loading, setLoading] = useState(true);
  const [loadingMore, setLoadingMore] = useState(false);
  const [hasMore, setHasMore] = useState(false);
  const [refreshKey, setRefreshKey] = useState(0);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    Promise.all([listNodes(), listUsers(), listAuthorizations()]).then(([nodeValues, userValues, authorizationValues]) => {
      if (!active) return;
      setNodes(nodeValues);
      setUsers(userValues);
      setAuthorizations(authorizationValues);
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    });
    return () => { active = false; };
  }, []);

  useEffect(() => {
    let active = true;
    setLoading(true);
    setError(undefined);
    listAccessEvents({ nodeID: nodeID || undefined, limit: pageSize }).then((values) => {
      if (!active) return;
      setItems(values);
      setHasMore(values.length === pageSize);
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, [nodeID, refreshKey]);

  const nodeNames = useMemo(() => new Map(nodes.map((node) => [node.id, node.name])), [nodes]);
  const userNames = useMemo(() => new Map(users.map((user) => [user.id, user.display_name])), [users]);
  const authorizationUsers = useMemo(() => new Map(authorizations.map((value) => [value.id, value.user_id])), [authorizations]);
  const visibleItems = useMemo(() => action === "" ? items : items.filter((item) => item.action === action), [action, items]);

  async function loadMore() {
    const beforeID = items.at(-1)?.id;
    if (beforeID === undefined) return;
    setLoadingMore(true);
    setError(undefined);
    try {
      const values = await listAccessEvents({ nodeID: nodeID || undefined, beforeID, limit: pageSize });
      setItems((current) => [...current, ...values]);
      setHasMore(values.length === pageSize);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setLoadingMore(false);
    }
  }

  return (
    <section aria-labelledby="recent-events-title">
      <PageHeader
        id="recent-events-title"
        eyebrow={t("Telemetry")}
        title={t("Recent events")}
        description={t("Inspect standardized access events received from runtime plugins.")}
      />
      <Card className="min-w-0 h-fit">
        <CardHeader className="flex flex-col items-start justify-between space-y-0 gap-4 pb-4 sm:flex-row sm:items-center">
          <div className="min-w-0"><CardTitle>{t("Event list")}</CardTitle><CardDescription>{t("{count} loaded events", { count: visibleItems.length })}</CardDescription></div>
          <div className="flex w-full flex-col gap-3 sm:w-auto sm:flex-row sm:flex-wrap sm:items-center sm:justify-end">
            <label className="flex items-center gap-2 max-[520px]:w-full" htmlFor={nodeFilterID}>
              <span className="shrink-0 whitespace-nowrap text-xs font-semibold text-muted-foreground" id={nodeFilterLabelID}>{t("Node")}</span>
              <Combobox
                value={nodeID || "all"}
                onValueChange={(value) => setNodeID(value === "all" ? "" : value)}
                options={[{ value: "all", label: t("All nodes") }, ...nodes.map((node) => ({ value: node.id, label: node.name }))]}
                searchPlaceholder={t("Search options...")}
                emptyText={t("No matching options.")}
                className="h-9 min-w-40 max-[520px]:flex-1"
                id={nodeFilterID}
                aria-labelledby={nodeFilterLabelID}
              />
            </label>
            <label className="flex items-center gap-2 max-[520px]:w-full" htmlFor={actionFilterID}>
              <span className="shrink-0 whitespace-nowrap text-xs font-semibold text-muted-foreground" id={actionFilterLabelID}>{t("Action")}</span>
              <Combobox
                value={action || "all"}
                onValueChange={(value) => setAction(value === "all" ? "" : value as AccessEvent["action"])}
                options={[
                  { value: "all", label: t("All actions") },
                  { value: "accepted", label: t("Accepted") },
                  { value: "blocked", label: t("Blocked") },
                ]}
                searchPlaceholder={t("Search options...")}
                emptyText={t("No matching options.")}
                className="h-9 min-w-36 max-[520px]:flex-1"
                id={actionFilterID}
                aria-labelledby={actionFilterLabelID}
              />
            </label>
            <Tooltip><TooltipTrigger asChild><Button className="shrink-0" variant="outline" onClick={() => setRefreshKey((value) => value + 1)} aria-label={t("Refresh events")} type="button"><RefreshCw size={16} />{t("Refresh")}</Button></TooltipTrigger><TooltipContent>{t("Refresh events")}</TooltipContent></Tooltip>
          </div>
        </CardHeader>
        <CardContent className="space-y-4">
          {error ? <FormError message={t(error)} /> : null}
          <div className="rounded-lg border bg-card">
            <Table className="min-w-[1000px]">
              <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Time")}</TableHead><TableHead>{t("Node")}</TableHead><TableHead>{t("User")}</TableHead><TableHead>{t("Source")}</TableHead><TableHead>{t("Destination")}</TableHead><TableHead>{t("Protocol")}</TableHead><TableHead>{t("Action")}</TableHead></TableRow></TableHeader>
              <TableBody>
                {!loading ? visibleItems.map((event) => {
                  const userID = authorizationUsers.get(event.authorization_id);
                  return (
                    <TableRow key={event.id}>
                      <TableCell className="whitespace-nowrap" title={t("Received {time}", { time: formatDateTime(event.received_at) })}>{formatDateTime(event.observed_at)}</TableCell>
                      <TableCell><span className="grid max-w-[190px] gap-0.5"><strong className="font-semibold">{nodeNames.get(event.node_id) ?? t("Unknown node")}</strong><small className="overflow-hidden text-ellipsis whitespace-nowrap text-xs text-muted-foreground" title={`${event.plugin_id} / ${event.service_id}`}>{event.plugin_id} / {event.service_id}</small></span></TableCell>
                      <TableCell>{userID ? userNames.get(userID) ?? t("Unknown user") : t("Unknown authorization")}</TableCell>
                      <TableCell className="whitespace-nowrap">{event.source_ip || t("Not reported")}</TableCell>
                      <TableCell className="whitespace-nowrap">{formatDestination(event, t)}</TableCell>
                      <TableCell>{[event.network, event.protocol].filter(Boolean).join(" / ") || t("Not reported")}</TableCell>
                      <TableCell><EventAction value={event.action} /></TableCell>
                    </TableRow>
                  );
                }) : null}
                {loading ? <TableRow><TableCell colSpan={7} className="h-24 text-center text-muted-foreground">{t("Loading...")}</TableCell></TableRow> : null}
                {!loading && visibleItems.length === 0 ? <TableRow><TableCell colSpan={7} className="h-24 text-center text-muted-foreground">{t("No recent access events.")}</TableCell></TableRow> : null}
              </TableBody>
            </Table>
          </div>
        </CardContent>
        {hasMore ? <CardFooter className="justify-center"><Button variant="outline" disabled={loadingMore} onClick={loadMore} type="button">{loadingMore ? t("Loading...") : t("Load older")}</Button></CardFooter> : null}
      </Card>
    </section>
  );
}

function EventAction({ value }: { value: AccessEvent["action"] }) {
  const { t } = useI18n();
  return <StatusBadge tone={value === "accepted" ? "success" : "warning"}>{value === "accepted" ? t("Accepted") : t("Blocked")}</StatusBadge>;
}

function formatDestination(event: AccessEvent, t: (message: string) => string): string {
  if (!event.destination) return t("Not reported");
  if (!event.destination_port) return event.destination;
  const host = event.destination.includes(":") && !event.destination.startsWith("[") ? `[${event.destination}]` : event.destination;
  return `${host}:${event.destination_port}`;
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
