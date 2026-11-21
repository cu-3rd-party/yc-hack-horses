import { useState } from 'react';
import { FileImage, MapPin, Plus, LoaderCircle } from 'lucide-react';
import type { Notebook, Observation } from '../journal/types';
import { dateLabel, describeTraits, validName } from '../journal/types';
import { importImage } from '../journal/media';
import { useCommand } from '../journal/use-journal';
import { validCoordinates } from '../journal/api';
import { Back, Heading, Modal, useNotice } from '../shared/ui/journal-ui';
import { MarkedPhoto, Thumbnail } from '../shared/ui/photos';
import { Button } from '../shared/ui/button';

export function ObservationLog({ book }: { book: Notebook }) {
  const [manual, setManual] = useState(false),
    [unlinked, setUnlinked] = useState(false);
  const [detail, setDetail] = useState<Observation | null>(null),
    [image, setImage] = useState(0);
  if (manual) return <Manual book={book} close={() => setManual(false)} />;
  const list = book.observations
    .filter((o) => !unlinked || o.horseId === null)
    .sort((a, b) => b.date.localeCompare(a.date));
  return (
    <>
      <Heading
        title="Журнал наблюдений"
        description="Полевые записи и результаты распознавания в хронологическом порядке."
        action={
          <Button onClick={() => setManual(true)}>
            <Plus size={16} />
            Ручная запись
          </Button>
        }
      />
      <div className="filters">
        <label className="check">
          <input
            type="checkbox"
            checked={unlinked}
            onChange={(e) => setUnlinked(e.target.checked)}
          />
          Только без привязки
        </label>
        <small>Записей · {list.length}</small>
      </div>
      <div className="log-list">
        {list.map((o) => (
          <button
            key={o.id}
            className="log-row"
            onClick={() => {
              setDetail(o);
              setImage(0);
            }}
          >
            {o.photos[0] ? (
              <Thumbnail photo={o.photos[0]} />
            ) : (
              <span className="file-tile">
                <FileImage />
              </span>
            )}
            <span className="row-title">
              <strong>{o.name}</strong>
              <small>{book.horses.find((h) => h.id === o.horseId)?.name ?? 'Без привязки'}</small>
            </span>
            <span className="row-date">{dateLabel(o.date)}</span>
            <span className="pill">{book.pending.includes(o.id) ? 'В очереди' : 'Сохранено'}</span>
          </button>
        ))}
        {!list.length && <p className="empty">Наблюдений пока нет.</p>}
      </div>
      {detail && (
        <Modal wide title={detail.name} onClose={() => setDetail(null)}>
          {detail.photos[image] && (
            <div className="record-photo">
              <MarkedPhoto photo={detail.photos[image]} />
            </div>
          )}
          {detail.photos.length > 1 && (
            <div className="thumb-strip">
              {detail.photos.map((p, i) => (
                <button key={p.id} aria-label={p.name} onClick={() => setImage(i)}>
                  <Thumbnail photo={p} />
                </button>
              ))}
            </div>
          )}
          <dl className="facts">
            <dt>Дата</dt>
            <dd>{dateLabel(detail.date)}</dd>
            <dt>Особь</dt>
            <dd>{book.horses.find((h) => h.id === detail.horseId)?.name ?? 'Без привязки'}</dd>
            <dt>Координаты</dt>
            <dd>{detail.coordinates || 'Не указаны'}</dd>
            <dt>Признаки</dt>
            <dd>{describeTraits(detail.traits) || 'Не указаны'}</dd>
            {detail.confidence !== undefined && (
              <>
                <dt>Достоверность</dt>
                <dd>{Math.round(detail.confidence * 100)}%</dd>
              </>
            )}
          </dl>
        </Modal>
      )}
    </>
  );
}
function Manual({ book, close }: { book: Notebook; close: () => void }) {
  const [name, setName] = useState(''),
    [horse, setHorse] = useState(''),
    [coords, setCoords] = useState('');
  const [entries, setEntries] = useState([
    { id: crypto.randomUUID(), label: 'Грива', value: '', confidence: '' },
  ]);
  const [confidence, setConfidence] = useState('0.9'),
    [files, setFiles] = useState<File[]>([]);
  const [busy, setBusy] = useState(false);
  const action = useCommand('/manual'),
    notice = useNotice();
  const valid =
    validName(name) &&
    (!coords || validCoordinates(coords)) &&
    (files.length > 0 || entries.some((e) => e.value.trim())) &&
    entries.every((e) => !e.value.trim() || !!e.label.trim()) &&
    confidence !== '' &&
    +confidence >= 0 &&
    +confidence <= 1;
  return (
    <>
      <Back onClick={close}>Назад в журнал</Back>
      <Heading
        title="Ручная запись"
        description="Зафиксируйте признаки и место встречи с животным."
      />
      <form
        className="panel entry-form"
        onSubmit={async (e) => {
          e.preventDefault();
          if (!valid || busy) return;
          setBusy(true);
          try {
            await action.mutateAsync({
              name,
              horseId: horse || null,
              coordinates: coords,
              traits: entries
                .filter((e) => e.value.trim())
                .map((e) => ({
                  label: e.label.trim(),
                  value: e.value.trim(),
                  confidence: e.confidence === '' ? undefined : Number(e.confidence),
                })),
              confidence: +confidence,
              files: await Promise.all(files.map(importImage)),
            });
            notice('Запись сохранена в журнале.');
            close();
          } catch (error) {
            notice((error as Error).message, true);
          } finally {
            setBusy(false);
          }
        }}
      >
        <fieldset disabled={busy}>
          <label className="field">
            Название наблюдения
            <input value={name} onChange={(e) => setName(e.target.value)} required />
          </label>
          <label className="field">
            Особь
            <select value={horse} onChange={(e) => setHorse(e.target.value)}>
              <option value="">Без привязки</option>
              {book.horses.map((h) => (
                <option key={h.id} value={h.id}>
                  {h.name}
                </option>
              ))}
            </select>
          </label>
          <label className="field">
            Фотографии
            <input
              type="file"
              accept="image/*,.heic,.heif,.tif,.tiff,.dng"
              multiple
              onChange={(e) => setFiles(Array.from(e.target.files ?? []))}
            />
          </label>
          <label className="field">
            Координаты
            <input
              placeholder="47.78, 106.01"
              value={coords}
              onChange={(e) => setCoords(e.target.value)}
            />
          </label>
          {coords && !validCoordinates(coords) && (
            <p className="field-error">Введите широту и долготу через запятую.</p>
          )}
          <button className="text-link" type="button" onClick={() => setCoords('47.78, 106.01')}>
            <MapPin size={15} />
            Подставить демокоординаты
          </button>
          <fieldset className="manual-traits">
            <legend>Признаки</legend>
            {entries.map((entry) => (
              <div className="trait-entry" key={entry.id}>
                {(['label', 'value', 'confidence'] as const).map((key, i) => (
                  <label className="field" key={key}>
                    {['Название признака', 'Значение признака', 'Уверенность (0–1)'][i]}
                    <input
                      type={key === 'confidence' ? 'number' : 'text'}
                      min={key === 'confidence' ? 0 : undefined}
                      max={key === 'confidence' ? 1 : undefined}
                      step={key === 'confidence' ? 0.01 : undefined}
                      value={entry[key]}
                      onChange={(e) =>
                        setEntries((current) =>
                          current.map((item) =>
                            item.id === entry.id ? { ...item, [key]: e.target.value } : item,
                          ),
                        )
                      }
                    />
                  </label>
                ))}
                <button
                  type="button"
                  className="text-link"
                  aria-label="Удалить признак"
                  onClick={() =>
                    setEntries((current) => current.filter((item) => item.id !== entry.id))
                  }
                >
                  ×
                </button>
              </div>
            ))}
            <button
              className="text-link"
              type="button"
              onClick={() =>
                setEntries((current) => [
                  ...current,
                  { id: crypto.randomUUID(), label: '', value: '', confidence: '' },
                ])
              }
            >
              <Plus size={15} />
              Добавить признак
            </button>
          </fieldset>
          <label className="field">
            Достоверность (0–1)
            <input
              type="number"
              min="0"
              max="1"
              step="0.01"
              value={confidence}
              onChange={(e) => setConfidence(e.target.value)}
            />
          </label>
          <div className="dialog-actions">
            <Button type="button" variant="outline" onClick={close}>
              Отменить
            </Button>
            <Button disabled={!valid || busy}>
              {busy && <LoaderCircle className="spin" size={15} />}Сохранить запись
            </Button>
          </div>
        </fieldset>
      </form>
    </>
  );
}
