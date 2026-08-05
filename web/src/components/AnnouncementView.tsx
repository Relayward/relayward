import { useEffect, useState } from "react";
import { Save } from "lucide-react";

import { APIError, getAnnouncement, updateAnnouncement } from "../api";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { BrandMark, PageHeader, StatusBadge } from "./PageLayout";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardHeader, CardTitle } from "./ui/card";
import { Textarea } from "./ui/textarea";

export function AnnouncementView() {
  const { t } = useI18n();
  const [content, setContent] = useState("");
  const [loading, setLoading] = useState(true);
  const [busy, setBusy] = useState(false);
  const [saved, setSaved] = useState(false);
  const [error, setError] = useState<string>();

  useEffect(() => {
    let active = true;
    getAnnouncement().then((value) => {
      if (active) setContent(value ?? "");
    }, (cause) => {
      if (active) setError(errorMessage(cause));
    }).finally(() => {
      if (active) setLoading(false);
    });
    return () => { active = false; };
  }, []);

  async function save() {
    setBusy(true);
    setSaved(false);
    setError(undefined);
    try {
      setContent((await updateAnnouncement(content)) ?? "");
      setSaved(true);
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  return (
    <section aria-labelledby="announcement-title">
      <PageHeader id="announcement-title" eyebrow={t("Subscriptions")} title={t("Announcement")} description={t("Edit the announcement shown on every public subscription page.")} actions={<Button size="sm" disabled={loading || busy} onClick={save} type="button"><Save size={17} />{busy ? t("Saving...") : t("Save")}</Button>} />
      <div className="grid grid-cols-[minmax(0,1.7fr)_minmax(280px,.8fr)] gap-5 max-[900px]:grid-cols-1">
        <Card>
          <CardHeader>
            <CardTitle>{t("Content")}</CardTitle>
            <CardDescription>{t("Public subscription announcement")}</CardDescription>
          </CardHeader>
          <CardContent>
            <label className="grid gap-2">
              <Textarea className="min-h-[430px] bg-background/55" value={content} onChange={(event) => { setContent(event.target.value); setSaved(false); }} maxLength={4096} rows={16} disabled={loading} />
              <span className="text-right text-xs text-muted-foreground">{content.length} / 4096</span>
            </label>
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>{t("Subscription preview")}</CardTitle>
            <CardAction><StatusBadge tone="success">{t("Live preview")}</StatusBadge></CardAction>
          </CardHeader>
          <CardContent>
            <div className="min-h-[430px] rounded-md border border-border bg-background p-5">
              <BrandMark />
              <div className="mt-10 border-t border-border pt-5">
                <h3 className="m-0 text-sm font-semibold">{t("Announcement")}</h3>
                <p className="mt-4 mb-0 whitespace-pre-wrap text-sm leading-6 text-muted-foreground">{content || t("No announcement has been published.")}</p>
              </div>
            </div>
          </CardContent>
        </Card>
      </div>
      {saved ? <p className="mt-3 mb-0 text-sm font-semibold text-success" role="status">{t("Saved.")}</p> : null}
      {error ? <div className="mt-3"><FormError message={t(error)} /></div> : null}
    </section>
  );
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
