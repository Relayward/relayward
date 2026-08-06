import { useEffect, useState, type ReactNode } from "react";
import { LoaderCircle, RefreshCw, RotateCcw } from "lucide-react";

import {
  APIError,
  getAgentUpdateAvailability,
  getLatestAgentUpdate,
  getNode,
  requestLatestAgentUpdate,
  type AgentUpdate,
  type AgentUpdateAvailability,
  type Node,
} from "../api";
import { agentUpdatePresentation } from "../agentUpdate";
import { useI18n } from "../i18n";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { StatusBadge } from "./PageLayout";
import { Button } from "./ui/button";
import { DialogFooter } from "./ui/dialog";

export function AgentUpdateDialog({ node, latest, onClose, onNodeUpdated, onUpdateChanged }: {
  node: Node;
  latest: AgentUpdate | null | undefined;
  onClose: () => void;
  onNodeUpdated: (value: Node) => void;
  onUpdateChanged: (nodeID: string, value: AgentUpdate | null) => void;
}) {
  const { t, formatDateTime } = useI18n();
  const [currentNode, setCurrentNode] = useState(node);
  const [currentUpdate, setCurrentUpdate] = useState(latest);
  const [availability, setAvailability] = useState<AgentUpdateAvailability>();
  const [checking, setChecking] = useState(true);
  const [busy, setBusy] = useState(false);
  const [releaseError, setReleaseError] = useState<string>();
  const [monitorError, setMonitorError] = useState<string>();

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      const [nodeResult, updateResult, availabilityResult] = await Promise.allSettled([
        getNode(node.id),
        getLatestAgentUpdate(node.id),
        getAgentUpdateAvailability(node.id),
      ]);
      if (!active) return;

      const monitoringFailures: unknown[] = [];
      if (nodeResult.status === "fulfilled") {
        setCurrentNode(nodeResult.value);
        onNodeUpdated(nodeResult.value);
      } else {
        monitoringFailures.push(nodeResult.reason);
      }
      if (updateResult.status === "fulfilled") {
        setCurrentUpdate(updateResult.value);
        onUpdateChanged(node.id, updateResult.value);
      } else {
        monitoringFailures.push(updateResult.reason);
      }
      setMonitorError(monitoringFailures.length > 0 ? errorMessage(monitoringFailures[0]) : undefined);

      if (availabilityResult.status === "fulfilled") {
        setAvailability(availabilityResult.value);
        setReleaseError(undefined);
      } else {
        setAvailability(undefined);
        setReleaseError(errorMessage(availabilityResult.reason));
      }
      setChecking(false);
    };

    void refresh();
    const timer = window.setInterval(() => { void refresh(); }, 2_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [node.id, onNodeUpdated, onUpdateChanged]);

  async function queueLatest() {
    setBusy(true);
    setReleaseError(undefined);
    try {
      const value = await requestLatestAgentUpdate(node.id);
      setCurrentUpdate(value);
      onUpdateChanged(node.id, value);
    } catch (cause) {
      setReleaseError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  const presentation = agentUpdatePresentation(currentUpdate, currentNode.agent_status);
  const awaitingVersionReport = currentUpdate?.status === "succeeded" && currentUpdate.version === availability?.latest_release.version;
  const canUpdate = availability?.relation === "available" && !presentation.active && !awaitingVersionReport;
  return (
    <Modal title={t("{name} Agent update", { name: currentNode.name })} onClose={onClose} width="wide">
      <div className="grid gap-4">
        <section className="grid gap-3 rounded-lg border p-4" aria-labelledby="agent-release-title">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="m-0 text-sm font-semibold" id="agent-release-title">{t("Official release")}</h3>
            {availability ? <StatusBadge tone={relationTone(availability.relation)}>{t(relationLabel(availability.relation))}</StatusBadge> : null}
          </div>
          {checking && !availability ? (
            <p className="m-0 flex items-center text-sm text-muted-foreground"><LoaderCircle className="mr-2 size-4 animate-spin" />{t("Checking latest release...")}</p>
          ) : null}
          {availability ? (
            <dl className="m-0 grid gap-2 text-sm sm:grid-cols-3">
              <UpdateDetail label={t("Current version")}>{availability.current_version || t("Not reported")}</UpdateDetail>
              <UpdateDetail label={t("Latest version")}>{availability.latest_release.version}</UpdateDetail>
              <UpdateDetail label={t("Published")}>{formatDateTime(availability.latest_release.published_at)}</UpdateDetail>
            </dl>
          ) : null}
          <FormError message={releaseError !== undefined ? t(releaseError) : undefined} />
        </section>

        <section className="grid gap-3 rounded-lg border p-4" aria-labelledby="agent-update-operation-title" aria-live="polite">
          <div className="flex flex-wrap items-center justify-between gap-2">
            <h3 className="m-0 text-sm font-semibold" id="agent-update-operation-title">{t("Latest update operation")}</h3>
            <StatusBadge tone={presentation.tone}>
              {presentation.active ? <LoaderCircle className="animate-spin" /> : currentUpdate?.problem?.message.toLocaleLowerCase().includes("rolled back") ? <RotateCcw /> : null}
              {t(presentation.label)}
            </StatusBadge>
          </div>
          {currentUpdate ? (
            <dl className="m-0 grid gap-2 text-sm sm:grid-cols-2">
              <UpdateDetail label={t("Target version")}>{currentUpdate.version}</UpdateDetail>
              <UpdateDetail label={t("Delivery attempts")}>{currentUpdate.attempts}</UpdateDetail>
              <UpdateDetail label={t("Last sent")}>{currentUpdate.last_sent_at ? formatDateTime(currentUpdate.last_sent_at) : t("Not yet")}</UpdateDetail>
              <UpdateDetail label={t("Completed")}>{currentUpdate.completed_at ? formatDateTime(currentUpdate.completed_at) : t("Not yet")}</UpdateDetail>
              <UpdateDetail label={t("Queued")}>{formatDateTime(currentUpdate.created_at)}</UpdateDetail>
              <UpdateDetail label={t("Expires")}>{formatDateTime(currentUpdate.expires_at)}</UpdateDetail>
            </dl>
          ) : null}
          {presentation.detail ? <p className="m-0 text-sm text-muted-foreground">{t(presentation.detail)}</p> : null}
          <FormError message={monitorError !== undefined ? t(monitorError) : undefined} />
        </section>

        <DialogFooter>
          <Button variant="ghost" onClick={onClose} type="button">{t("Close")}</Button>
          {canUpdate ? (
            <Button disabled={busy} onClick={queueLatest} type="button">
              {busy ? <LoaderCircle className="animate-spin" /> : <RefreshCw />}
              {busy ? t("Queuing...") : t("Update to {version}", { version: availability.latest_release.version })}
            </Button>
          ) : null}
        </DialogFooter>
      </div>
    </Modal>
  );
}

function UpdateDetail({ label, children }: { label: string; children: ReactNode }) {
  return (
    <div className="flex min-h-10 items-center justify-between gap-4 rounded-lg border px-3 py-2.5">
      <dt className="text-muted-foreground">{label}</dt>
      <dd className="m-0 [overflow-wrap:anywhere] text-right">{children}</dd>
    </div>
  );
}

function relationLabel(relation: AgentUpdateAvailability["relation"]): string {
  if (relation === "available") return "Update available";
  if (relation === "current") return "Up to date";
  if (relation === "ahead") return "Newer than official release";
  return "Version cannot be compared";
}

function relationTone(relation: AgentUpdateAvailability["relation"]): "success" | "warning" | "muted" {
  if (relation === "current") return "success";
  if (relation === "available") return "warning";
  return "muted";
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
