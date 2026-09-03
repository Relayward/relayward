import { type FormEvent, type ReactNode, useEffect, useState } from "react";
import { AlertTriangle, Globe2, Save, Settings2, WalletCards } from "lucide-react";

import { APIError, getSystemSettings, updateSystemSettings, type SystemSettings } from "../api";
import { useI18n } from "../i18n";
import { timezoneOptions } from "../timezones";
import { FormError } from "./AuthScreen";
import { PageHeader, StatusBadge } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { Combobox } from "./ui/combobox";
import { Input } from "./ui/input";
import { Label } from "./ui/label";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";

type EditableSettings = Omit<SystemSettings, "updated_at">;
type SettingsTab = "defaults" | "public-access" | "subscription";

export function SettingsView() {
  const { t, formatDateTime } = useI18n();
  const [value, setValue] = useState<SystemSettings>();
  const [draft, setDraft] = useState<EditableSettings>();
  const [tab, setTab] = useState<SettingsTab>("defaults");
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    getSystemSettings().then((settings) => {
      if (!active) return;
      setValue(settings);
      setDraft(editable(settings, window.location.origin));
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    });
    return () => { active = false; };
  }, []);

  function update<K extends keyof EditableSettings>(key: K, next: EditableSettings[K]) {
    setDraft((current) => current ? { ...current, [key]: next } : current);
    setSaved(false);
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!draft) return;
    setBusy(true);
    setSaved(false);
    setError(undefined);
    try {
      const updated = await updateSystemSettings(draft);
      setValue(updated);
      setDraft(editable(updated, window.location.origin));
      setSaved(true);
    } catch (cause) {
      const errorTab = settingsTabForError(cause);
      if (errorTab) setTab(errorTab);
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <Tabs value={tab} onValueChange={(value) => setTab(value as SettingsTab)}>
      <section aria-labelledby="settings-title">
        <PageHeader
          id="settings-title"
          eyebrow={t("System")}
          title={t("Settings")}
          description={t("Manage control plane defaults and public subscription identity.")}
          actions={<Button form="system-settings-form" size="sm" disabled={!draft || busy} type="submit"><Save />{busy ? t("Saving...") : t("Save changes")}</Button>}
        />
        <form id="system-settings-form" onSubmit={save}>
          <div className="mb-4 overflow-x-auto pb-1">
            <TabsList className="min-w-max" aria-label={t("Settings")}>
              <TabsTrigger value="defaults">{t("Control plane defaults")}</TabsTrigger>
              <TabsTrigger value="public-access">{t("Public access")}</TabsTrigger>
              <TabsTrigger value="subscription">{t("Subscription profile")}</TabsTrigger>
            </TabsList>
          </div>

          <TabsContent value="defaults">
            <Card>
              <CardHeader className="max-sm:has-data-[slot=card-action]:grid-cols-1">
                <CardTitle className="flex items-center gap-2"><Settings2 className="size-4" />{t("Control plane defaults")}</CardTitle>
                <CardDescription>{t("Defaults used for new sessions and authorization schedules.")}</CardDescription>
                {value ? <CardAction className="max-sm:col-start-1 max-sm:row-start-3 max-sm:row-span-1 max-sm:min-w-0 max-sm:max-w-full max-sm:justify-self-start"><StatusBadge className="max-w-full" tone="muted">{t("Updated {time}", { time: formatDateTime(value.updated_at) })}</StatusBadge></CardAction> : null}
              </CardHeader>
              <CardContent className="grid gap-5 md:grid-cols-2">
                <SettingField id="settings-timezone" label={t("Default timezone")} description={t("Used when creating a new authorization.")}>
                  <Combobox
                    id="settings-timezone"
                    value={draft?.timezone ?? ""}
                    onValueChange={(value) => update("timezone", value)}
                    options={timezoneOptions(draft?.timezone)}
                    searchPlaceholder={t("Search options...")}
                    emptyText={t("No matching options.")}
                    allowCustomValue
                    customValueLabel={(value) => t("Use {value}", { value })}
                    required
                  />
                </SettingField>
                <SettingField id="settings-session-lifetime" label={t("Session lifetime")} description={t("Applies to newly created administrator sessions.")} suffix={t("minutes")}>
                  <Input id="settings-session-lifetime" type="number" min="60" max="525600" step="1" value={draft?.session_lifetime_minutes ?? ""} onChange={(event) => update("session_lifetime_minutes", Number(event.target.value))} required />
                </SettingField>
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="public-access">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2"><Globe2 className="size-4" />{t("Public access")}</CardTitle>
                <CardDescription>{t("Canonical HTTP or HTTPS origin used when generating public links.")}</CardDescription>
              </CardHeader>
              <CardContent>
                <SettingField id="settings-public-url" label={t("{field} (optional)", { field: t("Public URL") })} description={t("Defaults to the address currently opened in the browser. You can change it.")}>
                  <Input id="settings-public-url" type="url" placeholder="https://panel.example.com" maxLength={2048} value={draft?.public_url ?? ""} onChange={(event) => update("public_url", event.target.value)} />
                  {draft?.public_url.trim().toLowerCase().startsWith("http://") ? (
                    <p className="m-0 flex items-start gap-2 text-sm text-warning-strong" role="status">
                      <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                      <span>{t("This Public URL uses HTTP. Public links and Agent connections will not be encrypted in transit.")}</span>
                    </p>
                  ) : null}
                </SettingField>
              </CardContent>
            </Card>
          </TabsContent>

          <TabsContent value="subscription">
            <Card>
              <CardHeader>
                <CardTitle className="flex items-center gap-2"><WalletCards className="size-4" />{t("Subscription profile")}</CardTitle>
                <CardDescription>{t("Identity and refresh information shown to subscription users and clients.")}</CardDescription>
              </CardHeader>
              <CardContent className="grid gap-5 md:grid-cols-2">
                <SettingField id="settings-subscription-title" label={t("Subscription title")}>
                  <Input id="settings-subscription-title" maxLength={100} value={draft?.subscription_title ?? ""} onChange={(event) => update("subscription_title", event.target.value)} required />
                </SettingField>
                <SettingField id="settings-refresh-hours" label={t("Refresh interval")} description={t("Set to 0 to omit the client refresh hint.")} suffix={t("hours")}>
                  <Input id="settings-refresh-hours" type="number" min="0" max="8760" step="1" value={draft?.subscription_refresh_hours ?? ""} onChange={(event) => update("subscription_refresh_hours", Number(event.target.value))} required />
                </SettingField>
                <SettingField id="settings-support-url" label={t("{field} (optional)", { field: t("Support URL") })}>
                  <Input id="settings-support-url" type="url" placeholder="https://support.example.com" maxLength={2048} value={draft?.support_url ?? ""} onChange={(event) => update("support_url", event.target.value)} />
                </SettingField>
                <SettingField id="settings-profile-url" label={t("{field} (optional)", { field: t("Profile URL") })}>
                  <Input id="settings-profile-url" type="url" placeholder="https://example.com/account" maxLength={2048} value={draft?.profile_url ?? ""} onChange={(event) => update("profile_url", event.target.value)} />
                </SettingField>
              </CardContent>
            </Card>
          </TabsContent>

          {saved ? <p className="mt-4 mb-0 text-sm font-medium text-success" role="status">{t("Settings saved.")}</p> : null}
          {error ? <div className="mt-4"><FormError message={t(error)} /></div> : null}
        </form>
      </section>
    </Tabs>
  );
}

