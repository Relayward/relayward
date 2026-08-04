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
import { cn } from "../lib/utils";
import { Button } from "./ui/button";
import { Select, SelectContent, SelectItem, SelectTrigger, SelectValue } from "./ui/select";
import { Table, TableBody, TableCell, TableHead, TableHeader, TableRow } from "./ui/table";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

const pageSize = 100;

export function RecentEventsView() {
  const { t, formatDateTime } = useI18n();
  const nodeFilterID = useId();
  const [items, setItems] = useState<AccessEvent[]>([]);
  const [nodes, setNodes] = useState<Node[]>([]);
  const [users, setUsers] = useState<User[]>([]);
  const [authorizations, setAuthorizations] = useState<Authorization[]>([]);
  const [nodeID, setNodeID] = useState("");
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
      <div className="mb-6 flex items-end justify-between gap-4 max-[700px]:flex-col max-[700px]:items-start">
        <div><p className="m-0 text-xs font-semibold text-muted-foreground">{t("Telemetry")}</p><h1 className="mt-0.5 mb-0 text-[25px] font-semibold" id="recent-events-title">{t("Recent events")}</h1></div>
        <div className="flex items-center gap-2">
          <label className="flex items-center gap-2" htmlFor={nodeFilterID}>
            <span className="shrink-0 whitespace-nowrap text-xs font-semibold text-muted-foreground">{t("Node")}</span>
            <Select value={nodeID || "all"} onValueChange={(value) => setNodeID(value === "all" ? "" : value)}>
              <SelectTrigger className="h-9 min-w-40" id={nodeFilterID}><SelectValue /></SelectTrigger>
              <SelectContent>
                <SelectItem value="all">{t("All nodes")}</SelectItem>
                {nodes.map((node) => <SelectItem key={node.id} value={node.id}>{node.name}</SelectItem>)}
              </SelectContent>
            </Select>
          </label>
          <Tooltip>
            <TooltipTrigger asChild>
              <Button variant="ghost" size="icon" onClick={() => setRefreshKey((value) => value + 1)} aria-label={t("Refresh events")} type="button"><RefreshCw size={17} /></Button>
            </TooltipTrigger>
            <TooltipContent>{t("Refresh events")}</TooltipContent>
          </Tooltip>
        </div>
      </div>
      {error ? <div className="mb-3"><FormError message={t(error)} /></div> : null}
      <div className="overflow-hidden rounded-md border border-border bg-card">
        <Table className="min-w-[1000px]">
          <TableHeader><TableRow className="hover:bg-transparent"><TableHead>{t("Time")}</TableHead><TableHead>{t("Node")}</TableHead><TableHead>{t("User")}</TableHead><TableHead>{t("Source")}</TableHead><TableHead>{t("Destination")}</TableHead><TableHead>{t("Protocol")}</TableHead><TableHead>{t("Action")}</TableHead></TableRow></TableHeader>
          <TableBody>
            {!loading ? items.map((event) => {
              const userID = authorizationUsers.get(event.authorization_id);
              return (
                <TableRow key={event.id}>
                  <TableCell className="whitespace-nowrap" title={t("Received {time}", { time: formatDateTime(event.received_at) })}>{formatDateTime(event.observed_at)}</TableCell>
                  <TableCell><span className="grid max-w-[190px] gap-0.5"><strong className="font-semibold">{nodeNames.get(event.node_id) ?? t("Unknown node")}</strong><small className="overflow-hidden text-ellipsis whitespace-nowrap text-[11px] text-muted-foreground" title={`${event.plugin_id} / ${event.service_id}`}>{event.plugin_id} / {event.service_id}</small></span></TableCell>
                  <TableCell>{userID ? userNames.get(userID) ?? t("Unknown user") : t("Unknown authorization")}</TableCell>
                  <TableCell className="whitespace-nowrap">{event.source_ip || t("Not reported")}</TableCell>
                  <TableCell className="whitespace-nowrap">{formatDestination(event, t)}</TableCell>
                  <TableCell>{[event.network, event.protocol].filter(Boolean).join(" / ") || t("Not reported")}</TableCell>
                  <TableCell><EventAction value={event.action} /></TableCell>
                </TableRow>
              );
            }) : null}
          </TableBody>
        </Table>
        {loading ? <div className="flex h-24 items-center justify-center px-4 text-center text-[13px] text-muted-foreground">{t("Loading...")}</div> : null}
        {!loading && items.length === 0 ? <div className="flex h-24 items-center justify-center px-4 text-center text-[13px] text-muted-foreground">{t("No recent access events.")}</div> : null}
      </div>
      {hasMore ? <div className="flex justify-center pt-4"><Button variant="secondary" disabled={loadingMore} onClick={loadMore} type="button">{loadingMore ? t("Loading...") : t("Load older")}</Button></div> : null}
    </section>
  );
}

function EventAction({ value }: { value: AccessEvent["action"] }) {
  const { t } = useI18n();
  return <span className="inline-flex items-center gap-1.5 whitespace-nowrap"><span className={cn("size-2 shrink-0 rounded-full", value === "accepted" ? "bg-success" : "bg-warning")} />{value === "accepted" ? t("Accepted") : t("Blocked")}</span>;
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
