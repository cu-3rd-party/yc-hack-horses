import { useRef, useState } from 'react';
import {
  ArrowRight,
  Check,
  FileImage,
  Images,
  LoaderCircle,
  CircleAlert,
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
import { useCommand, useHorseHistory } from '../journal/use-journal';
import { Heading, Modal, Pager, Toggle, useNotice } from '../shared/ui/journal-ui';
import { MarkedPhoto, PhotoGrid, Thumbnail } from '../shared/ui/photos';
import { Button } from '../shared/ui/button';

interface IntakeProps {
  book: Notebook;
  openLog: (id?: string) => void;
  onBusyChange: (busy: boolean) => void;
}
export function ReviewPage(props: IntakeProps) {
  const review = props.book.review;
  return review ? (
    <Comparison key={review.id} book={props.book} review={review} />
  ) : (
    <Intake {...props} />
  );
}
function Intake({ book, openLog, onBusyChange }: IntakeProps) {
  const picker = useRef<HTMLInputElement>(null),
    locked = useRef(false);
  const [loading, setLoading] = useState(false);
  const [drag, setDrag] = useState(false);
  const [fileName, setFileName] = useState('');
  const [error, setError] = useState('');
  const [emptyResult, setEmptyResult] = useState(false);
  const analyze = useCommand('/analysis');
  async function begin(name: string, input: () => Promise<AnalysisInput>) {
    if (locked.current) return;
    locked.current = true;
    setFileName(name);
    setError('');
    setEmptyResult(false);
    setLoading(true);
    onBusyChange(true);
    try {
      const result = await analyze.mutateAsync(await input());
      setEmptyResult(result?.detections.length === 0);
    } catch (cause) {
      setError((cause as Error).message);
    } finally {
      locked.current = false;
      setLoading(false);
      onBusyChange(false);
    }
  }
  function pick(files: FileList | null) {
    if (!files?.length || locked.current) return;
    if (files.length !== 1) {
      setFileName('');
      setError('Выберите одну фотографию за раз.');
      return;
    }
    const selected = files[0]!;
    void begin(selected.name, async () => ({ upload: await importImage(selected) }));
  }
  const recent = [...book.observations].sort((a, b) => b.date.localeCompare(a.date)).slice(0, 3);
  return (
    <>
      <Heading
        title="Новое наблюдение"
        description="Загрузите снимок, чтобы найти лошадь в каталоге."
      />
      <div className="intake-grid">
        <section aria-label="Загрузка фотографии">
          <h2 className="intake-label">Полевой снимок</h2>
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
            className={'dropzone' + (drag ? ' drag' : '') + (error ? ' invalid' : '')}
            disabled={loading}
            aria-busy={loading}
            aria-describedby="upload-feedback"
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
              pick(e.dataTransfer.files);
            }}
          >
            {loading && <LoaderCircle className="spin" size={18} />}
            <span>
              {loading ? (
                'Анализируем снимок…'
              ) : (
                <>
                  <strong>Выберите файл</strong> или перетащите его сюда
                </>
              )}
            </span>
          </button>
          {emptyResult && <p role="status">На снимке не обнаружено лошадей.</p>}
          {fileName && (
            <div className="file-state" role="status">
              <FileImage size={17} />
              <span>{fileName}</span>
              {loading && <small>Обработка</small>}
            </div>
          )}
          {error ? (
            <div id="upload-feedback" className="file-error" role="alert">
              <CircleAlert size={16} />
              <span>{error}</span>
            </div>
          ) : (
            <p className="file-hint" id="upload-feedback">
              Одна фотография за раз. JPG, PNG, HEIC, TIFF и другие форматы изображений.
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
                  disabled={loading}
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
function Comparison({ book, review }: { book: Notebook; review: Review }) {
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
        notice('Сравнение отменено.');
        return;
      }
      await resolve.mutateAsync({
        reviewId: review.id,
        detectionId: detection.id,
        horseId: modal === 'new' ? null : candidateId!,
        name: filename,
        horseName: modal === 'new' ? horseName : undefined,
      });
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
          description="Оставшиеся наблюдения будут пропущены"
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
