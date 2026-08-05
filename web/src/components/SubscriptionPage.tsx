import { useEffect, useState } from "react";
import { CalendarClock, Download, Gauge, House, LifeBuoy, RefreshCw, RotateCcw, Server, WalletCards } from "lucide-react";

import { APIError, getSubscription, type SubscriptionInfo } from "../api";
import { LanguageSwitcher, useI18n } from "../i18n";
import { BrandMark, StatusBadge, SummaryBar, SummaryItem } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "./ui/card";

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
        <BrandMark />
        <h1 className="m-0 text-2xl font-semibold">{t("Subscription unavailable")}</h1>
        <p className="m-0 max-w-lg text-sm text-muted-foreground">{t(error)}</p>
        <Button size="sm" onClick={() => window.location.reload()} type="button">{t("Retry")}</Button>
      </main>
    );
  }
  if (!subscription) {
    return <main className="flex min-h-screen flex-col items-center justify-center gap-3.5 p-6"><BrandMark /><span className="text-xs text-muted-foreground">{t("Loading...")}</span></main>;
  }

  const trafficPercentage = subscription.traffic_limit_bytes !== null && subscription.traffic_limit_bytes > 0 && subscription.traffic_used_bytes !== null
    ? Math.min(subscription.traffic_used_bytes / subscription.traffic_limit_bytes * 100, 100)
    : 0;
  const status = statusLabel(subscription.status, t);
  const downloadsAvailable = subscription.status === "active" && subscription.services.length > 0;

  return (
    <div className="min-h-screen bg-background">
      <header className="border-b border-border bg-card">
        <div className="mx-auto flex h-16 w-full max-w-[1040px] items-center px-6 max-[440px]:px-4"><BrandMark /><LanguageSwitcher className="ml-auto" /></div>
      </header>
      <main className="mx-auto w-full max-w-[1040px] px-6 py-10 max-[640px]:px-4 max-[640px]:py-7">
        <section className="mb-7 flex items-start justify-between gap-5 max-[640px]:flex-col" aria-labelledby="subscription-title">
          <div className="min-w-0">
            <p className="m-0 text-xs font-semibold text-muted-foreground">{subscription.user_name} · {subscription.node_name}</p>
            <h1 className="mt-1 mb-2 text-2xl leading-tight font-bold" id="subscription-title">{subscription.title}</h1>
            {subscription.node_address ? <p className="m-0 break-all text-xs text-muted-foreground">{subscription.node_address}</p> : null}
          </div>
          <StatusBadge tone={statusTone(subscription.status)} className="shrink-0">{status}</StatusBadge>
        </section>

        <SummaryBar>
          <SummaryItem icon={<Gauge size={17} />} label={t("Traffic used")} value={subscription.traffic_used_bytes === null ? t("Unavailable") : formatBytes(subscription.traffic_used_bytes)} tone="primary" />
          <SummaryItem icon={<WalletCards size={17} />} label={t("Traffic quota")} value={subscription.traffic_limit_bytes === null ? t("Unlimited") : formatBytes(subscription.traffic_limit_bytes)} />
          <SummaryItem icon={<RotateCcw size={17} />} label={t("Reset")} value={formatReset(subscription.reset, t)} />
          <SummaryItem icon={<CalendarClock size={17} />} label={t("Expires")} value={subscription.expires_at ? formatDateTime(subscription.expires_at) : t("Never")} tone={subscription.status === "expired" ? "warning" : "default"} />
        </SummaryBar>

        {subscription.traffic_limit_bytes !== null ? <section className="mb-5" aria-label={t("Traffic usage")}><div className="mb-2 flex items-center justify-between gap-4 text-xs text-muted-foreground"><span>{t("Traffic usage")}</span><span>{Math.round(trafficPercentage)}%</span></div><div className="h-2 overflow-hidden rounded-full bg-muted"><div className={trafficPercentage >= 100 ? "h-full rounded-full bg-destructive" : trafficPercentage >= 80 ? "h-full rounded-full bg-warning" : "h-full rounded-full bg-primary"} style={{ width: `${trafficPercentage}%` }} /></div></section> : null}

        {subscription.refresh_hours > 0 || subscription.support_url || subscription.profile_url ? (
          <section className="mb-5 flex flex-wrap items-center gap-x-6 gap-y-3 border-y border-border py-3 text-sm text-muted-foreground" aria-label={t("Subscription profile")}>
            {subscription.refresh_hours > 0 ? <span className="inline-flex items-center gap-2"><RefreshCw className="size-4" />{refreshLabel(subscription.refresh_hours, t)}</span> : null}
            {subscription.support_url ? <a className="inline-flex items-center gap-2 font-medium text-foreground underline-offset-4 hover:underline" href={subscription.support_url} target="_blank" rel="noreferrer"><LifeBuoy className="size-4" />{t("Support")}</a> : null}
            {subscription.profile_url ? <a className="inline-flex items-center gap-2 font-medium text-foreground underline-offset-4 hover:underline" href={subscription.profile_url} target="_blank" rel="noreferrer"><House className="size-4" />{t("Homepage")}</a> : null}
          </section>
        ) : null}

        {subscription.announcement ? <section className="mb-5 border-l-[3px] border-primary bg-primary-soft px-5 py-4"><h2 className="m-0 text-sm font-semibold text-primary-strong">{t("Announcement")}</h2><p className="mt-2 mb-0 whitespace-pre-wrap text-xs leading-6 text-foreground/80">{subscription.announcement}</p></section> : null}

        <Card aria-labelledby="services-title">
          <CardHeader>
            <CardTitle id="services-title">{t("Services")}</CardTitle>
            <CardDescription>{t("{count} services are available", { count: subscription.services.length })}</CardDescription>
            {downloadsAvailable ? <CardAction className="flex flex-wrap justify-end gap-2 max-[640px]:hidden"><DownloadLink href={downloadURL(token, "base64")} label="Base64" /><DownloadLink href={downloadURL(token, "mihomo.yaml")} label="Mihomo" /><DownloadLink href={downloadURL(token, "sing-box.json")} label="sing-box" /></CardAction> : null}
          </CardHeader>
          <CardContent className="divide-y divide-border">
            {subscription.services.length === 0 ? <p className="my-0 py-10 text-center text-sm text-muted-foreground">{t("No services available.")}</p> : null}
            {subscription.services.map((service) => (
              <div className="flex items-center gap-4 py-4" key={`${service.plugin_id}/${service.service_id}`}>
                <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary-soft text-primary"><Server size={17} /></span>
                <span className="grid min-w-0 flex-1 gap-1"><strong className="truncate text-sm font-semibold">{service.display_name}</strong><small className="truncate text-xs text-muted-foreground" title={`${service.plugin_id} / ${service.service_id}`}>{service.plugin_id} / {service.service_id}</small></span>
                <span className="max-w-[40%] truncate text-right text-xs text-muted-foreground max-[560px]:hidden">{service.capabilities.join(", ") || t("No reported capabilities")}</span>
              </div>
            ))}
          </CardContent>
          {downloadsAvailable ? <CardFooter className="hidden flex-wrap gap-2 border-t max-[640px]:flex"><DownloadLink href={downloadURL(token, "base64")} label="Base64" /><DownloadLink href={downloadURL(token, "mihomo.yaml")} label="Mihomo" /><DownloadLink href={downloadURL(token, "sing-box.json")} label="sing-box" /></CardFooter> : null}
        </Card>
      </main>
    </div>
  );
}

export function refreshLabel(hours: number, t: (message: string, values?: Record<string, string | number>) => string): string {
  return hours === 1 ? t("Refresh every hour") : t("Refresh every {count} hours", { count: hours });
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

function statusTone(status: SubscriptionInfo["status"]): "success" | "muted" | "warning" {
  if (status === "active") return "success";
  if (status === "disabled") return "muted";
  return "warning";
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
