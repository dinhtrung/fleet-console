/** Dialog — the shadcn Base UI dialog, styled to the board.
 *
 *  The primitive owns the modal contract (focus trap, Escape, scroll lock, focus return); this file
 *  only supplies the ruled, square panel from `DESIGN.md` and the destructive confirmation the
 *  screens share. Colours resolve through the shadcn semantic variables bridged in `src/index.css`.
 */

import * as React from "react";
import { Dialog as DialogPrimitive } from "@base-ui/react/dialog";
import { cn } from "@/lib/utils";
import { Button } from "@/components/ui/button";
import { Input } from "@/components/ui/input";
import { XIcon } from "lucide-react";

function Dialog({ ...props }: DialogPrimitive.Root.Props) {
  return <DialogPrimitive.Root data-slot="dialog" {...props} />;
}

function DialogTrigger({ ...props }: DialogPrimitive.Trigger.Props) {
  return <DialogPrimitive.Trigger data-slot="dialog-trigger" {...props} />;
}

function DialogPortal({ ...props }: DialogPrimitive.Portal.Props) {
  return <DialogPrimitive.Portal data-slot="dialog-portal" {...props} />;
}

function DialogClose({ ...props }: DialogPrimitive.Close.Props) {
  return <DialogPrimitive.Close data-slot="dialog-close" {...props} />;
}

function DialogOverlay({ className, ...props }: DialogPrimitive.Backdrop.Props) {
  return (
    <DialogPrimitive.Backdrop
      data-slot="dialog-overlay"
      className={cn(
        "fixed inset-0 isolate z-50 bg-foreground/55 duration-100 data-open:animate-in data-open:fade-in-0 data-closed:animate-out data-closed:fade-out-0",
        className,
      )}
      {...props}
    />
  );
}

function DialogContent({
  className,
  children,
  showCloseButton = false,
  ...props
}: DialogPrimitive.Popup.Props & {
  showCloseButton?: boolean;
}) {
  return (
    <DialogPortal>
      <DialogOverlay />
      <DialogPrimitive.Popup
        data-slot="dialog-content"
        className={cn(
          "fixed top-1/2 left-1/2 z-50 grid w-full max-w-[calc(100%-2rem)] -translate-x-1/2 -translate-y-1/2 gap-3 border border-rule-strong bg-panel p-3 text-body text-ink shadow-overlay duration-100 outline-none sm:max-w-lg data-open:animate-in data-open:fade-in-0 data-open:zoom-in-95 data-closed:animate-out data-closed:fade-out-0 data-closed:zoom-out-95",
          className,
        )}
        {...props}
      >
        {children}
        {showCloseButton && (
          <DialogPrimitive.Close
            data-slot="dialog-close"
            render={<Button variant="quiet" className="absolute top-2 right-2" size="sm" />}
          >
            <XIcon />
            <span className="sr-only">Close</span>
          </DialogPrimitive.Close>
        )}
      </DialogPrimitive.Popup>
    </DialogPortal>
  );
}

function DialogHeader({ className, ...props }: React.ComponentProps<"div">) {
  return (
    <div
      data-slot="dialog-header"
      className={cn("-mx-3 -mt-3 flex flex-col gap-1 border-b border-rule px-3 py-2", className)}
      {...props}
    />
  );
}

function DialogFooter({
  className,
  showCloseButton = false,
  children,
  ...props
}: React.ComponentProps<"div"> & {
  showCloseButton?: boolean;
}) {
  return (
    <div
      data-slot="dialog-footer"
      className={cn(
        "-mx-3 -mb-3 flex flex-col-reverse gap-2 border-t border-rule px-3 py-2 sm:flex-row sm:justify-end",
        className,
      )}
      {...props}
    >
      {children}
      {showCloseButton && (
        <DialogPrimitive.Close render={<Button variant="outline" />}>Close</DialogPrimitive.Close>
      )}
    </div>
  );
}

function DialogTitle({ className, ...props }: DialogPrimitive.Title.Props) {
  return (
    <DialogPrimitive.Title
      data-slot="dialog-title"
      className={cn("text-lead font-semibold tracking-[-0.01em] text-ink", className)}
      {...props}
    />
  );
}

function DialogDescription({ className, ...props }: DialogPrimitive.Description.Props) {
  return (
    <DialogPrimitive.Description
      data-slot="dialog-description"
      className={cn("text-[0.6875rem] text-ink-muted", className)}
      {...props}
    />
  );
}

/** Confirmation for a destructive action.
 *
 *  The dangerous button is never the default: it is magenta, it is on the right, and where the
 *  action cannot be undone the operator types the target's name before it enables. */
export function ConfirmDialog({
  open,
  onClose,
  onConfirm,
  title,
  description,
  confirmLabel,
  requireTyped,
  typedLabel,
  busy = false,
  error,
}: {
  open: boolean;
  onClose: () => void;
  onConfirm: () => void;
  title: string;
  description: string;
  confirmLabel: string;
  requireTyped?: string;
  typedLabel?: string;
  busy?: boolean;
  error?: string;
}) {
  const [typed, setTyped] = React.useState("");
  const armed = !requireTyped || typed.trim() === requireTyped;
  // The typed confirmation is cleared where the dialog closes, not in an effect watching `open`.
  const close = () => {
    setTyped("");
    onClose();
  };

  return (
    <Dialog
      open={open}
      onOpenChange={(next) => {
        if (!next) close();
      }}
    >
      <DialogContent className="sm:max-w-sm">
        <DialogHeader>
          <DialogTitle>{title}</DialogTitle>
          <DialogDescription>{description}</DialogDescription>
        </DialogHeader>
        {requireTyped || error ? (
          <div className="flex flex-col gap-2">
            {requireTyped ? (
              <label className="flex flex-col gap-1">
                <span className="text-[0.6875rem] text-ink-muted">
                  {typedLabel ?? "Type the name to confirm"}
                </span>
                <Input
                  value={typed}
                  onChange={(event) => setTyped(event.target.value)}
                  placeholder={requireTyped}
                  className="min-h-[var(--density-control-h)] rounded-chip border-rule-strong bg-panel px-2 text-body"
                />
              </label>
            ) : null}
            {error ? <p className="text-[0.6875rem] font-semibold text-attention">{error}</p> : null}
          </div>
        ) : null}
        <DialogFooter>
          <Button variant="quiet" onClick={close}>
            Cancel
          </Button>
          <Button variant="attention" onClick={onConfirm} disabled={!armed} busy={busy}>
            {confirmLabel}
          </Button>
        </DialogFooter>
      </DialogContent>
    </Dialog>
  );
}

export {
  Dialog,
  DialogClose,
  DialogContent,
  DialogDescription,
  DialogFooter,
  DialogHeader,
  DialogOverlay,
  DialogPortal,
  DialogTitle,
  DialogTrigger,
};
