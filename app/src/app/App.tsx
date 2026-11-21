import { useEffect, useRef, useState } from 'react';
import {
  Activity,
  Binoculars,
  BookOpen,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  Images,
  LoaderCircle,
  RefreshCw,
  ScanLine,
  Settings2,
} from 'lucide-react';
import { useDemo } from '../journal/mode';
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
  const demo = useDemo();
  const [route, setRoute] = useState<Route>('review');
  return <Workspace key={String(demo)} demo={demo} route={route} setRoute={setRoute} />;
}
function Workspace({
  demo,
  route,
  setRoute,
}: {
  demo: boolean;
  route: Route;
  setRoute: (route: Route) => void;
}) {
  const [help, setHelp] = useState(false),
    [collapsed, setCollapsed] = useState(false),
    [processing, setProcessing] = useState(false),
    [logId, setLogId] = useState<string | undefined>();
  const main = useRef<HTMLElement>(null),
    { data: book, error, refetch } = useNotebook();
  useEffect(() => {
    main.current?.scrollTo(0, 0);
  }, [route, book?.review?.id]);
  return (
    <div className="shell">
      <aside className="sidebar" data-collapsed={collapsed}>
        <div className="sidebar-head">
          <button
            className="brand"
            title="ЛошID — распознавание"
            aria-label="ЛошID — распознавание"
            onClick={() => setRoute('review')}
          >
            <span className="brand-icon">
              <Binoculars size={21} strokeWidth={1.7} />
            </span>
            <strong>ЛошID</strong>
          </button>
          <button
            className="sidebar-toggle"
            aria-controls="journal-nav"
            aria-expanded={!collapsed}
            aria-label={collapsed ? 'Развернуть меню' : 'Свернуть меню'}
            title={collapsed ? 'Развернуть меню' : 'Свернуть меню'}
            onClick={() => setCollapsed(!collapsed)}
          >
            {collapsed ? <ChevronRight size={16} /> : <ChevronLeft size={16} />}
          </button>
        </div>
        <nav id="journal-nav" aria-label="Навигация">
          {sections.map((section) => (
            <div className="nav-group" key={section.label}>
              <h2>{section.label}</h2>
              {section.items.map(({ key, label, icon: Icon }) => (
                <button
                  key={key}
                  title={label}
                  aria-label={label}
                  disabled={processing}
                  aria-current={key === route ? 'page' : undefined}
                  onClick={() => setRoute(key)}
                >
                  <Icon size={18} strokeWidth={1.7} />
                  <span>{label}</span>
                  {key === 'sync' && !!book?.pending.length && <b>{book.pending.length}</b>}
                </button>
              ))}
            </div>
          ))}
        </nav>
        <button
          className="sidebar-help"
          title="Справка"
          aria-label="Справка"
          onClick={() => setHelp(true)}
        >
          <CircleHelp size={18} strokeWidth={1.7} />
          <span>Справка</span>
        </button>
      </aside>
      <main className="work" ref={main}>
        <div className="content">
          {route === 'settings' ? (
            <Settings book={book} />
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
              : 'Рабочий журнал и фотографии хранятся на локальной ноде.'}
          </p>
        </Modal>
      )}
    </div>
  );
}
