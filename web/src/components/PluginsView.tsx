import { type FormEvent, type ReactNode, useEffect, useId, useState } from "react";
import { Activity, Boxes, ExternalLink, KeyRound, PackagePlus, Puzzle, RefreshCw, ServerCog, Trash2 } from "lucide-react";

import {
  APIError,
  inspectPluginRelease,
  installPlugin,
  listPluginInstallations,
  replacePluginGitHubToken,
  uninstallPlugin,
  upgradePlugin,
  type PluginInstallation,
  type PluginReleaseCandidate,
} from "../api";
import { useI18n } from "../i18n";
import { cn } from "../lib/utils";
import { pluginPermissionPresentation } from "../pluginPermissions";
import { FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { PageHeader, StatusBadge, SummaryBar, SummaryItem } from "./PageLayout";
import { PluginFrame, type PluginNavigationTarget } from "./PluginFrame";
import { PluginInstancesView } from "./PluginInstancesView";
import { Button } from "./ui/button";
import { Card, CardAction, CardContent, CardDescription, CardFooter, CardHeader, CardTitle } from "./ui/card";
import { Checkbox } from "./ui/checkbox";
import { DialogFooter } from "./ui/dialog";
import { Input } from "./ui/input";
import { Tabs, TabsContent, TabsList, TabsTrigger } from "./ui/tabs";
import { Tooltip, TooltipContent, TooltipTrigger } from "./ui/tooltip";

type PluginTab = "installations" | "instances";

export function PluginsView({ onNavigate }: { onNavigate: (target: PluginNavigationTarget) => void }) {
  const { t } = useI18n();
  const [tab, setTab] = useState<PluginTab>("installations");
  const [openedPlugin, setOpenedPlugin] = useState<PluginInstallation>();

  if (openedPlugin !== undefined) {
    return <PluginFrame plugin={openedPlugin} onClose={() => setOpenedPlugin(undefined)} onNavigate={onNavigate} />;
  }
  return (
    <Tabs value={tab} onValueChange={(value) => setTab(value as PluginTab)}>
      <section aria-labelledby="plugins-title">
        <PageHeader
          id="plugins-title"
          eyebrow={t("Extensions")}
          title={t("Plugins")}
          description={t("Install center features and manage runtime plugins across nodes.")}
          actions={<TabsList aria-label={t("Plugin view")}>
            <TabsTrigger value="installations">{t("Installations")}</TabsTrigger>
            <TabsTrigger value="instances">{t("Node instances")}</TabsTrigger>
          </TabsList>}
        />
        <TabsContent value="installations"><PluginInstallationsView onOpen={setOpenedPlugin} /></TabsContent>
        <TabsContent value="instances"><PluginInstancesView embedded /></TabsContent>
      </section>
    </Tabs>
  );
}

function PluginInstallationsView({ onOpen }: { onOpen: (plugin: PluginInstallation) => void }) {
  const { t } = useI18n();
  const [items, setItems] = useState<PluginInstallation[]>([]);
  const [loading, setLoading] = useState(true);
  const [error, setError] = useState<string>();
  const [installing, setInstalling] = useState(false);
  const [upgrading, setUpgrading] = useState<PluginInstallation>();
  const [removing, setRemoving] = useState<PluginInstallation>();
  const [replacingToken, setReplacingToken] = useState<PluginInstallation>();

  useEffect(() => {
    let active = true;
    listPluginInstallations().then((values) => {
      if (active) {
        setItems(values);
        setLoading(false);
      }
    }, (cause) => {
      if (active) {
        setError(errorMessage(cause));
        setLoading(false);
      }
    });
    return () => { active = false; };
  }, []);

  function replace(value: PluginInstallation) {
    setItems((current) => {
      const found = current.some((item) => item.plugin_id === value.plugin_id);
      return (found ? current.map((item) => item.plugin_id === value.plugin_id ? value : item) : [...current, value])
        .sort((left, right) => left.manifest.name.localeCompare(right.manifest.name));
    });
  }

  const active = items.filter((item) => item.state === "active").length;
  const healthy = items.filter((item) => item.health === "healthy").length;
  const runtime = items.filter((item) => item.kind === "runtime").length;
  const features = items.filter((item) => item.kind === "feature").length;

  return (
    <>
      <SummaryBar>
        <SummaryItem icon={<Boxes size={17} />} label={t("Installed plugins")} value={items.length} tone="primary" />
        <SummaryItem icon={<Activity size={17} />} label={t("Active plugins")} value={`${active} / ${items.length}`} tone={items.length === 0 ? "default" : active === items.length ? "success" : "warning"} />
        <SummaryItem icon={<ServerCog size={17} />} label={t("Runtime plugins")} value={runtime} />
        <SummaryItem icon={<Puzzle size={17} />} label={t("Feature plugins")} value={features} note={t("{count} healthy", { count: healthy })} />
      </SummaryBar>
      <div className="mb-3 flex min-h-10 flex-wrap items-center justify-end gap-4">
        {error ? <div className="mr-auto"><FormError message={t(error)} /></div> : null}
        <Button size="sm" onClick={() => setInstalling(true)} type="button">
          <PackagePlus size={17} />{t("Install plugin")}
        </Button>
      </div>
      <div className="grid gap-4 md:grid-cols-2 xl:grid-cols-3">
        {items.map((item) => {
          const hasUI = item.manifest.artifacts.some((artifact) => artifact.role === "ui");
          return (
            <Card key={item.plugin_id}>
              <CardHeader>
                <CardTitle>{item.manifest.name}</CardTitle>
                <CardDescription className="truncate" title={item.plugin_id}>{item.plugin_id}</CardDescription>
                <CardAction><Status value={t(label(item.state))} tone={installationStateTone(item.state)} /></CardAction>
              </CardHeader>
              <CardContent>
                <dl className="grid grid-cols-2 gap-4 text-sm">
                  <PluginDetail label={t("Kind")} value={t(label(item.kind))} />
                  <PluginDetail label={t("Version")} value={item.active_version} />
                  <PluginDetail label={t("Health")} value={<Status value={t(label(item.health))} tone={item.health === "healthy" ? "ok" : item.health === "unhealthy" ? "error" : "muted"} />} />
                  <PluginDetail label={t("Permissions")} value={item.approved_permissions.length} />
                </dl>
              </CardContent>
              <CardFooter className="justify-end gap-1 border-t pt-6">
                {hasUI ? (
                  <Button variant="outline" size="sm" onClick={() => onOpen(item)}><ExternalLink />{t("Open plugin")}</Button>
                ) : null}
                <IconAction label={t("Upgrade {name}", { name: item.manifest.name })} description={t("Check for upgrade")} onClick={() => setUpgrading(item)}><RefreshCw size={17} /></IconAction>
                <IconAction label={t("Replace GitHub token for {name}", { name: item.manifest.name })} description={t("Replace GitHub token")} onClick={() => setReplacingToken(item)}><KeyRound size={17} /></IconAction>
                <IconAction label={t("Uninstall {name}", { name: item.manifest.name })} description={t("Uninstall plugin")} danger onClick={() => setRemoving(item)}><Trash2 size={17} /></IconAction>
              </CardFooter>
            </Card>
          );
        })}
        {loading ? <Card className="flex min-h-40 items-center justify-center text-sm text-muted-foreground">{t("Loading...")}</Card> : null}
        {!loading && items.length === 0 ? <Card className="flex min-h-40 items-center justify-center text-sm text-muted-foreground">{t("No plugins are installed.")}</Card> : null}
      </div>
      {installing ? <PluginReleaseDialog onClose={() => setInstalling(false)} onSaved={(value) => { replace(value); setInstalling(false); }} /> : null}
      {upgrading ? <PluginReleaseDialog existing={upgrading} onClose={() => setUpgrading(undefined)} onSaved={(value) => { replace(value); setUpgrading(undefined); }} /> : null}
      {replacingToken ? <PluginTokenDialog plugin={replacingToken} onClose={() => setReplacingToken(undefined)} /> : null}
      {removing ? (
        <PluginUninstallDialog
          plugin={removing}
          onClose={() => setRemoving(undefined)}
          onRemoved={() => {
            setItems((current) => current.filter((item) => item.plugin_id !== removing.plugin_id));
            setRemoving(undefined);
          }}
        />
      ) : null}
    </>
  );
}

function PluginDetail({ label: detailLabel, value }: { label: string; value: ReactNode }) {
  return (
    <div className="grid gap-1">
      <dt className="text-muted-foreground">{detailLabel}</dt>
      <dd className="m-0 font-medium">{value}</dd>
    </div>
  );
}

function PluginTokenDialog({ plugin, onClose }: { plugin: PluginInstallation; onClose: () => void }) {
  const { t } = useI18n();
  const [token, setToken] = useState("");
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function save(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      await replacePluginGitHubToken(plugin.plugin_id, token);
      onClose();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={t("Replace token for {name}", { name: plugin.manifest.name })} onClose={onClose}>
      <form className="grid gap-5" onSubmit={save}>
        <FieldLabel label={t("GitHub token")}>
          <Input value={token} onChange={(event) => setToken(event.target.value)} type="password" autoComplete="off" required autoFocus />
        </FieldLabel>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
          <Button disabled={busy || token.trim() === ""} type="submit">{busy ? t("Saving...") : t("Replace")}</Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

function PluginReleaseDialog({ existing, onClose, onSaved }: {
  existing?: PluginInstallation;
  onClose: () => void;
  onSaved: (plugin: PluginInstallation) => void;
}) {
  const { t } = useI18n();
  const [repository, setRepository] = useState(existing?.repository ?? "");
  const [version, setVersion] = useState("");
  const [token, setToken] = useState("");
  const [candidate, setCandidate] = useState<PluginReleaseCandidate>();
  const [approved, setApproved] = useState<Set<string>>(new Set());
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  function changeSource(change: () => void) {
    change();
    setCandidate(undefined);
    setApproved(new Set());
  }

  async function inspect(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    try {
      const value = await inspectPluginRelease({
        repository,
        version,
        ...(token === "" ? {} : { github_token: token }),
      });
      if (existing !== undefined && value.manifest.id !== existing.plugin_id) {
        throw new Error("The release belongs to a different plugin.");
      }
      setCandidate(value);
      setApproved(new Set());
    } catch (cause) {
      setError(errorMessage(cause));
    } finally {
      setBusy(false);
    }
  }

  async function save() {
    if (candidate === undefined) return;
    const permissions = candidate.manifest.permissions.map((permission) => permission.name).sort();
    if (permissions.some((permission) => !approved.has(permission))) {
      setError("Approve every requested permission before continuing.");
      return;
    }
    setBusy(true);
    setError(undefined);
    try {
      const input = {
        version: candidate.manifest.version,
        approved_permissions: permissions,
        ...(token === "" ? {} : { github_token: token }),
      };
      const saved = existing === undefined
        ? await installPlugin({ repository: candidate.repository, ...input })
        : await upgradePlugin(existing.plugin_id, input);
      onSaved(saved);
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={existing === undefined ? t("Install plugin") : t("Upgrade {name}", { name: existing.manifest.name })} onClose={onClose} width="wide">
      <form className="grid gap-4" onSubmit={inspect}>
        <FieldLabel label={t("GitHub repository")}>
          <Input value={repository} onChange={(event) => changeSource(() => setRepository(event.target.value))} disabled={existing !== undefined} placeholder="https://github.com/owner/repository" required />
        </FieldLabel>
        <div className="grid grid-cols-2 gap-4 max-[700px]:grid-cols-1">
          <FieldLabel label={t("{field} (optional)", { field: t("Version") })}>
            <Input value={version} onChange={(event) => changeSource(() => setVersion(event.target.value))} placeholder={t("Latest stable release")} />
          </FieldLabel>
          <FieldLabel label={t("{field} (optional)", { field: t("GitHub token") })}>
            <Input value={token} onChange={(event) => changeSource(() => setToken(event.target.value))} type="password" autoComplete="off" placeholder={existing === undefined ? t("Public repository") : t("Use saved token")} />
          </FieldLabel>
        </div>
        <div className="flex justify-end">
          <Button variant="secondary" disabled={busy} type="submit"><RefreshCw size={16} />{busy ? t("Checking...") : t("Check release")}</Button>
        </div>
      </form>
      {candidate ? (
        <div className="mt-1 border-y border-border py-4">
          <div className="flex items-center justify-between gap-4">
            <div className="grid min-w-0 gap-0.5"><strong className="font-semibold">{candidate.manifest.name}</strong><small className="[overflow-wrap:anywhere] text-muted-foreground">{candidate.manifest.id}</small></div>
            <span className="shrink-0 text-sm">v{candidate.manifest.version}</span>
          </div>
          <div className="mt-3.5 grid gap-2" aria-label={t("Requested permissions")}>
            {candidate.manifest.permissions.length === 0 ? <p className="m-0 text-sm text-muted-foreground">{t("No kernel permissions requested.")}</p> : null}
            {candidate.manifest.permissions.map((permission) => (
              <PermissionOption
                key={permission.name}
                name={permission.name}
                reason={permission.reason}
                checked={approved.has(permission.name)}
                onCheckedChange={(checked) => setApproved((current) => {
                  const next = new Set(current);
                  if (checked) next.add(permission.name); else next.delete(permission.name);
                  return next;
                })}
              />
            ))}
          </div>
        </div>
      ) : null}
      <FormError message={error !== undefined ? t(error) : undefined} />
      <DialogFooter>
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button disabled={busy || candidate === undefined} onClick={save} type="button">
          {busy ? t("Saving...") : existing === undefined ? t("Install") : t("Upgrade")}
        </Button>
      </DialogFooter>
    </Modal>
  );
}

function PermissionOption({ name, reason, checked, onCheckedChange }: {
  name: string;
  reason: string;
  checked: boolean;
  onCheckedChange: (checked: boolean) => void;
}) {
  const { t } = useI18n();
  const id = useId();
  const presentation = pluginPermissionPresentation(name, reason);
  return (
    <label className="flex cursor-pointer items-start gap-2.5 rounded-sm border border-border bg-muted/45 px-3 py-2.5" htmlFor={id}>
      <Checkbox id={id} className="mt-0.5" checked={checked} onCheckedChange={(value) => onCheckedChange(value === true)} />
      <span className="grid min-w-0 gap-0.5">
        <strong className="[overflow-wrap:anywhere] text-sm font-semibold">{t(presentation.title)}</strong>
        <small className="[overflow-wrap:anywhere] text-xs text-muted-foreground">{t(presentation.description)}</small>
        <code className="[overflow-wrap:anywhere] text-xs text-muted-foreground/80">{name}</code>
      </span>
    </label>
  );
}

function FieldLabel({ label, children }: { label: string; children: ReactNode }) {
  return <label className="grid gap-1.5"><span className="text-sm font-semibold text-foreground/80">{label}</span>{children}</label>;
}

function PluginUninstallDialog({ plugin, onClose, onRemoved }: {
  plugin: PluginInstallation;
  onClose: () => void;
  onRemoved: () => void;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function remove() {
    setBusy(true);
    setError(undefined);
    try {
      await uninstallPlugin(plugin.plugin_id);
      onRemoved();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={t("Uninstall plugin")} onClose={onClose}>
      <p className="m-0 [overflow-wrap:anywhere] border-l-[3px] border-destructive bg-destructive/10 p-3">{plugin.manifest.name}</p>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <DialogFooter>
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button variant="destructive" disabled={busy} onClick={remove} type="button">{busy ? t("Uninstalling...") : t("Uninstall")}</Button>
      </DialogFooter>
    </Modal>
  );
}

function Status({ value, tone }: { value: string; tone: "ok" | "warning" | "error" | "muted" }) {
  const badgeTone = { ok: "success", warning: "warning", error: "danger", muted: "muted" } as const;
  return <StatusBadge tone={badgeTone[tone]}>{value}</StatusBadge>;
}

function IconAction({ label, description, danger = false, onClick, children }: {
  label: string;
  description: string;
  danger?: boolean;
  onClick: () => void;
  children: ReactNode;
}) {
  return (
    <Tooltip>
      <TooltipTrigger asChild>
        <Button
          className={cn(
            "ml-0.5 text-muted-foreground",
            danger && "text-destructive hover:bg-destructive/10 hover:text-destructive",
          )}
          variant="ghost"
          size="icon"
          aria-label={label}
          onClick={onClick}
          type="button"
        >
          {children}
        </Button>
      </TooltipTrigger>
      <TooltipContent>{description}</TooltipContent>
    </Tooltip>
  );
}

function installationStateTone(value: PluginInstallation["state"]): "ok" | "warning" | "error" {
  if (value === "active") return "ok";
  if (value === "failed") return "error";
  return "warning";
}

function label(value: string) {
  return value.charAt(0).toUpperCase() + value.slice(1);
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  if (cause instanceof Error) return cause.message;
  return "The request could not be completed.";
}
