import { useRef, useState } from 'react';
import {
  ArrowRight,
  Check,
  FileImage,
  Images,
  LoaderCircle,
  ScanLine,
  Upload,
  UserRoundPlus,
} from 'lucide-react';
import { samplePhoto } from '../journal/fixtures';
import {
  dateLabel,
  horsePhotos,
  validName,
  type AnalysisInput,
  type Notebook,
  type Review,
} from '../journal/types';
import { importImage } from '../journal/media';
import { useCommand } from '../journal/use-journal';
import { Heading, Modal, Pager, Toggle, useNotice } from '../shared/ui/journal-ui';
import { MarkedPhoto, PhotoGrid, Thumbnail } from '../shared/ui/photos';
import { Button } from '../shared/ui/button';

export function ReviewPage({ book, openLog }: { book: Notebook; openLog: () => void }) {
  return book.review ? (
    <Comparison key={book.review.id} book={book} review={book.review} />
  ) : (
    <Intake book={book} openLog={openLog} />
  );
}
function Intake({ book, openLog }: { book: Notebook; openLog: () => void }) {
  const picker = useRef<HTMLInputElement>(null),
    locked = useRef(false);
  const [loading, setLoading] = useState(false),
    [drag, setDrag] = useState(false);
  const analyze = useCommand('/analysis'),
    notice = useNotice();
  async function begin(input: () => Promise<AnalysisInput>) {
    if (locked.current) return;
    locked.current = true;
    setLoading(true);
    try {
      await analyze.mutateAsync(await input());
    } catch (error) {
      notice((error as Error).message, true);
    } finally {
      locked.current = false;
      setLoading(false);
    }
  }
  function pick(files: FileList | null) {
    if (!files?.length) return;
    if (files.length !== 1) {
      notice('Выберите одну фотографию за раз.', true);
      return;
    }
    const selected = files[0]!;
    void begin(async () => ({ upload: await importImage(selected) }));
  }
  const recent = [...book.observations].sort((a, b) => b.date.localeCompare(a.date)).slice(0, 2);
  return (
    <>
      <Heading
        title="Новое наблюдение"
        description="Загрузите полевой снимок, чтобы сравнить лошадей с особями в каталоге."
      />
      <input
        type="file"
        className="sr-only"
        aria-label="Файл наблюдения"
        tabIndex={-1}
        ref={picker}
        accept="image/*,.heic,.heif,.tif,.tiff,.dng,.avif,.jxl"
        disabled={loading}
        onChange={(e) => {
          pick(e.target.files);
          e.target.value = '';
        }}
      />
      <button
        className={'dropzone' + (drag ? ' drag' : '')}
        disabled={loading}
        aria-busy={loading}
        onClick={() => picker.current?.click()}
        onDragOver={(e) => {
          e.preventDefault();
          if (!loading) setDrag(true);
        }}
        onDragLeave={(e) => {
          if (!e.currentTarget.contains(e.relatedTarget as Node)) setDrag(false);
        }}
        onDrop={(e) => {
          e.preventDefault();
          setDrag(false);
          if (!loading) pick(e.dataTransfer.files);
        }}
      >
        <span className="upload-symbol">
          {loading ? (
            <LoaderCircle className="spin" size={26} />
          ) : (
            <Upload size={26} strokeWidth={1.6} />
          )}
        </span>
        <strong>{loading ? 'Анализируем снимок…' : 'Перетащите фотографию сюда'}</strong>
        <span>
          {loading ? 'Подбираем кандидатов из каталога' : 'Один снимок в любом формате изображения'}
        </span>
        {!loading && (
          <span className="pick-file">
            <FileImage size={15} />
            Выбрать файл
          </span>
        )}
      </button>
      <section className="home-section">
        <div className="section-heading">
          <h2>Примеры для распознавания</h2>
        </div>
        <div className="sample-grid">
          {(['single', 'pair'] as const).map((key, i) => (
            <button
              key={key}
              className="sample-card"
              disabled={loading}
              onClick={() => void begin(async () => ({ sample: key }))}
            >
              <div className="sample-art">
                <Thumbnail photo={samplePhoto(key)} />
                <span className="photo-chip">
                  <ScanLine size={13} />
                  {i ? '2 особи' : '1 особь'}
                </span>
              </div>
              <div className="sample-info">
                <div>
                  <strong>{i ? 'Табун у сопки' : 'На выпасе'}</strong>
                  <small>{i ? 'Групповое наблюдение' : 'Одиночное наблюдение'}</small>
                </div>
                <span className="round-arrow">
                  <ArrowRight size={18} />
                </span>
              </div>
            </button>
          ))}
        </div>
      </section>
      <section className="home-section">
        <div className="section-heading">
          <h2>Последние наблюдения</h2>
          <button className="text-link" onClick={openLog}>
            В журнал <ArrowRight size={15} />
          </button>
        </div>
        <div className="recent-list">
          {recent.map((o) => (
            <button key={o.id} className="recent" onClick={openLog}>
              {o.photos[0] ? <Thumbnail photo={o.photos[0]} /> : <FileImage />}
              <span>{o.name}</span>
              <small>{dateLabel(o.date)}</small>
            </button>
          ))}
        </div>
      </section>
    </>
  );
}
function Comparison({ book, review }: { book: Notebook; review: Review }) {
  const [ordinal, setOrdinal] = useState(review.detections[0]!.number);
  const detection =
    review.detections.find((d) => d.number >= ordinal) ??
    review.detections[review.detections.length - 1]!;
  const index = review.detections.indexOf(detection);
  const [choices, setChoices] = useState<Record<string, { id: string; photo: number }>>({});
  const candidateId = choices[detection.id]?.id ?? detection.candidates[0]!.horseId;
  const photos = horsePhotos(book, candidateId),
    photoIndex = choices[detection.id]?.photo ?? 0;
  const candidate = book.horses.find((h) => h.id === candidateId)!;
  const right = photos[photoIndex] ?? photos[0]!;
  function select(id: string, photo: number) {
    setChoices((old) => ({ ...old, [detection.id]: { id, photo } }));
  }
  const [traits, setTraits] = useState(true),
    [modal, setModal] = useState<'pick' | 'match' | 'new' | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const filename = drafts[detection.id] ?? defaultName(review.photo.name, detection.number);
  const resolve = useCommand('/decisions'),
    notice = useNotice();
  const actionLock = useRef(false),
    saving = resolve.isPending;
  async function save() {
    if (actionLock.current || !validName(filename)) return;
    actionLock.current = true;
    try {
      await resolve.mutateAsync({
        reviewId: review.id,
        detectionId: detection.id,
        horseId: modal === 'new' ? null : candidateId,
        name: filename,
      });
      notice(
        modal === 'new'
          ? 'Новая особь добавлена в галерею.'
          : 'Наблюдение добавлено к особи «' + candidate.name + '».',
      );
    } catch (error) {
      notice((error as Error).message, true);
    } finally {
      setModal(null);
      actionLock.current = false;
    }
  }
  return (
    <>
      <Heading
        title="Сравнение наблюдений"
        description={
          review.illustrative
            ? 'Для загруженного снимка показаны иллюстративные признаки и кандидаты.'
            : review.photo.name
        }
      />
      <div className="review-toolbar">
        <Toggle label="Показывать признаки" on={traits} onChange={() => setTraits((v) => !v)} />
        <div className="actions">
          <Button variant="outline" onClick={() => setModal('new')}>
            <UserRoundPlus size={16} />
            Нет подходящих
          </Button>
          <Button onClick={() => setModal('match')}>
            <Check size={16} />
            Зафиксировать результат
          </Button>
        </div>
      </div>
      <div className="comparison">
        <article className="compare-card">
          <header>
            <span className="eyelabel">Полевой снимок</span>
            <h2>Новое наблюдение {detection.number}</h2>
          </header>
          <MarkedPhoto
            photo={review.photo}
            annotation={detection.annotation}
            traits={traits}
            highlight={review.detections.length > 1}
          />
          <footer>
            <span>Особь на снимке</span>
            <Pager
              index={index}
              total={review.detections.length}
              label="Особь"
              onChange={(i) => setOrdinal(review.detections[i]!.number)}
            />
          </footer>
        </article>
        <article className="compare-card">
          <header>
            <span className="eyelabel">Фото из каталога · {candidate.name}</span>
            <h2>{right.name}</h2>
          </header>
          <MarkedPhoto photo={right} traits={traits} onClick={() => setModal('pick')} />
          <footer>
            <button className="text-link" onClick={() => setModal('pick')}>
              <Images size={15} />
              Все фото
            </button>
            <Pager
              index={photoIndex}
              total={photos.length}
              label="Фото кандидата"
              onChange={(i) => select(candidateId, i)}
            />
          </footer>
        </article>
      </div>
      <section className="home-section">
        <div className="section-heading">
          <h2>Вероятные кандидаты</h2>
          <small>{detection.candidates.length} особи</small>
        </div>
        <div className="candidate-grid">
          {detection.candidates.map((c) => {
            const horse = book.horses.find((h) => h.id === c.horseId)!;
            return (
              <button
                key={horse.id}
                aria-pressed={candidateId === horse.id}
                className={'candidate' + (candidateId === horse.id ? ' selected' : '')}
                onClick={() => select(horse.id, 0)}
              >
                <Thumbnail photo={horsePhotos(book, horse.id)[0]!} />
                <span>
                  <strong>{horse.name}</strong>
                  <b>{c.score}%</b>
                  <i>
                    <em
                      style={{
                        width: c.score + '%',
                        background: 'hsl(' + Math.round(12 + c.score * 0.92) + ' 32% 46%)',
                      }}
                    />
                  </i>
                </span>
              </button>
            );
          })}
        </div>
      </section>
      {modal === 'pick' && (
        <Modal wide title={'Фотографии · ' + candidate.name} onClose={() => setModal(null)}>
          <PhotoGrid
            photos={photos}
            onSelect={(p) => {
              select(candidateId, photos.indexOf(p));
              setModal(null);
            }}
          />
        </Modal>
      )}
      {(modal === 'match' || modal === 'new') && (
        <Modal
          title={modal === 'match' ? 'Зафиксировать результат' : 'Нет подходящих лошадей'}
          busy={saving}
          onClose={() => setModal(null)}
          description={
            modal === 'match'
              ? 'Наблюдение ' +
                detection.number +
                ' будет добавлено к наблюдениям лошади «' +
                candidate.name +
                '».'
              : 'Наблюдение ' +
                detection.number +
                ' не подходит ни одной лошади из списка, будет сохранено как отдельная особь.'
          }
        >
          <form
            onSubmit={(e) => {
              e.preventDefault();
              void save();
            }}
          >
            <label className="field">
              Название наблюдения
              <input
                autoFocus
                value={filename}
                disabled={saving}
                onChange={(e) => setDrafts({ ...drafts, [detection.id]: e.target.value })}
              />
            </label>
            {!validName(filename) && (
              <p className="field-error">Введите допустимое название файла.</p>
            )}
            <div className="dialog-actions">
              <Button
                type="button"
                variant="outline"
                disabled={saving}
                onClick={() => setModal(null)}
              >
                Отменить
              </Button>
              <Button disabled={saving || !validName(filename)}>
                {saving ? (
                  <>
                    <LoaderCircle size={15} className="spin" />
                    Сохранение…
                  </>
                ) : (
                  'Подтвердить'
                )}
              </Button>
            </div>
          </form>
        </Modal>
      )}
    </>
  );
}
function defaultName(name: string, number: number) {
  const dot = name.lastIndexOf('.');
  return dot > 0 ? name.slice(0, dot) + ' — ' + number + name.slice(dot) : name + ' — ' + number;
}
