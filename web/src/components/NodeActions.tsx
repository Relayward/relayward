import { type FormEvent, useId, useState } from "react";

import { APIError, createNode, updateNode, type Node, type NodeInput } from "../api";
import { useI18n } from "../i18n";
import { Field, FormError } from "./AuthScreen";
import { Modal } from "./Modal";
import { Button } from "./ui/button";
import { Checkbox } from "./ui/checkbox";
import { DialogFooter } from "./ui/dialog";

export function NodeEditorDialog({ value, onClose, onSaved }: {
  value?: Node;
  onClose: () => void;
  onSaved: (node: Node) => void;
}) {
  const { t } = useI18n();
  const enabledID = useId();
  const [name, setName] = useState(value?.name ?? "");
  const [enabled, setEnabled] = useState(value?.enabled ?? true);
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function submit(event: FormEvent) {
    event.preventDefault();
    setBusy(true);
    setError(undefined);
    const input: NodeInput = { name, enabled };
    try {
      onSaved(value ? await updateNode(value.id, input) : await createNode(input));
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={value ? t("Edit node") : t("Add node")} onClose={onClose}>
      <form className="grid gap-5" onSubmit={submit}>
        <div className="grid gap-4">
          <Field label={t("Name")} value={name} onChange={setName} autoFocus />
          <label className="flex min-h-8 cursor-pointer items-center gap-2 text-sm font-semibold text-foreground/80" htmlFor={enabledID}>
            <Checkbox id={enabledID} checked={enabled} onCheckedChange={(checked) => setEnabled(checked === true)} />
            <span>{t("Enabled")}</span>
          </label>
        </div>
        <FormError message={error !== undefined ? t(error) : undefined} />
        <DialogFooter>
          <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
          <Button disabled={busy} type="submit">{busy ? t("Saving...") : value ? t("Save") : t("Add node")}</Button>
        </DialogFooter>
      </form>
    </Modal>
  );
}

export function ConfirmNodeAction({ title, name, action, onClose, onConfirm }: {
  title: string;
  name: string;
  action: string;
  onClose: () => void;
  onConfirm: () => Promise<void>;
}) {
  const { t } = useI18n();
  const [busy, setBusy] = useState(false);
  const [error, setError] = useState<string>();

  async function confirm() {
    setBusy(true);
    setError(undefined);
    try {
      await onConfirm();
    } catch (cause) {
      setError(errorMessage(cause));
      setBusy(false);
    }
  }

  return (
    <Modal title={title} onClose={onClose}>
      <p className="m-0 [overflow-wrap:anywhere] border-l-[3px] border-destructive bg-destructive/10 p-3">{name}</p>
      <FormError message={error !== undefined ? t(error) : undefined} />
      <DialogFooter>
        <Button variant="ghost" onClick={onClose} type="button">{t("Cancel")}</Button>
        <Button variant="destructive" disabled={busy} onClick={confirm} type="button">{busy ? t("Working...") : action}</Button>
      </DialogFooter>
    </Modal>
  );
}

function errorMessage(cause: unknown): string {
  if (cause instanceof APIError) return cause.message;
  return "The request could not be completed.";
}
