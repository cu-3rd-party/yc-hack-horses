import * as Dialog from '@radix-ui/react-dialog';
import { ArrowLeft, Check, ChevronLeft, ChevronRight, CircleAlert, X } from 'lucide-react';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useRef,
  useState,
  type ReactNode,
} from 'react';
import { Button } from './button';
export function Heading({
  title,
  description,
  action,
}: {
  title: string;
  description?: string;
  action?: ReactNode;
}) {
  return (
    <header className="heading">
      <div>
        <h1>{title}</h1>
        {description && <p>{description}</p>}
      </div>
      {action}
    </header>
  );
}
export function Back({ children, onClick }: { children: ReactNode; onClick: () => void }) {
  return (
    <button className="back" onClick={onClick}>
      <ArrowLeft size={16} />
      {children}
    </button>
  );
}
export function Modal({
  title,
  description,
  children,
  onClose,
  busy = false,
  wide = false,
  fullscreen = false,
}: {
  title: string;
  description?: string;
  children: ReactNode;
  onClose: () => void;
  busy?: boolean;
  wide?: boolean;
  fullscreen?: boolean;
}) {
  const opener = useRef(
    document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );
  return (
    <Dialog.Root
      open
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="veil" />
        <Dialog.Content
          onCloseAutoFocus={(event) => {
            if (opener.current?.isConnected) {
              event.preventDefault();
              opener.current.focus({ preventScroll: true });
            }
          }}
          onEscapeKeyDown={(event) => {
            const popover = document.querySelector<HTMLElement>('[role="dialog"] :popover-open');
            if (popover) {
              event.preventDefault();
              popover.hidePopover();
              document
                .querySelector<HTMLButtonElement>(
                  '[popovertarget="' + CSS.escape(popover.id) + '"]',
                )
                ?.focus();
            }
          }}
          className={'dialog' + (fullscreen ? ' fullscreen' : wide ? ' wide' : '')}
          {...(!description ? { 'aria-describedby': undefined } : {})}
        >
          <div className="dialog-head">
            <Dialog.Title>{title}</Dialog.Title>
            <Button
              variant="ghost"
              size="icon"
              disabled={busy}
              aria-label="Закрыть"
              onClick={onClose}
            >
              <X size={18} />
            </Button>
          </div>
          {description && (
            <Dialog.Description className="dialog-description">{description}</Dialog.Description>
          )}
          {children}
        </Dialog.Content>
      </Dialog.Portal>
    </Dialog.Root>
  );
}
export function Pager({
  index,
  total,
  onChange,
  label,
}: {
  index: number;
  total: number;
  onChange: (i: number) => void;
  label: string;
}) {
  return (
    <div className="pager" role="group" aria-label={label}>
      <button
        aria-label={label + ' — назад'}
        disabled={index === 0}
        onClick={() => onChange(index - 1)}
      >
        <ChevronLeft size={16} />
      </button>
      <span>
        {label} {index + 1}/{total}
      </span>
      <button
        aria-label={label + ' — вперёд'}
        disabled={index >= total - 1}
        onClick={() => onChange(index + 1)}
      >
        <ChevronRight size={16} />
      </button>
    </div>
  );
}
export function Toggle({
  on,
  onChange,
  label,
  disabled,
}: {
  on: boolean;
  onChange: () => void;
  label: string;
  disabled?: boolean;
}) {
  return (
    <button
      className="toggle-label"
      role="switch"
      aria-checked={on}
      disabled={disabled}
      onClick={onChange}
    >
      <span className={'toggle' + (on ? ' on' : '')}>
        <i />
      </span>
      {label}
    </button>
  );
}
const ToastContext = createContext<(text: string, error?: boolean) => void>(() => {});
export function Toasts({ children }: { children: ReactNode }) {
  const [toast, setToast] = useState<{ text: string; error: boolean } | null>(null);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const notify = useCallback((text: string, error = false) => {
    clearTimeout(timer.current);
    setToast({ text, error });
    timer.current = setTimeout(() => setToast(null), 5000);
  }, []);
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <ToastContext.Provider value={notify}>
      {children}
      {toast && (
        <div
          className={'toast' + (toast.error ? ' error' : '')}
          role={toast.error ? 'alert' : 'status'}
        >
          {toast.error ? <CircleAlert size={18} /> : <Check size={18} />}
          <span>{toast.text}</span>
          <button aria-label="Закрыть уведомление" onClick={() => setToast(null)}>
            <X size={16} />
          </button>
        </div>
      )}
    </ToastContext.Provider>
  );
}
export const useNotice = () => useContext(ToastContext);
