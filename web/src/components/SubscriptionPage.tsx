import { useEffect, useState } from "react";
import { Download } from "lucide-react";

import { APIError, getSubscription, type SubscriptionInfo } from "../api";
import { LanguageSwitcher, useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { Button } from "./ui/button";

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
    return (
      <main className="relative flex min-h-screen flex-col items-center justify-center gap-3.5 p-6 text-center">
        <LanguageSwitcher className="absolute top-5 right-5" />
        <span className="flex size-11 items-center justify-center rounded-md bg-foreground text-[22px] font-bold text-background">R</span>
        <h1 className="m-0 text-2xl font-semibold">{t("Subscription unavailable")}</h1>
        <p className="m-0 max-w-lg text-sm text-muted-foreground">{t(error)}</p>
        <Button size="sm" onClick={() => window.location.reload()} type="button">{t("Retry")}</Button>
      </main>
    );
  }
  if (!subscription) {
    return <main className="flex min-h-screen flex-col items-center justify-center gap-3.5 p-6"><span className="flex size-11 items-center justify-center rounded-md bg-foreground text-[22px] font-bold text-background">R</span><span>Relayward</span></main>;
  }

  return (
    <main className="mx-auto min-h-screen w-full max-w-[820px] px-6 pb-12 max-[440px]:px-[18px] max-[440px]:pb-9">
      <header className="flex h-16 items-center gap-2.5"><span className="flex size-[30px] items-center justify-center rounded-md bg-foreground text-[15px] font-bold text-background">R</span><strong>Relayward</strong><LanguageSwitcher className="ml-auto" /></header>
      <section className="border-b border-border pt-13 pb-8.5 max-[440px]:pt-8.5">
        <p className="m-0 text-xs font-semibold text-muted-foreground">{subscription.user_name}</p>
        <h1 className="mt-1 mb-2.5 text-[34px] font-semibold max-[440px]:text-[30px]">{subscription.node_name}</h1>
        <p className="m-0 inline-flex items-center gap-2 text-sm font-semibold"><span className={cn("size-2.5 rounded-full", statusTone(subscription.status))} />{statusLabel(subscription.status, t)}</p>
        {subscription.node_address ? <p className="mt-3 mb-0 text-sm text-muted-foreground">{subscription.node_address}</p> : null}
      </section>
      <dl className="mt-7 grid grid-cols-2 gap-x-6 max-[440px]:grid-cols-1" aria-label={t("Subscription details")}>
        <Detail label={t("Traffic quota")} value={subscription.traffic_limit_bytes === null ? t("Unlimited") : formatBytes(subscription.traffic_limit_bytes)} />
        <Detail label={t("Traffic used")} value={subscription.traffic_used_bytes === null ? t("Unavailable") : formatBytes(subscription.traffic_used_bytes)} />
        <Detail label={t("Reset")} value={formatReset(subscription.reset, t)} />
        <Detail label={t("Expires")} value={subscription.expires_at ? formatDateTime(subscription.expires_at) : t("Never")} />
      </dl>
      {subscription.announcement ? <section className="mt-7 border-t border-border pt-6"><h2 className="mt-0 mb-3 text-[17px] font-semibold">{t("Announcement")}</h2><p className="m-0 whitespace-pre-wrap text-sm text-muted-foreground">{subscription.announcement}</p></section> : null}
      <section className="mt-7 border-t border-border pt-6" aria-labelledby="services-title">
        <h2 className="mt-0 mb-3 text-[17px] font-semibold" id="services-title">{t("Services")}</h2>
        {subscription.services.length === 0 ? <p className="m-0 text-sm text-muted-foreground">{t("No services available.")}</p> : null}
        {subscription.services.length > 0 ? (
          <ul className="m-0 list-none p-0">{subscription.services.map((service) => (
            <li className="flex items-baseline justify-between gap-4 border-b border-border py-3.5 max-[440px]:flex-col max-[440px]:items-start max-[440px]:gap-1" key={`${service.plugin_id}/${service.service_id}`}>
              <strong className="font-semibold">{service.display_name}</strong>
              <span className="text-right text-xs text-muted-foreground [overflow-wrap:anywhere] max-[440px]:text-left">{service.plugin_id} / {service.service_id}</span>
            </li>
          ))}</ul>
        ) : null}
      </section>
      {subscription.status === "active" && subscription.services.length > 0 ? (
        <section className="mt-7 border-t border-border pt-6" aria-labelledby="downloads-title">
          <h2 className="mt-0 mb-3 text-[17px] font-semibold" id="downloads-title">{t("Downloads")}</h2>
          <div className="flex flex-wrap gap-2.5 max-[440px]:grid">
            <DownloadLink href={downloadURL(token, "base64")} label="Base64" />
            <DownloadLink href={downloadURL(token, "mihomo.yaml")} label="Mihomo" />
            <DownloadLink href={downloadURL(token, "sing-box.json")} label="sing-box" />
          </div>
        </section>
      ) : null}
    </main>
  );
}

function Detail({ label, value }: { label: string; value: string }) {
  return <div className="flex min-h-[76px] flex-col gap-1.5 border-b border-border py-3.5"><dt className="text-xs text-muted-foreground">{label}</dt><dd className="m-0 font-semibold">{value}</dd></div>;
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

function statusTone(status: SubscriptionInfo["status"]): string {
  if (status === "active") return "bg-success";
  if (status === "disabled") return "bg-muted-foreground";
  return "bg-warning";
}

function DownloadLink({ href, label }: { href: string; label: string }) {
  return <Button variant="secondary" size="sm" asChild><a href={href} download><Download size={16} />{label}</a></Button>;
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
