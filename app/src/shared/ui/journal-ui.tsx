import * as Dialog from '@radix-ui/react-dialog';
import { ArrowLeft, Check, ChevronLeft, ChevronRight, CircleAlert, X } from 'lucide-react';
import {
  createContext,
  useCallback,
  useContext,
  useEffect,
  useId,
  useLayoutEffect,
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
interface ModalProps {
  title: string;
  description?: string;
  children: ReactNode;
  onClose: () => void;
  busy?: boolean;
  wide?: boolean;
  fullscreen?: boolean;
}
interface OpenDialog {
  id: string;
  props: ModalProps;
  open: boolean;
}
const DialogContext = createContext<(id: string, props: ModalProps | null) => void>(() => {});

// A save can remove the whole comparison page. Keep its dialog mounted in the
// host until Radix finishes the exit animation and releases the focus trap.
export function DialogHost({ children }: { children: ReactNode }) {
  const [dialog, setDialog] = useState<OpenDialog | null>(null);
  const update = useCallback((id: string, props: ModalProps | null) => {
    setDialog((current) => {
      if (props) return { id, props, open: true };
      return current?.id === id ? { ...current, open: false } : current;
    });
  }, []);
  const removeClosed = useCallback(
    () => setDialog((current) => (current?.open ? current : null)),
    [],
  );
  return (
    <DialogContext.Provider value={update}>
      {children}
      {dialog && (
        <DialogSurface
          {...dialog.props}
          id={dialog.id}
          open={dialog.open}
          afterClose={removeClosed}
        />
      )}
    </DialogContext.Provider>
  );
}
export function Modal(props: ModalProps) {
  const id = useId();
  const update = useContext(DialogContext);
  useLayoutEffect(() => {
    update(id, props);
  }, [id, props, update]);
  useLayoutEffect(() => () => update(id, null), [id, update]);
  return null;
}
function DialogSurface({
  title,
  description,
  children,
  onClose,
  busy,
  wide,
  fullscreen,
  id,
  open,
  afterClose,
}: ModalProps & {
  id: string;
  open: boolean;
  afterClose: () => void;
}) {
  const opener = useRef(
    document.activeElement instanceof HTMLElement ? document.activeElement : null,
  );
  const surface = useRef<HTMLDivElement>(null);
  const previousId = useRef(id);
  useEffect(() => {
    if (!open) return;
    // Let Chromium dismiss its open picker before Radix handles Escape on document.
    const dismissPicker = (event: KeyboardEvent) => {
      if (event.key === 'Escape' && surface.current?.querySelector('select:open'))
        event.stopPropagation();
    };
    window.addEventListener('keydown', dismissPicker, true);
    return () => window.removeEventListener('keydown', dismissPicker, true);
  }, [open]);
  useLayoutEffect(() => {
    if (previousId.current !== id) {
      previousId.current = id;
      const first =
        surface.current?.querySelector<HTMLElement>('[data-dialog-focus]') ??
        surface.current?.querySelector<HTMLButtonElement>('.dialog-close');
      first?.focus();
    }
  }, [id]);
  return (
    <Dialog.Root
      open={open}
      onOpenChange={(open) => {
        if (!open && !busy) onClose();
      }}
    >
      <Dialog.Portal>
        <Dialog.Overlay className="veil" />
        <Dialog.Content
          ref={surface}
          inert={!open}
          aria-hidden={!open || undefined}
          onOpenAutoFocus={(event) => {
            const field = surface.current?.querySelector<HTMLElement>('[data-dialog-focus]');
            if (field) {
              event.preventDefault();
              field.focus();
            }
          }}
          onCloseAutoFocus={(event) => {
            afterClose();
            if (
              opener.current?.isConnected &&
              !document.querySelector('[role="dialog"][data-state="open"]')
            ) {
              event.preventDefault();
              opener.current.focus({ preventScroll: true });
            }
          }}
          className={'dialog' + (fullscreen ? ' fullscreen' : wide ? ' wide' : '')}
          {...(!description ? { 'aria-describedby': undefined } : {})}
        >
          <div className="dialog-head">
            <Dialog.Title>{title}</Dialog.Title>
            <Button
              className="dialog-close"
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
  const [toast, setToast] = useState<{
    id: number;
    text: string;
    error: boolean;
    closing: boolean;
  } | null>(null);
  const sequence = useRef(0);
  const dismiss = useCallback(() => {
    setToast((old) => old && { ...old, closing: true });
  }, []);
  const timer = useRef<ReturnType<typeof setTimeout> | undefined>(undefined);
  const notify = useCallback(
    (text: string, error = false) => {
      clearTimeout(timer.current);
      setToast({ id: ++sequence.current, text, error, closing: false });
      timer.current = setTimeout(dismiss, 5000);
    },
    [dismiss],
  );
  useEffect(() => () => clearTimeout(timer.current), []);
  return (
    <ToastContext.Provider value={notify}>
      {children}
      {toast && (
        <div
          key={toast.id}
          data-state={toast.closing ? 'closed' : 'open'}
          onAnimationEnd={(event) => {
            if (event.target === event.currentTarget && toast.closing)
              setToast((old) => (old?.id === toast.id ? null : old));
          }}
          className={'toast' + (toast.error ? ' error' : '')}
          role={toast.error ? 'alert' : 'status'}
        >
          {toast.error ? <CircleAlert size={18} /> : <Check size={18} />}
          <span>{toast.text}</span>
          <button aria-label="Закрыть уведомление" onClick={dismiss}>
            <X size={16} />
          </button>
        </div>
      )}
    </ToastContext.Provider>
  );
}
export const useNotice = () => useContext(ToastContext);
