import { type ReactNode, useEffect, useId } from "react";
import { X } from "lucide-react";

interface ModalProps {
  title: string;
  children: ReactNode;
  onClose: () => void;
  dismissible?: boolean;
  width?: "normal" | "wide";
}

export function Modal({ title, children, onClose, dismissible = true, width = "normal" }: ModalProps) {
  const titleID = useId();

  useEffect(() => {
    if (!dismissible) return;
    function closeOnEscape(event: KeyboardEvent) {
      if (event.key === "Escape") onClose();
    }
    window.addEventListener("keydown", closeOnEscape);
    return () => window.removeEventListener("keydown", closeOnEscape);
  }, [dismissible, onClose]);

  return (
    <div className="modal-backdrop" role="presentation" onMouseDown={(event) => {
      if (dismissible && event.target === event.currentTarget) onClose();
    }}>
      <section className={`modal${width === "wide" ? " modal--wide" : ""}`} role="dialog" aria-modal="true" aria-labelledby={titleID}>
        <div className="modal-heading">
          <h2 id={titleID}>{title}</h2>
          {dismissible ? (
            <button className="icon-button" onClick={onClose} aria-label="Close" title="Close" type="button"><X size={18} /></button>
          ) : null}
        </div>
        {children}
      </section>
    </div>
  );
}
