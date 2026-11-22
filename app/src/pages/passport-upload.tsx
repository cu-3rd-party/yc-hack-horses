import { useState, type FormEvent } from 'react';
import { FileText } from 'lucide-react';
import { MAX_PASSPORT_BYTES } from '../../../packages/contracts/src';
import { useCommand } from '../journal/use-journal';
import type { Horse } from '../journal/types';
import { Button } from '../shared/ui/button';
import { FileDropzone } from '../shared/ui/file-dropzone';
import { Modal, useNotice } from '../shared/ui/journal-ui';

export function PassportUpload({ horse, close }: { horse: Horse; close: () => void }) {
  const replacing = !!horse.passportSha256;
  const [file, setFile] = useState<File | null>(null);
  const [error, setError] = useState('');
  const [busy, setBusy] = useState(false);
  const upload = useCommand('/passport');
  const notice = useNotice();

  function pick(files: FileList | null) {
    if (!files?.length) return;
    const next = files[0]!;
    const problem = files.length !== 1 ? 'Выберите один PDF-файл.'
      : next.size > MAX_PASSPORT_BYTES ? 'Размер файла превышает 50 МБ.'
      : next.size === 0 ? 'Выбранный файл пуст.' : '';
    setFile(problem ? null : next);
    setError(problem);
  }

  async function save(event: FormEvent) {
    event.preventDefault();
    if (!file || busy) return;
    setBusy(true);
    setError('');
    try {
      if (await file.slice(0, 5).text() !== '%PDF-') {
        setFile(null);
        setError('Выберите документ в формате PDF.');
        return;
      }
      await upload.mutateAsync({ horseId: horse.id, file });
      notice(replacing ? 'Паспорт обновлён.' : 'Паспорт загружен.');
      close();
    } catch (cause) {
      setError(cause instanceof Error ? cause.message : 'Не удалось сохранить паспорт. Повторите попытку.');
    } finally {
      setBusy(false);
    }
  }

  return (
    <Modal
      title={replacing ? 'Обновить паспорт?' : 'Загрузить паспорт'}
      description={replacing
        ? 'Новый PDF заменит текущий паспорт особи «' + horse.name + '».'
        : 'Прикрепите PDF к карточке особи «' + horse.name + '».'}
      busy={busy} onClose={close}
    >
      <form onSubmit={save}>
        <FileDropzone label="Файл паспорта" accept=".pdf,application/pdf" onFiles={pick}
          disabled={busy} busyLabel={busy ? 'Сохраняем паспорт…' : undefined}
          invalid={!!error} describedBy={error ? 'passport-error' : 'passport-hint'} />
        <p id="passport-hint" className="file-hint">Один PDF-файл до 50 МБ.</p>
        {file && <div className="file-state" role="status"><FileText size={16} /><span>{file.name}</span></div>}
        {error && <p id="passport-error" className="file-error" role="alert">{error}</p>}
        <div className="dialog-actions">
          <Button type="button" size="lg" variant="outline" disabled={busy} onClick={close}>Отменить</Button>
          <Button type="submit" size="lg" disabled={!file || busy}>{replacing ? 'Обновить' : 'Загрузить'}</Button>
        </div>
      </form>
    </Modal>
  );
}
