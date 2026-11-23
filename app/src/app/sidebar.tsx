import {
  Activity,
  Binoculars,
  BookOpen,
  ChevronLeft,
  ChevronRight,
  CircleHelp,
  createLucideIcon,
  RefreshCw,
  ScanLine,
  Settings2,
} from 'lucide-react';

// Horse Head from Lucide Lab, using the same SVG defaults as the sidebar icons.
const HorseIcon = createLucideIcon('horse-head', [
  ['path', { d: 'M11.5 12H11', key: 'eye' }],
  ['path', {
    d: 'M5 15a4 4 0 0 0 4 4h7.8l.3.3a3 3 0 0 0 4-4.46L12 7c0-3-1-5-1-5S8 3 8 7c-4 1-6 3-6 3',
    key: 'head',
  }],
  ['path', { d: 'M6.14 17.8S4 19 2 22', key: 'neck' }],
]);

// MPA-навигация (ADR-0015): пункты меню — настоящие ссылки на отдельные
// HTML-входы; подсветка активного пункта — по data-page текущего документа
// (и по якорю под-вкладки для страницы «Система»).
const hrefs = {
  review: '/',
  gallery: '/gallery.html',
  log: '/observations.html',
  dashboard: '/system.html#dashboard',
  sync: '/system.html#sync',
  settings: '/system.html#settings',
} as const;

const sections = [
  {
    label: 'Полевой журнал',
    items: [
      { key: 'review', label: 'Распознавание', icon: ScanLine },
      { key: 'gallery', label: 'Особи', icon: HorseIcon },
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

// Активный пункт: data-page документа, а для страницы «Система» — якорь
// под-вкладки (location.hash), чтобы подсветка следила за вкладками.
function activeKey(page: string, hash: string): Route | null {
  if (page === 'review') return 'review';
  if (page === 'gallery') return 'gallery';
  if (page === 'observations') return 'log';
  if (page === 'system') {
    const tab = hash.replace(/^#/, '');
    if (tab === 'sync' || tab === 'settings') return tab;
    return 'dashboard';
  }
  return null;
}

export function Sidebar({
  page,
  pending,
  collapsed,
  setCollapsed,
  onHelp,
}: {
  page: string;
  pending: number;
  collapsed: boolean;
  setCollapsed: (collapsed: boolean) => void;
  onHelp: () => void;
}) {
  const active = activeKey(page, window.location.hash);
  return (
    <aside className="sidebar" data-collapsed={collapsed}>
      <div className="sidebar-head">
        <a
          className="brand"
          href={hrefs.review}
          title="ЛошID — распознавание"
          aria-label="ЛошID — распознавание"
        >
          <span className="brand-icon">
            <Binoculars size={21} strokeWidth={1.7} />
          </span>
          <strong>ЛошID</strong>
        </a>
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
              <a
                key={key}
                href={hrefs[key]}
                title={label}
                aria-label={label}
                aria-current={key === active ? 'page' : undefined}
                className={key === active ? 'active' : undefined}
              >
                <Icon size={18} strokeWidth={1.7} />
                <span>{label}</span>
                {key === 'sync' && pending > 0 && <b>{pending}</b>}
              </a>
            ))}
          </div>
        ))}
      </nav>
      <button
        className="sidebar-help"
        title="Справка"
        aria-label="Справка"
        onClick={onHelp}
      >
        <CircleHelp size={18} strokeWidth={1.7} />
        <span>Справка</span>
      </button>
    </aside>
  );
}
