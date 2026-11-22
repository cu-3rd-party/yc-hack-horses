import { useRef, useState } from 'react';
import { LoaderCircle } from 'lucide-react';

interface FileDropzoneProps {
  label: string;
  accept: string;
  multiple?: boolean;
  disabled?: boolean;
  busyLabel?: string;
  invalid?: boolean;
  describedBy: string;
  onFiles: (files: FileList | null) => void;
}

export function FileDropzone({
  label, accept, multiple, disabled, busyLabel, invalid, describedBy, onFiles,
}: FileDropzoneProps) {
  const picker = useRef<HTMLInputElement>(null);
  const [drag, setDrag] = useState(false);
  return (
    <>
      <input
        ref={picker} type="file" className="sr-only" tabIndex={-1}
        aria-label={label} accept={accept} multiple={multiple} disabled={disabled}
        onChange={event => {
          onFiles(event.target.files);
          event.target.value = '';
        }}
      />
      <button
        type="button" className={'dropzone' + (drag ? ' drag' : '') + (invalid ? ' invalid' : '')}
        aria-busy={!!busyLabel} aria-describedby={describedBy} disabled={disabled}
        onClick={() => picker.current?.click()}
        onDragOver={event => {
          event.preventDefault();
          if (!disabled) setDrag(true);
        }}
        onDragLeave={event => {
          if (!event.currentTarget.contains(event.relatedTarget as Node)) setDrag(false);
        }}
        onDrop={event => {
          event.preventDefault();
          setDrag(false);
          if (!disabled) onFiles(event.dataTransfer.files);
        }}
      >
        {busyLabel && <LoaderCircle className="spin" size={18} />}
        <span>{busyLabel || <><strong>Выберите файлы</strong> или перетащите их сюда</>}</span>
      </button>
    </>
  );
}
