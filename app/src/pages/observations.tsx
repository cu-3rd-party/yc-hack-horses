import { useState } from 'react';
import { MapPin, Plus, LoaderCircle, Search, Expand, Unlink } from 'lucide-react';
import type { Notebook, Observation } from '../journal/types';
import { dateLabel, describeTraits, validName } from '../journal/types';
import { importImage } from '../journal/media';
import { useCommand } from '../journal/use-journal';
import { validCoordinates } from '../journal/api';
import { Back, Heading, Modal, useNotice } from '../shared/ui/journal-ui';
import { MarkedPhoto, PhotoTile, Thumbnail } from '../shared/ui/photos';
import { Button } from '../shared/ui/button';
import { Select } from '../shared/ui/select';

import { FilterBar } from '../shared/ui/filter-bar';
import { Carousel } from './gallery';
export function ObservationLog({
  book,
  initialId,
  onDismiss,
}: {
  book: Notebook;
  initialId?: string;
  onDismiss: () => void;
}) {
  const [filters, setFilters] = useState({ search: '', unlinked: false });
  const { search, unlinked } = filters;
  const [fullscreen, setFullscreen] = useState(false);
  const [manual, setManual] = useState(false);
  const [detail, setDetail] = useState<Observation | null>(
      () => book.observations.find((o) => o.id === initialId) ?? null,
    ),
    [image, setImage] = useState(0);
  if (manual) return <Manual book={book} close={() => setManual(false)} />;
  const list = book.observations
    .filter((o) => !unlinked || o.horseId === null)
    .filter((o) =>
      [
        o.name,
        ...o.photos.map((p) => p.name),
        book.horses.find((h) => h.id === o.horseId)?.name,
      ].some((name) =>
        name?.toLocaleLowerCase('ru').includes(search.trim().toLocaleLowerCase('ru')),
      ),
    )
    .sort((a, b) => b.date.localeCompare(a.date));
  return (
    <>
      <Heading
        title="Журнал наблюдений"
        description="Полевые записи и результаты распознавания в хронологическом порядке."
        action={
          <Button size="lg" onClick={() => setManual(true)}>
            <Plus size={16} />
            Ручная запись
          </Button>
        }
      />
      <FilterBar value={filters} onChange={setFilters}>
        {(draft, edit) => (
          <>
            <label className="search">
              <Search size={16} />
              <input
                aria-label="Поиск наблюдений"
                placeholder="Найти запись, фото или особь"
                value={draft.search}
                onChange={(e) => edit({ ...draft, search: e.target.value })}
              />
            </label>
            <button
              className="filter-trigger"
              type="button"
              aria-pressed={draft.unlinked}
              onClick={() => edit({ ...draft, unlinked: !draft.unlinked })}
            >
              <Unlink size={16} aria-hidden="true" />
              Только без привязки
            </button>
            <small>Записей · {list.length}</small>
          </>
        )}
      </FilterBar>
      <div className="photo-grid">
        {list.map((o) => {
          const photo = o.photos[0];
          const horse = book.horses.find((h) => h.id === o.horseId)?.name ?? 'Без привязки';
          return (
            <PhotoTile
              key={o.id}
              photo={photo}
              name={o.name}
              detail={[photo && photo.name !== o.name ? photo.name : '', dateLabel(o.date)]
                .filter(Boolean)
                .join(' · ')}
              tags={[
                horse,
                ...(photo ? [photo.angle, photo.framing] : []),
                ...(book.pending.includes(o.id) ? ['В очереди'] : []),
              ]}
              onClick={() => {
                setDetail(o);
                setImage(0);
              }}
            />
          );
        })}
        {!list.length && <p className="empty">Наблюдений по этим условиям нет.</p>}
      </div>
      {detail && fullscreen && (
        <Carousel
          photos={detail.photos}
          index={image}
          setIndex={setImage}
          close={() => setFullscreen(false)}
        />
      )}
      {detail && !fullscreen && (
        <Modal
          wide
          title={detail.name}
          onClose={() => {
            setDetail(null);
            onDismiss();
          }}
        >
          {detail.photos[image] && (
            <div className="record-photo">
              <MarkedPhoto photo={detail.photos[image]} />
              <button
                className="expand-photo"
                aria-label="Открыть фото на весь экран"
                onClick={() => setFullscreen(true)}
              >
                <Expand size={18} />
              </button>
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
            {detail.observer && (
              <>
                <dt>Наблюдатель</dt>
                <dd>{detail.observer}</dd>
              </>
            )}
            <dt>{book.node ? 'Место наблюдения' : 'Координаты'}</dt>
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
  const live = Boolean(book.node);
  const [observer, setObserver] = useState('');
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
  const valid = live
    ? Boolean(observer.trim())
    : validName(name) &&
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
        description={
          live
            ? 'Сохраните место наблюдения и фотографии в журнале.'
            : 'Зафиксируйте признаки и место встречи с животным.'
        }
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
              observer,
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
          {live ? (
            <label className="field">
              Наблюдатель
              <input value={observer} onChange={(e) => setObserver(e.target.value)} required />
            </label>
          ) : (
            <label className="field">
              Название наблюдения
              <input value={name} onChange={(e) => setName(e.target.value)} required />
            </label>
          )}
          <label className="field">
            Особь
            <Select
              label="Особь"
              value={horse}
              onChange={setHorse}
              options={[
                { value: '', label: 'Без привязки' },
                ...book.horses.map((h) => ({ value: h.id, label: h.name })),
              ]}
            />
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
            {live ? 'Место наблюдения' : 'Координаты'}
            <input
              placeholder={live ? 'Название места или координаты' : '47.78, 106.01'}
              value={coords}
              onChange={(e) => setCoords(e.target.value)}
            />
          </label>
          {!live && (
            <>
              {coords && !validCoordinates(coords) && (
                <p className="field-error">Введите широту и долготу через запятую.</p>
              )}
              <button
                className="text-link"
                type="button"
                onClick={() => setCoords('47.78, 106.01')}
              >
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
            </>
          )}
          <div className="dialog-actions">
            <Button size="lg" type="button" variant="outline" onClick={close}>
              Отменить
            </Button>
            <Button size="lg" disabled={!valid || busy}>
              {busy && <LoaderCircle className="spin" size={15} />}Сохранить запись
            </Button>
          </div>
        </fieldset>
      </form>
    </>
  );
}
