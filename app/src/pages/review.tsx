import { useEffect, useRef, useState, type Dispatch, type SetStateAction } from 'react';
import {
  ArrowRight,
  Check,
  FileImage,
  Images,
  LoaderCircle,
  CircleAlert,
  UserRoundPlus,
} from 'lucide-react';
import { desktop } from '../journal/node';
import { ModelPanel, useModels } from './models';
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
import { useCommand, useHorseHistory } from '../journal/use-journal';
import { Heading, Modal, Pager, Toggle, useNotice } from '../shared/ui/journal-ui';
import { MarkedPhoto, PhotoGrid, Thumbnail } from '../shared/ui/photos';
import { Button } from '../shared/ui/button';
import { FileDropzone } from '../shared/ui/file-dropzone';

export interface PhotoBatch {
  files: File[];
  index: number;
  phase: 'queued' | 'processing' | 'review' | 'failed';
  error?: string;
}
function nextPhoto(batch: PhotoBatch | null): PhotoBatch | null {
  if (!batch || batch.index + 1 >= batch.files.length) return null;
  return { files: batch.files, index: batch.index + 1, phase: 'queued' };
}
interface IntakeProps {
  book: Notebook;
  batch: PhotoBatch | null;
  setBatch: Dispatch<SetStateAction<PhotoBatch | null>>;
  openLog: (id?: string) => void;
  onBusyChange: (busy: boolean) => void;
}
export function ReviewPage(props: IntakeProps) {
  const { review } = props.book;
  const { batch, setBatch } = props;
  return (
    <>
      {batch && batch.files.length > 1 && (
        <p className="eyelabel" role="status">
          Снимок {batch.index + 1} из {batch.files.length} · {batch.files[batch.index]!.name}
        </p>
      )}
      {review ? (
        <Comparison
          key={review.id}
          book={props.book}
          review={review}
          hasMorePhotos={!!batch && batch.index + 1 < batch.files.length}
          onComplete={() => setBatch(nextPhoto)}
          onCancel={() => setBatch(null)}
        />
      ) : (
        <Intake {...props} />
      )}
    </>
  );
}
function Intake({ book, batch, setBatch, openLog, onBusyChange }: IntakeProps) {
  const locked = useRef(false);
  const [loading, setLoading] = useState(false);
  const [fileName, setFileName] = useState('');
  const [error, setError] = useState('');
  const [emptyResult, setEmptyResult] = useState(false);
  const analyze = useCommand('/analysis');
  const notice = useNotice();
  const setup = useModels(Boolean(book.node));
  const waiting = Boolean(book.node && (setup.isPending || setup.isError || (setup.data && !setup.data.ready)));
  const uploadDisabled = loading || !!batch || (!desktop && waiting);
  useEffect(() => {
    if (batch?.phase !== 'queued' || waiting) return;
    const file = batch.files[batch.index]!;
    void begin(file.name, async () => ({ upload: await importImage(file) }), batch);
  }, [batch, waiting]);
  async function begin(name: string, input: () => Promise<AnalysisInput>, current?: PhotoBatch) {
    if (locked.current) return;
    locked.current = true;
    if (current) setBatch({ ...current, phase: 'processing' });
    setFileName(name);
    setError('');
    setEmptyResult(false);
    setLoading(true);
    onBusyChange(true);
    try {
      const result = await analyze.mutateAsync(await input());
      const empty = result?.detections.length === 0;
      setEmptyResult(empty);
      if (current) {
        if (empty && current.files.length > 1) notice(name + ' — лошади не обнаружены.');
        setBatch(empty ? nextPhoto(current) : { ...current, phase: 'review' });
      }
    } catch (cause) {
      const message = (cause as Error).message;
      setError(message);
      if (current) {
        setBatch(current.files.length > 1 ? { ...current, phase: 'failed', error: message } : null);
      }
    } finally {
      locked.current = false;
      setLoading(false);
      onBusyChange(false);
    }
  }
  function pick(files: FileList | null) {
    if (!files?.length || locked.current || uploadDisabled) return;
    setFileName('');
    setError('');
    setEmptyResult(false);
    setBatch({ files: Array.from(files), index: 0, phase: 'queued' });
  }
  function dismissFile(skip: boolean) {
    setError('');
    setFileName('');
    setBatch(skip ? nextPhoto : null);
  }
  const recent = [...book.observations].sort((a, b) => b.date.localeCompare(a.date)).slice(0, 3);
  return (
    <>
      <Heading
        title="Новое наблюдение"
        description="Загрузите снимки, чтобы найти лошадей в каталоге."
      />
      {book.node && setup.isError && <p role="alert">Не удалось проверить модели. <button className="text-link" onClick={()=>void setup.refetch()}>Повторить</button></p>}
      {book.node && setup.data && !setup.data.ready && <ModelPanel setup={setup.data}/>}
      <div className="intake-grid">
        <section aria-label="Загрузка фотографии">
          <h2 className="intake-label">Полевой снимок</h2>
          <FileDropzone
            label="Файл наблюдения" accept="image/*,.heic,.heif,.tif,.tiff,.dng,.avif,.jxl" multiple
            disabled={uploadDisabled} busyLabel={loading ? 'Анализируем снимок…' : undefined}
            invalid={!!error} describedBy="upload-feedback" onFiles={pick}
          />
          {emptyResult && <p role="status">На снимке не обнаружено лошадей.</p>}
          {(batch || fileName) && (
            <div className="file-state" role="status">
              <FileImage size={17} />
              <span>{batch ? batch.files[batch.index]!.name : fileName}</span>
              {loading && <small>Обработка</small>}
              {waiting && batch?.phase === 'queued' && (
                <>
                  <small>Ожидает готовности моделей</small>
                  <button className="text-link" onClick={() => dismissFile(false)}>Отменить загрузку</button>
                </>
              )}
            </div>
          )}
          {(batch?.error || error) ? (
            <div id="upload-feedback" className="file-error" role="alert">
              <CircleAlert size={16} />
              <span>{batch?.error || error}</span>
              {batch?.phase === 'failed' && (
                <>
                  <button className="text-link" onClick={() => setBatch({ ...batch, phase: 'queued', error: undefined })}>
                    Повторить
                  </button>
                  <button className="text-link" onClick={() => dismissFile(true)}>Пропустить снимок</button>
                  <button className="text-link" onClick={() => dismissFile(false)}>Отменить очередь</button>
                </>
              )}
            </div>
          ) : (
            <p className="file-hint" id="upload-feedback">
              {desktop
                ? 'Снимки обрабатываются по очереди. JPG, PNG, HEIC, TIFF и другие форматы изображений.'
                : 'Снимки обрабатываются по очереди. Подойдут JPG, PNG и другие форматы, которые поддерживает браузер.'}
            </p>
          )}
        </section>
        <section aria-label="Последние наблюдения" className="recent-section">
          <div className="section-heading">
            <h2>Последние наблюдения</h2>
            <button className="text-link" disabled={loading} onClick={() => openLog()}>
              В журнал <ArrowRight size={15} />
            </button>
          </div>
          <div className="recent-list">
            {recent.map((o) => (
              <button
                key={o.id}
                className="recent"
                disabled={loading}
                onClick={() => openLog(o.id)}
              >
                {o.photos[0] ? <Thumbnail photo={o.photos[0]} /> : <FileImage size={20} />}
                <span>{o.name}</span>
                <small>{dateLabel(o.date)}</small>
              </button>
            ))}
          </div>
        </section>
      </div>
      {!book.node && (
        <section className="home-section">
          <div className="section-heading">
            <h2>Примеры для распознавания</h2>
          </div>
          <div className="sample-grid">
            {(['single', 'pair'] as const).map((key, i) => {
              const photo = samplePhoto(key);
              return (
                <button
                  key={key}
                  className="sample-card"
                  disabled={uploadDisabled}
                  onClick={() => void begin(photo.name, async () => ({ sample: key }))}
                >
                  <div className="sample-art">
                    <Thumbnail photo={photo} />
                  </div>
                  <div className="sample-info">
                    <div>
                      <strong>{i ? 'Табун у сопки' : 'На выпасе'}</strong>
                      <small>{i ? 'Две лошади' : 'Одна лошадь'}</small>
                    </div>
                    <ArrowRight size={18} strokeWidth={1.6} />
                  </div>
                </button>
              );
            })}
          </div>
        </section>
      )}
    </>
  );
}
function Comparison({ book, review, hasMorePhotos, onComplete, onCancel }: {
  book: Notebook;
  review: Review;
  hasMorePhotos: boolean;
  onComplete: () => void;
  onCancel: () => void;
}) {
  const [ordinal, setOrdinal] = useState(review.detections[0]!.number);
  const detection =
    review.detections.find((d) => d.number >= ordinal) ??
    review.detections[review.detections.length - 1]!;
  const index = review.detections.indexOf(detection);
  const [choices, setChoices] = useState<Record<string, { id: string; photo?: number }>>({});
  const choice = choices[detection.id];
  const candidateId = choice?.id ?? detection.candidates[0]?.horseId;
  const ranked = detection.candidates.find((c) => c.horseId === candidateId);
  const history = useHorseHistory(book, candidateId);
  const photos = book.node
    ? (history.data?.flatMap((o) => o.photos) ?? [])
    : horsePhotos(book, candidateId ?? '');
  const preferred = ranked?.bestPhotoId;
  const photoIndex =
    choice?.photo ??
    Math.max(
      0,
      photos.findIndex((p) => p.id === preferred),
    );
  const candidateName = ranked?.name ?? book.horses.find((h) => h.id === candidateId)?.name;
  const right = photos[photoIndex] ?? photos[0];
  function select(id: string, photo?: number) {
    setChoices((old) => ({ ...old, [detection.id]: { id, photo } }));
  }
  const [traits, setTraits] = useState(true),
    [modal, setModal] = useState<'pick' | 'match' | 'new' | 'cancel' | null>(null);
  const [drafts, setDrafts] = useState<Record<string, string>>({});
  const [comments, setComments] = useState<Record<string, string>>({});
  const [horseNames, setHorseNames] = useState<Record<string, string>>({});
  const horseName = horseNames[detection.id] ?? '';
  const filename = drafts[detection.id] ?? defaultName(review.photo.name, detection.number);
  const resolve = useCommand('/decisions'),
    cancel = useCommand('/analysis/cancel'),
    notice = useNotice();
  const actionLock = useRef(false),
    saving = resolve.isPending || cancel.isPending;
  async function save() {
    if (
      actionLock.current ||
      (modal !== 'cancel' && (!validName(filename) || (modal === 'new' && !horseName.trim())))
    )
      return;
    actionLock.current = true;
    try {
      if (modal === 'cancel') {
        await cancel.mutateAsync({ reviewId: review.id });
        onCancel();
        notice('Сравнение отменено.');
        return;
      }
      await resolve.mutateAsync({
        reviewId: review.id,
        detectionId: detection.id,
        horseId: modal === 'new' ? null : candidateId!,
        name: filename,
        comment: comments[detection.id],
        horseName: modal === 'new' ? horseName : undefined,
      });
      if (review.detections.length === 1) onComplete();
      notice(
        modal === 'new'
          ? 'Новая особь добавлена в галерею.'
          : 'Наблюдение добавлено к особи «' + candidateName + '».',
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
        <Toggle label="Показать черты" on={traits} onChange={() => setTraits((v) => !v)} />
        <div className="actions">
          <Button
            size="lg"
            className="danger-link"
            variant="ghost"
            onClick={() => setModal('cancel')}
          >
            Отменить сравнение
          </Button>
          <Button size="lg" variant="outline" onClick={() => setModal('new')}>
            <UserRoundPlus size={16} />
            Нет подходящих
          </Button>
          <Button size="lg" disabled={!candidateId} onClick={() => setModal('match')}>
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
            <span className="photo-filename" title={review.photo.name}>
              {review.photo.name}
            </span>
            <Pager
              index={index}
              total={review.detections.length}
              label="Лошадь"
              onChange={(i) => setOrdinal(review.detections[i]!.number)}
            />
          </footer>
        </article>
        <article className="compare-card">
          {right && candidateId ? (
            <>
              <header>
                <span className="eyelabel">Фото из каталога · {candidateName}</span>
                <h2>{right.name}</h2>
              </header>
              <MarkedPhoto photo={right} traits={traits} onClick={() => setModal('pick')} />
              <footer>
                <button className="text-link" onClick={() => setModal('pick')}>
                  <Images size={15} />
                  Все фото особи
                </button>
                <Pager
                  index={photoIndex}
                  total={photos.length}
                  label="Фото"
                  onChange={(i) => select(candidateId, i)}
                />
              </footer>
            </>
          ) : (
            <p className="empty" role="status">
              {!candidateId ? (
                'Подходящих особей не найдено.'
              ) : history.isError ? (
                <>
                  Не удалось загрузить фотографии.{' '}
                  <button className="text-link" onClick={() => void history.refetch()}>
                    Повторить
                  </button>
                </>
              ) : history.isFetching ? (
                'Загружаем фотографии…'
              ) : (
                'У особи нет фотографий.'
              )}
            </p>
          )}
        </article>
      </div>
      <section className="home-section">
        <div className="section-heading">
          <h2>{book.node ? 'Кандидаты по сходству' : 'Вероятные кандидаты'}</h2>
          <small>{detection.candidates.length}</small>
        </div>
        <div className="candidate-grid">
          {detection.candidates.map((c) => {
            const preview = horsePhotos(book, c.horseId);
            const thumb = preview.find((p) => p.id === c.bestPhotoId) ?? preview[0];
            const name = c.name ?? book.horses.find((h) => h.id === c.horseId)?.name ?? c.horseId;
            const percent = book.node ? Math.round(c.score * 100) : c.score;
            const bar = Math.min(100, Math.max(0, percent));
            return (
              <button
                key={c.horseId}
                aria-pressed={candidateId === c.horseId}
                className={'candidate' + (candidateId === c.horseId ? ' selected' : '')}
                onClick={() => select(c.horseId)}
              >
                {thumb ? <Thumbnail photo={thumb} /> : <FileImage size={22} />}
                <span>
                  <strong>{name}</strong>
                  <b>{percent}%</b>
                  <i>
                    <em
                      style={{
                        width: bar + '%',
                        backgroundColor:
                          'color-mix(in oklab, var(--muted), var(--score) ' + bar + '%)',
                      }}
                    />
                  </i>
                </span>
              </button>
            );
          })}
        </div>
      </section>
      {modal === 'pick' && candidateId && (
        <Modal wide title={'Фотографии · ' + candidateName} onClose={() => setModal(null)}>
          <PhotoGrid
            photos={photos}
            onSelect={(p) => {
              select(candidateId, photos.indexOf(p));
              setModal(null);
            }}
          />
        </Modal>
      )}
      {modal === 'cancel' && (
        <Modal
          title="Отменить сравнение"
          description={
            hasMorePhotos
              ? 'Оставшиеся наблюдения и снимки очереди будут пропущены.'
              : 'Оставшиеся наблюдения будут пропущены'
          }
          busy={saving}
          onClose={() => setModal(null)}
        >
          <div className="dialog-actions">
            <Button size="lg" variant="outline" disabled={saving} onClick={() => setModal(null)}>
              Отменить
            </Button>
            <Button
              size="lg"
              className="danger-confirm"
              disabled={saving}
              onClick={() => void save()}
            >
              Подтвердить
            </Button>
          </div>
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
                candidateName +
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
            {modal === 'new' && (
              <label className="field">
                Имя лошади
                <input
                  data-dialog-focus
                  required
                  value={horseName}
                  disabled={saving}
                  onChange={(e) => setHorseNames({ ...horseNames, [detection.id]: e.target.value })}
                />
              </label>
            )}
            <label className="field">
              Название наблюдения
              <input
                data-dialog-focus={modal === 'match' ? true : undefined}
                aria-invalid={!validName(filename)}
                aria-describedby={!validName(filename) ? 'decision-name-error' : undefined}
                value={filename}
                disabled={saving}
                onChange={(e) => setDrafts({ ...drafts, [detection.id]: e.target.value })}
              />
            </label>
            <label className="field">
              Комментарий
              <textarea
                value={comments[detection.id] ?? ''}
                disabled={saving}
                onChange={(e) => setComments({ ...comments, [detection.id]: e.target.value })}
              />
            </label>
            {!validName(filename) && (
              <p className="field-error" id="decision-name-error">
                Введите допустимое название файла.
              </p>
            )}
            <div className="dialog-actions">
              <Button
                size="lg"
                type="button"
                variant="outline"
                disabled={saving}
                onClick={() => setModal(null)}
              >
                Отменить
              </Button>
              <Button
                size="lg"
                disabled={saving || !validName(filename) || (modal === 'new' && !horseName.trim())}
              >
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