function SettingField({ id, label, description, suffix, children }: {
  id: string;
  label: string;
  description?: string;
  suffix?: string;
  children: ReactNode;
}) {
  return (
    <div className="grid content-start gap-2">
      <div className="grid gap-1">
        <Label htmlFor={id}>{label}{suffix ? <span className="font-normal text-muted-foreground"> ({suffix})</span> : null}</Label>
        {description ? <p className="m-0 text-xs text-muted-foreground">{description}</p> : null}
      </div>
      {children}
    </div>
  );
}

function editable(value: SystemSettings, currentOrigin: string): EditableSettings {
  const { updated_at: _, ...settings } = value;
  return { ...settings, public_url: defaultPublicURL(settings.public_url, currentOrigin) };
}

export function defaultPublicURL(configuredURL: string, currentOrigin: string): string {
  return configuredURL || currentOrigin;
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) {
    switch (cause.violations[0]?.field) {
      case "session_lifetime_minutes": return "Session lifetime must be between 60 and 525600 minutes.";
      case "timezone": return "Enter a valid IANA time zone.";
      case "public_url": return "Public URL must be an HTTP or HTTPS origin without a path, query, fragment, or credentials.";
      case "subscription_title": return "Subscription title must contain 1 to 100 characters on one line.";
      case "support_url": return "Support URL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment.";
      case "profile_url": return "Profile URL must be an absolute HTTP or HTTPS URL without credentials, query, or fragment.";
      case "subscription_refresh_hours": return "Refresh interval must be between 0 and 8760 hours.";
      default: return cause.message;
    }
  }
  return "The request could not be completed.";
}

function settingsTabForError(cause: unknown): SettingsTab | undefined {
  if (!(cause instanceof APIError)) return undefined;
  switch (cause.violations[0]?.field) {
    case "timezone":
    case "session_lifetime_minutes":
      return "defaults";
    case "public_url":
      return "public-access";
    case "subscription_title":
    case "support_url":
    case "profile_url":
    case "subscription_refresh_hours":
      return "subscription";
    default:
      return undefined;
  }
}
