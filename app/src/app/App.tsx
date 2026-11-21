import { useEffect, useRef, useState } from 'react';
import {
  Activity,
  Binoculars,
  BookOpen,
  CircleHelp,
  Images,
  LoaderCircle,
  RefreshCw,
  ScanLine,
  Settings2,
} from 'lucide-react';
import { useNotebook } from '../journal/use-journal';
import { ReviewPage } from '../pages/review';
import { Gallery } from '../pages/gallery';
import { ObservationLog } from '../pages/observations';
import { Dashboard, Settings, Sync } from '../pages/system';
import { Modal } from '../shared/ui/journal-ui';
const sections = [
  {
    label: 'Полевой журнал',
    items: [
      { key: 'review', label: 'Распознавание', icon: ScanLine },
      { key: 'gallery', label: 'Галерея особей', icon: Images },
      { key: 'log', label: 'Наблюдения', icon: BookOpen },
    ],
  },
  {
    label: 'Рабочая станция',
    items: [
      { key: 'dashboard', label: 'Сводка', icon: Activity },
      { key: 'sync', label: 'Синхронизация', icon: RefreshCw },
      { key: 'settings', label: 'Настройки', icon: Settings2 },
    ],
  },
] as const;
type Route = (typeof sections)[number]['items'][number]['key'];
export default function App() {
  const [route, setRoute] = useState<Route>('review'),
    [help, setHelp] = useState(false),
    [processing, setProcessing] = useState(false),
    [logId, setLogId] = useState<string | undefined>();
  const main = useRef<HTMLElement>(null),
    { data: book, error, refetch } = useNotebook();
  useEffect(() => {
    main.current?.scrollTo(0, 0);
  }, [route, book?.review?.id]);
  return (
    <div className="shell">
      <header className="masthead">
        <div className="mast-inner">
          <button
            className="brand"
            aria-label="Тахи — распознавание"
            onClick={() => setRoute('review')}
          >
            <span className="brand-icon">
              <Binoculars size={21} strokeWidth={1.7} />
            </span>
            <strong>Тахи</strong>
          </button>
          <nav aria-label="Навигация">
            {sections.map((section, index) => (
              <div className={'nav-group' + (index ? ' tools' : '')} key={section.label}>
                {section.items.map(({ key, label, icon: Icon }) => (
                  <button
                    key={key}
                    title={label}
                    aria-label={label}
                    disabled={processing}
                    aria-current={key === route ? 'page' : undefined}
                    onClick={() => setRoute(key)}
                  >
                    {index > 0 && <Icon size={18} strokeWidth={1.7} />}
                    <span>{label}</span>
                    {key === 'sync' && !!book?.pending.length && <b>{book.pending.length}</b>}
                  </button>
                ))}
              </div>
            ))}
          </nav>
          <button
            className="help"
            title="Справка"
            aria-label="Справка"
            onClick={() => setHelp(true)}
          >
            <CircleHelp size={18} strokeWidth={1.7} />
          </button>
        </div>
      </header>
      <div className="work">
        <main ref={main}>
          <div className="content">
            {error ? (
              <div className="empty" role="alert">
                Не удалось открыть журнал.{' '}
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
                {route === 'review' && (
                  <ReviewPage
                    onBusyChange={setProcessing}
                    book={book}
                    openLog={(id) => {
                      setLogId(id);
                      setRoute('log');
                    }}
                  />
                )}
                {route === 'gallery' && <Gallery book={book} />}
                {route === 'log' && (
                  <ObservationLog
                    book={book}
                    initialId={logId}
                    onDismiss={() => setLogId(undefined)}
                  />
                )}
                {route === 'dashboard' && <Dashboard book={book} />}
                {route === 'sync' && <Sync book={book} />}
                {route === 'settings' && <Settings book={book} />}
              </>
            )}
          </div>
        </main>
      </div>
      {help && (
        <Modal title="Как пользоваться журналом" onClose={() => setHelp(false)}>
          <ol className="guide">
            <li>Выберите пример или загрузите полевой снимок.</li>
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
            Данные и фотографии хранятся на этом устройстве. Распознавание и обмен с узлом
            смоделированы.
          </p>
        </Modal>
      )}
    </div>
  );
}
