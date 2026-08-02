import { useEffect, useState } from "react";

import { APIError, getSubscription, type SubscriptionInfo } from "../api";

const gibibyte = 1024 ** 3;
const weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];

export function SubscriptionPage({ token }: { token: string }) {
  const [subscription, setSubscription] = useState<SubscriptionInfo>();
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    getSubscription(token).then((value) => {
      if (active) setSubscription(value);
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    });
    return () => { active = false; };
  }, [token]);

  if (error) {
    return <main className="subscription-error"><span className="brand-mark">R</span><h1>Subscription unavailable</h1><p>{error}</p></main>;
  }
  if (!subscription) {
    return <main className="subscription-loading"><span className="brand-mark">R</span><span>Relayward</span></main>;
  }

  return (
    <main className="subscription-page">
      <header className="subscription-header"><span className="brand-mark brand-mark--small">R</span><strong>Relayward</strong></header>
      <section className="subscription-summary">
        <p className="eyebrow">{subscription.user_name}</p>
        <h1>{subscription.node_name}</h1>
        <p className={`subscription-state subscription-state--${subscription.status}`}>{statusLabel(subscription.status)}</p>
        {subscription.node_address ? <p className="subscription-address">{subscription.node_address}</p> : null}
      </section>
      <section className="subscription-details" aria-label="Subscription details">
        <Detail label="Traffic quota" value={subscription.traffic_limit_bytes === null ? "Unlimited" : formatBytes(subscription.traffic_limit_bytes)} />
        <Detail label="Traffic used" value={subscription.traffic_used_bytes === null ? "Unavailable" : formatBytes(subscription.traffic_used_bytes)} />
        <Detail label="Reset" value={formatReset(subscription.reset)} />
        <Detail label="Expires" value={subscription.expires_at ? new Date(subscription.expires_at).toLocaleString() : "Never"} />
      </section>
      {subscription.announcement ? <section className="subscription-announcement"><h2>Announcement</h2><p>{subscription.announcement}</p></section> : null}
      <section className="subscription-services" aria-labelledby="services-title">
        <h2 id="services-title">Services</h2>
        {subscription.services.length === 0 ? <p className="empty-service">No services available.</p> : null}
      </section>
    </main>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

function statusLabel(status: SubscriptionInfo["status"]): string {
  switch (status) {
    case "active": return "Active";
    case "disabled": return "Disabled";
    case "expired": return "Expired";
    case "node_disabled": return "Node disabled";
  }
}

function formatBytes(value: number): string {
  const gib = value / gibibyte;
  return `${Number.isInteger(gib) ? gib : gib.toFixed(2)} GiB`;
}

function formatReset(reset: SubscriptionInfo["reset"]): string {
  switch (reset.kind) {
    case "never": return "Never";
    case "daily": return `Daily / ${reset.timezone}`;
    case "weekly": return `${weekdays[(reset.value ?? 1) - 1] ?? "Weekly"} / ${reset.timezone}`;
    case "monthly": return `Day ${reset.value} / ${reset.timezone}`;
    case "interval_days": return `Every ${reset.value} days / ${reset.timezone}`;
  }
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The subscription could not be loaded.";
}
