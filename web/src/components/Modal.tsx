import { useRef, type ReactNode } from "react";

import { useI18n } from "../i18n";
import { Dialog, DialogContent, DialogHeader, DialogTitle } from "./ui/dialog";

interface ModalProps {
  title: string;
  children: ReactNode;
  onClose: () => void;
  dismissible?: boolean;
  width?: "normal" | "wide";
}

export function Modal({ title, children, onClose, dismissible = true, width = "normal" }: ModalProps) {
  const { t } = useI18n();
  const trigger = useRef<HTMLElement | null>(
    typeof document !== "undefined" && document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );

  return (
    <Dialog open onOpenChange={(open) => { if (!open && dismissible) onClose(); }}>
      <DialogContent
        className={width === "wide" ? "sm:max-w-3xl" : undefined}
        closeLabel={t("Close")}
        showCloseButton={dismissible}
        onEscapeKeyDown={(event) => { if (!dismissible) event.preventDefault(); }}
        onPointerDownOutside={(event) => { if (!dismissible) event.preventDefault(); }}
        onCloseAutoFocus={(event) => {
          event.preventDefault();
          if (trigger.current?.isConnected) trigger.current.focus();
        }}
      >
        <DialogHeader><DialogTitle>{title}</DialogTitle></DialogHeader>
        {children}
      </DialogContent>
    </Dialog>
  );
}
