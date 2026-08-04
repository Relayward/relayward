import { useEffect, useState } from "react";
import { Download } from "lucide-react";

import { APIError, getSubscription, type SubscriptionInfo } from "../api";
import { LanguageSwitcher, useI18n } from "../i18n";

const gibibyte = 1024 ** 3;
const weekdays = ["Monday", "Tuesday", "Wednesday", "Thursday", "Friday", "Saturday", "Sunday"];

export function SubscriptionPage({ token }: { token: string }) {
  const { t, formatDateTime } = useI18n();
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
    return <main className="subscription-error"><LanguageSwitcher className="page-language-switcher" /><span className="brand-mark">R</span><h1>{t("Subscription unavailable")}</h1><p>{t(error)}</p></main>;
  }
  if (!subscription) {
    return <main className="subscription-loading"><span className="brand-mark">R</span><span>Relayward</span></main>;
  }

  return (
    <main className="subscription-page">
      <header className="subscription-header"><span className="brand-mark brand-mark--small">R</span><strong>Relayward</strong><LanguageSwitcher /></header>
      <section className="subscription-summary">
        <p className="eyebrow">{subscription.user_name}</p>
        <h1>{subscription.node_name}</h1>
        <p className={`subscription-state subscription-state--${subscription.status}`}>{statusLabel(subscription.status, t)}</p>
        {subscription.node_address ? <p className="subscription-address">{subscription.node_address}</p> : null}
      </section>
      <section className="subscription-details" aria-label={t("Subscription details")}>
        <Detail label={t("Traffic quota")} value={subscription.traffic_limit_bytes === null ? t("Unlimited") : formatBytes(subscription.traffic_limit_bytes)} />
        <Detail label={t("Traffic used")} value={subscription.traffic_used_bytes === null ? t("Unavailable") : formatBytes(subscription.traffic_used_bytes)} />
        <Detail label={t("Reset")} value={formatReset(subscription.reset, t)} />
        <Detail label={t("Expires")} value={subscription.expires_at ? formatDateTime(subscription.expires_at) : t("Never")} />
      </section>
      {subscription.announcement ? <section className="subscription-announcement"><h2>{t("Announcement")}</h2><p>{subscription.announcement}</p></section> : null}
      <section className="subscription-services" aria-labelledby="services-title">
        <h2 id="services-title">{t("Services")}</h2>
        {subscription.services.length === 0 ? <p className="empty-service">{t("No services available.")}</p> : null}
        {subscription.services.length > 0 ? (
          <ul>{subscription.services.map((service) => (
            <li key={`${service.plugin_id}/${service.service_id}`}>
              <strong>{service.display_name}</strong>
              <span>{service.plugin_id} / {service.service_id}</span>
            </li>
          ))}</ul>
        ) : null}
      </section>
      {subscription.status === "active" && subscription.services.length > 0 ? (
        <section className="subscription-downloads" aria-label={t("Downloads")}>
          <DownloadLink href={downloadURL(token, "base64")} label="Base64" />
          <DownloadLink href={downloadURL(token, "mihomo.yaml")} label="Mihomo" />
          <DownloadLink href={downloadURL(token, "sing-box.json")} label="sing-box" />
        </section>
      ) : null}
    </main>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return <div><span>{label}</span><strong>{value}</strong></div>;
}

function statusLabel(status: SubscriptionInfo["status"], t: (message: string) => string): string {
  switch (status) {
    case "active": return t("Active");
    case "disabled": return t("Disabled");
    case "expired": return t("Expired");
    case "node_disabled": return t("Node disabled");
    case "quota_exceeded": return t("Quota reached");
  }
}

function DownloadLink({ href, label }: { href: string; label: string }) {
  return <a className="secondary-button button-with-icon" href={href} download><Download size={16} />{label}</a>;
}

function downloadURL(token: string, format: string): string {
  return `/api/v1/subscriptions/${encodeURIComponent(token)}/${format}`;
}

function formatBytes(value: number): string {
  const gib = value / gibibyte;
  return `${Number.isInteger(gib) ? gib : gib.toFixed(2)} GiB`;
}

function formatReset(reset: SubscriptionInfo["reset"], t: (message: string, values?: Record<string, string | number>) => string): string {
  switch (reset.kind) {
    case "never": return t("Never");
    case "daily": return t("Daily / {timezone}", { timezone: reset.timezone });
    case "weekly": return t("{weekday} / {timezone}", { weekday: t(weekdays[(reset.value ?? 1) - 1] ?? "Monday"), timezone: reset.timezone });
    case "monthly": return t("Day {day} / {timezone}", { day: reset.value ?? 1, timezone: reset.timezone });
    case "interval_days": return t("Every {days} days / {timezone}", { days: reset.value ?? 1, timezone: reset.timezone });
  }
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The subscription could not be loaded.";
}
