import { useEffect, useMemo, useState } from "react";
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

const pageSize = 100;

export function RecentEventsView() {
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
      <div className="section-heading event-section-heading">
        <div><p className="eyebrow">Telemetry</p><h1 id="recent-events-title">Recent events</h1></div>
        <div className="event-controls">
          <label><span>Node</span><select value={nodeID} onChange={(event) => setNodeID(event.target.value)}><option value="">All nodes</option>{nodes.map((node) => <option key={node.id} value={node.id}>{node.name}</option>)}</select></label>
          <button className="icon-button" onClick={() => setRefreshKey((value) => value + 1)} aria-label="Refresh events" title="Refresh events" type="button"><RefreshCw size={17} /></button>
        </div>
      </div>
      <FormError message={error} />
      <div className="table-frame">
        <table className="resource-table event-table">
          <thead><tr><th>Time</th><th>Node</th><th>User</th><th>Source</th><th>Destination</th><th>Protocol</th><th>Action</th></tr></thead>
          <tbody>
            {loading ? <tr><td colSpan={7} className="empty-cell">Loading...</td></tr> : null}
            {!loading && items.length === 0 ? <tr><td colSpan={7} className="empty-cell">No recent access events.</td></tr> : null}
            {!loading ? items.map((event) => {
              const userID = authorizationUsers.get(event.authorization_id);
              return (
                <tr key={event.id}>
                  <td className="event-time" title={`Received ${new Date(event.received_at).toLocaleString()}`}>{new Date(event.observed_at).toLocaleString()}</td>
                  <td><strong>{nodeNames.get(event.node_id) ?? "Unknown node"}</strong><small className="event-detail">{event.plugin_id} / {event.service_id}</small></td>
                  <td>{userID ? userNames.get(userID) ?? "Unknown user" : "Unknown authorization"}</td>
                  <td className="event-address">{event.source_ip || "Not reported"}</td>
                  <td className="event-address">{formatDestination(event)}</td>
                  <td>{[event.network, event.protocol].filter(Boolean).join(" / ") || "Not reported"}</td>
                  <td><EventAction value={event.action} /></td>
                </tr>
              );
            }) : null}
          </tbody>
        </table>
      </div>
      {hasMore ? <div className="list-footer"><button className="secondary-button" disabled={loadingMore} onClick={loadMore} type="button">{loadingMore ? "Loading..." : "Load older"}</button></div> : null}
    </section>
  );
}

function EventAction({ value }: { value: AccessEvent["action"] }) {
  return <span className="inline-status"><span className={`status-dot status-dot--${value === "accepted" ? "ok" : "warning"}`} />{value === "accepted" ? "Accepted" : "Blocked"}</span>;
}

function formatDestination(event: AccessEvent): string {
  if (!event.destination) return "Not reported";
  if (!event.destination_port) return event.destination;
  const host = event.destination.includes(":") && !event.destination.startsWith("[") ? `[${event.destination}]` : event.destination;
  return `${host}:${event.destination_port}`;
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
