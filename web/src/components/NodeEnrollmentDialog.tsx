import { useEffect, useState } from "react";
import { AlertTriangle, CheckCircle2, Clipboard, LoaderCircle, RefreshCw, Server } from "lucide-react";

import {
  APIError,
  createNodeRegistrationToken,
  getSystemSettings,
  listNodes,
  type Node,
  type NodeRegistrationToken,
} from "../api";
import { useI18n } from "../i18n";
import { buildAgentInstallCommand, type AgentInstallCommand } from "../nodeEnrollment";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { StatusBadge } from "./PageLayout";
import { Button } from "./ui/button";
import { DialogFooter } from "./ui/dialog";

interface EnrollmentBundle {
  token: NodeRegistrationToken;
  install: AgentInstallCommand;
}

export function NodeEnrollmentDialog({ node, mode, onClose, onNodeUpdated }: {
  node: Node;
  mode: "register" | "reregister";
  onClose: () => void;
  onNodeUpdated: (node: Node) => void;
}) {
  const { t, formatDateTime } = useI18n();
  const [initialRegisteredAt] = useState(node.registered_at);
  const [currentNode, setCurrentNode] = useState(node);
  const [bundle, setBundle] = useState<EnrollmentBundle>();
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [monitorError, setMonitorError] = useState<string>();
  const [copied, setCopied] = useState(false);
  const [generation, setGeneration] = useState(0);
  const [clock, setClock] = useState(() => Date.now());

  useEffect(() => {
    let active = true;
    const generate = async () => {
      setLoading(true);
      setBundle(undefined);
      setError(undefined);
      setCopied(false);
      try {
        const settings = await getSystemSettings();
        buildAgentInstallCommand("validation-token", settings.public_url, window.location.origin);
        const token = await createNodeRegistrationToken(node.id);
        const install = buildAgentInstallCommand(token.token, settings.public_url, window.location.origin);
        if (active) setBundle({ token, install });
      } catch (cause) {
        if (active) {
          setBundle(undefined);
          setError(errorMessage(cause));
        }
      } finally {
        if (active) setLoading(false);
      }
    };
    void generate();
    return () => { active = false; };
  }, [generation, node.id]);

  useEffect(() => {
    let active = true;
    const refresh = async () => {
      try {
        const nodes = await listNodes();
        const updated = nodes.find((candidate) => candidate.id === node.id);
        if (!updated) throw new Error("The node no longer exists.");
        if (active) {
          setCurrentNode(updated);
          setMonitorError(undefined);
          onNodeUpdated(updated);
        }
      } catch (cause) {
        if (active) setMonitorError(errorMessage(cause));
      }
    };
    const timer = window.setInterval(() => { void refresh(); }, 2_000);
    return () => {
      active = false;
      window.clearInterval(timer);
    };
  }, [node.id, onNodeUpdated]);

  useEffect(() => {
    const timer = window.setInterval(() => setClock(Date.now()), 1_000);
    return () => window.clearInterval(timer);
  }, []);

  const registrationChanged = currentNode.registered_at !== null && currentNode.registered_at !== initialRegisteredAt;
  const online = registrationChanged && currentNode.agent_status === "online";
  const expired = bundle !== undefined && Date.parse(bundle.token.expires_at) <= clock && !registrationChanged;

  async function copyCommand() {
    if (!bundle) return;
    setError(undefined);
    try {
      await navigator.clipboard.writeText(bundle.install.command);
      setCopied(true);
    } catch {
      setCopied(false);
      setError("The install command could not be copied.");
    }
  }

  const generationFailed = error !== undefined && bundle === undefined;
  const phase = registrationPhase(registrationChanged, online, currentNode.enabled, expired, loading, generationFailed);
  return (
    <Modal
      title={t(mode === "reregister" ? "Re-register {name} Agent" : "Register {name} Agent", { name: node.name })}
      onClose={onClose}
      dismissible={false}
      width="wide"
    >
      <div className="grid gap-4">
        <div className="flex items-center justify-between gap-4 rounded-lg border p-4">
          <span className="flex min-w-0 items-center gap-3">
            <span className="flex size-9 shrink-0 items-center justify-center rounded-md bg-primary-soft text-primary"><Server /></span>
            <span className="grid min-w-0 gap-0.5">
              <strong className="truncate text-sm font-semibold">{node.name}</strong>
              <small className="truncate text-xs text-muted-foreground">{currentNode.hostname || t("Not registered")}</small>
            </span>
          </span>
          <StatusBadge tone={phase.tone}>{phase.busy ? <LoaderCircle className="mr-1 inline size-3 animate-spin" /> : null}{t(phase.label)}</StatusBadge>
        </div>

        {mode === "reregister" && !registrationChanged ? (
          <p className="m-0 border-l-[3px] border-warning bg-warning/10 p-3 text-sm text-warning-strong">
            {t("Generating this command does not revoke the current Agent. A failed replacement keeps its existing identity.")}
          </p>
        ) : null}
        <FormError message={error !== undefined ? t(error) : undefined} />
        <FormError message={monitorError !== undefined ? t(monitorError) : undefined} />

        {loading ? (
          <div className="flex min-h-32 items-center justify-center rounded-lg border text-sm text-muted-foreground">
            <LoaderCircle className="mr-2 size-4 animate-spin" />{t("Generating install command...")}
          </div>
        ) : null}

        {bundle && !registrationChanged ? (
          <section className="min-w-0 space-y-3" aria-labelledby="agent-install-command-title">
            <div className="flex flex-wrap items-center justify-between gap-2">
              <h3 className="m-0 text-sm font-semibold" id="agent-install-command-title">{t("Root install command")}</h3>
              <span className="text-xs text-muted-foreground">{bundle.install.serverURL}</span>
            </div>
            <pre className="m-0 max-w-full overflow-x-auto rounded-lg border bg-muted p-4 text-sm leading-6"><code>{bundle.install.command}</code></pre>
            {bundle.install.insecure ? (
              <p className="m-0 flex items-start gap-2 rounded-lg border border-warning/40 bg-warning/10 px-3 py-2.5 text-sm text-warning-strong" role="status">
                <AlertTriangle className="mt-0.5 size-4 shrink-0" aria-hidden="true" />
                <span>{t("This Agent will connect over unencrypted HTTP. Registration credentials, commands, and telemetry can be intercepted or changed in transit.")}</span>
              </p>
            ) : null}
            <dl className="grid gap-2 text-sm sm:grid-cols-2">
              <div className="flex items-center justify-between gap-3 rounded-lg border px-3 py-2.5"><dt className="text-muted-foreground">{t("Token expires")}</dt><dd className="m-0 text-right">{formatDateTime(bundle.token.expires_at)}</dd></div>
              <div className="flex items-center justify-between gap-3 rounded-lg border px-3 py-2.5"><dt className="text-muted-foreground">{t("Supported systems")}</dt><dd className="m-0 text-right">Debian / Alpine · AMD64</dd></div>
            </dl>
          </section>
        ) : null}

        {registrationChanged ? (
          <section className="grid gap-3 rounded-lg border p-4" aria-live="polite">
            <span className="flex items-center gap-3">
              <CheckCircle2 className={online ? "text-success-strong" : "text-warning-strong"} />
              <span className="grid gap-0.5">
                <strong className="text-sm font-semibold">{t(online ? "Agent registered and online" : currentNode.enabled ? "Agent registered; waiting for connection" : "Agent registered; node is disabled")}</strong>
                <small className="text-xs text-muted-foreground">{currentNode.agent_version || t("Version not reported yet")}</small>
              </span>
            </span>
          </section>
        ) : null}

        <DialogFooter>
          {(generationFailed || expired) ? (
            <Button variant="outline" disabled={loading} onClick={() => setGeneration((value) => value + 1)} type="button">
              <RefreshCw />{t(expired ? "Generate new command" : "Retry")}
            </Button>
          ) : null}
          {bundle && !registrationChanged && !expired ? (
            <Button variant="secondary" onClick={copyCommand} type="button"><Clipboard />{t(copied ? "Copied" : "Copy command")}</Button>
          ) : null}
          <Button onClick={onClose} type="button">{t("Done")}</Button>
        </DialogFooter>
      </div>
    </Modal>
  );
}

function registrationPhase(registered: boolean, online: boolean, enabled: boolean, expired: boolean, loading: boolean, failed: boolean): {
  label: string;
  tone: "success" | "warning" | "danger";
  busy: boolean;
} {
  if (online) return { label: "Online", tone: "success", busy: false };
  if (registered && !enabled) return { label: "Registered; disabled", tone: "warning", busy: false };
  if (registered) return { label: "Connecting", tone: "warning", busy: true };
  if (expired) return { label: "Token expired", tone: "danger", busy: false };
  if (failed) return { label: "Setup unavailable", tone: "danger", busy: false };
  if (loading) return { label: "Generating", tone: "warning", busy: true };
  return { label: "Waiting for Agent", tone: "warning", busy: true };
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  if (cause instanceof Error) return cause.message;
  return "The request could not be completed.";
}
