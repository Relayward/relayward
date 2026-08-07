import { AlertTriangle } from "lucide-react";

import { useI18n } from "../i18n";
import { cn } from "../lib/utils";

export function InsecureTransportWarning({ className }: { className?: string }) {
  const { t } = useI18n();
  if (window.location.protocol !== "http:") return null;

  return (
    <div className={cn("flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2.5 text-sm text-warning-strong", className)} role="status">
      <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
      <span>{t("This page uses HTTP. Administrator credentials and session data are not encrypted in transit. HTTPS is strongly recommended.")}</span>
    </div>
  );
}
