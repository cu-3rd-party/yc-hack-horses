import { useEffect, useRef, useState } from 'react';
import { LoaderCircle } from 'lucide-react';
import { useDemo } from '../journal/mode';
import { useNotebook } from '../journal/use-journal';
import { ReviewPage, type PhotoBatch } from '../pages/review';
import { Gallery } from '../pages/gallery';
import { ObservationLog } from '../pages/observations';
import { SystemPage } from '../pages/system';
import { Modal } from '../shared/ui/journal-ui';
import { Sidebar } from './sidebar';

// MPA-навигация (ADR-0015): активная страница задаётся в html-входе через
// <body data-page="...">, App остаётся общим каркасом (сайдбар + провайдеры).
const pages = ['review', 'gallery', 'observations', 'system'] as const;
type Page = (typeof pages)[number];
function currentPage(): Page {
  const raw = document.body.dataset.page;
  return (pages as readonly string[]).includes(raw ?? '') ? (raw as Page) : 'review';
}

export default function App() {
  const demo = useDemo();
  return <Workspace key={String(demo)} demo={demo} page={currentPage()} />;
}

function Workspace({ demo, page }: { demo: boolean; page: Page }) {
  const [help, setHelp] = useState(false),
    [collapsed, setCollapsed] = useState(false),
    [, setProcessing] = useState(false),
    [batch, setBatch] = useState<PhotoBatch | null>(null);
  const main = useRef<HTMLElement>(null),
    { data: book, error, refetch } = useNotebook();
  useEffect(() => {
    main.current?.scrollTo(0, 0);
  }, [page, book?.review?.id]);
  // Переход к наблюдению со страницы «Распознавание» — обычная навигация
  // на observations.html с якорем-идентификатором записи.
  const openLog = (id?: string) => {
    window.location.href = '/observations.html' + (id ? '#' + encodeURIComponent(id) : '');
  };
  return (
    <div className="shell">
      <Sidebar
        page={page}
        pending={book?.pending.length ?? 0}
        collapsed={collapsed}
        setCollapsed={setCollapsed}
        onHelp={() => setHelp(true)}
      />
      <main className="work" ref={main}>
        <div className="content">
          {page === 'system' ? (
            <SystemPage book={book} error={error} refetch={() => void refetch()} />
          ) : error ? (
            <div className="empty" role="alert">
              {error.message || 'Не удалось открыть журнал.'}{' '}
              <button className="text-link" onClick={() => void refetch()}>
                Повторить
              </button>
            </div>
          ) : !book ? (
            <p className="empty">
              <LoaderCircle className="spin" /> Открываем журнал…
            </p>
          ) : (
            <>
              {page === 'review' && (
                <ReviewPage
                  batch={batch}
                  setBatch={setBatch}
                  onBusyChange={setProcessing}
                  book={book}
                  openLog={openLog}
                />
              )}
              {page === 'gallery' && <Gallery book={book} />}
              {page === 'observations' && (
                <ObservationLog
                  book={book}
                  initialId={decodeURIComponent(window.location.hash.replace(/^#/, '')) || undefined}
                  onDismiss={() =>
                    history.replaceState(null, '', window.location.pathname + window.location.search)
                  }
                />
              )}
            </>
          )}
        </div>
      </main>
      {help && (
        <Modal title="Как пользоваться журналом" onClose={() => setHelp(false)}>
          <ol className="guide">
            <li>
              {demo ? 'Выберите пример или загрузите полевой снимок.' : 'Загрузите полевой снимок.'}
            </li>
            <li>
              Сравните каждую лошадь с кандидатами. Нажмите на фото справа, чтобы выбрать другой
              ракурс.
            </li>
            <li>
              Зафиксируйте совпадение или сохраните новую особь. Записи появятся в галерее и
              журнале.
            </li>
          </ol>
          <p className="dialog-description">
            {demo
              ? 'Деможурнал хранится на этом устройстве. Распознавание и обмен с узлом смоделированы.'
              : 'Рабочий журнал и фотографии хранятся на ноде журнала.'}
          </p>
        </Modal>
      )}
    </div>
  );
}
