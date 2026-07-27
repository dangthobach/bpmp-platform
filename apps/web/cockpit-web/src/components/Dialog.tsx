import { X } from "lucide-react";
import type { PropsWithChildren, ReactNode } from "react";
import { IconButton } from "./IconButton";

export function Dialog({
  open,
  title,
  description,
  onClose,
  footer,
  size = "default",
  children,
}: PropsWithChildren<{
  open: boolean;
  title: string;
  description?: string;
  onClose: () => void;
  footer?: ReactNode;
  size?: "default" | "wide";
}>) {
  if (!open) return null;
  return (
    <div className="dialog-backdrop" role="presentation" onMouseDown={onClose}>
      <section
        className={`dialog dialog--${size}`}
        role="dialog"
        aria-modal="true"
        aria-labelledby="dialog-title"
        onMouseDown={(event) => event.stopPropagation()}
      >
        <header className="dialog__header">
          <div>
            <h2 id="dialog-title">{title}</h2>
            {description ? <p>{description}</p> : null}
          </div>
          <IconButton icon={X} label="Close" onClick={onClose} />
        </header>
        <div className="dialog__content">{children}</div>
        {footer ? <footer className="dialog__footer">{footer}</footer> : null}
      </section>
    </div>
  );
}
