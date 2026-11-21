import { useEffect, useRef, useState } from 'react';
import {
  Activity,
  Binoculars,
  BookOpen,
  ChevronRight,
  CircleHelp,
  FolderOpen,
  Images,
  Leaf,
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
    [help, setHelp] = useState(false);
  const main = useRef<HTMLElement>(null),
    { data: book, error, refetch } = useNotebook();
  useEffect(() => {
    main.current?.scrollTo(0, 0);
  }, [route, book?.review?.id]);
  const current = sections.flatMap((s) => [...s.items]).find((s) => s.key === route);
  return (
    <div className="shell">
      <aside className="sidebar">
        <div className="brand">
          <span className="brand-icon">
            <Binoculars size={27} strokeWidth={1.55} />
          </span>
          <div>
            <strong>Тахи</strong>
            <small>Полевой журнал</small>
          </div>
        </div>
        <nav aria-label="Навигация">
          {sections.map((section) => (
            <div className="nav-group" key={section.label}>
              <h2>{section.label}</h2>
              {section.items.map(({ key, label, icon: Icon }) => (
                <button
                  key={key}
                  aria-current={key === route ? 'page' : undefined}
                  className={key === route ? 'active' : ''}
                  onClick={() => setRoute(key)}
                >
                  <Icon size={18} strokeWidth={1.65} />
                  {label}
                  {key === 'sync' && !!book?.pending.length && <b>{book.pending.length}</b>}
                </button>
              ))}
            </div>
          ))}
        </nav>
        <div className="side-bottom">
          <span className="local-mark">
            <Leaf size={17} />
          </span>
          <div>
            <strong>Локальная станция</strong>
            <small>
              <i />
              Деморежим
            </small>
          </div>
        </div>
      </aside>
      <div className="work">
        <div className="topbar">
          <div>
            <FolderOpen size={15} />
            <span>Рабочая область</span>
            <ChevronRight size={13} />
            <b>{current?.label}</b>
          </div>
          <button className="help" onClick={() => setHelp(true)}>
            <CircleHelp size={16} />
            Справка
          </button>
        </div>
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
                {route === 'review' && <ReviewPage book={book} openLog={() => setRoute('log')} />}
                {route === 'gallery' && <Gallery book={book} />}
                {route === 'log' && <ObservationLog book={book} />}
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
