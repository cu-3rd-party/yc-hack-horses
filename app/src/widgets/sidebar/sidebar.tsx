// Sidebar навигация приложения.
import { NavLink, useLocation } from 'react-router-dom';
import { useCallback, useEffect, useState } from 'react';
import { LayoutDashboard, Squirrel, ClipboardList, RefreshCw, Settings, Plus } from 'lucide-react';
import { cn } from '../../shared/lib/utils';
import { Badge } from '../../shared/ui/badge';
import { useOfflineQueue } from '../../features/offline-queue/model/use-offline-queue';
import { executeQueuedAction } from '../../features/offline-queue/model/executors';

const items = [
  { to: '/', label: 'Dashboard', icon: LayoutDashboard },
  { to: '/horses', label: 'Horses', icon: Squirrel },
  { to: '/observations', label: 'Observations', icon: ClipboardList },
  { to: '/new-observation', label: 'New Observation', icon: Plus },
  { to: '/sync', label: 'Sync', icon: RefreshCw },
  { to: '/settings', label: 'Settings', icon: Settings },
];

export function Sidebar() {
  const { pendingCount, flush, refresh } = useOfflineQueue();
  const [flushing, setFlushing] = useState(false);
  const location = useLocation();

  const doFlush = useCallback(() => {
    setFlushing(true);
    flush(executeQueuedAction).finally(() => {
      setFlushing(false);
      refresh();
    });
  }, [flush, refresh]);

  // При смене экрана пробуем выгрузить очередь — нода могла подняться.
  useEffect(() => {
    if (pendingCount > 0 && !flushing) doFlush();
  }, [location.pathname]);

  return (
    <aside className="flex w-52 shrink-0 flex-col border-r border-border bg-card/50 p-3">
      <div className="mb-6 px-2 text-sm font-semibold tracking-wide">yc-hack-horses</div>
      <nav className="flex flex-1 flex-col gap-1">
        {items.map(({ to, label, icon: Icon }) => (
          <NavLink
            key={to}
            to={to}
            end={to === '/'}
            className={({ isActive }) =>
              cn(
                'flex items-center gap-2 rounded-md px-3 py-2 text-sm text-muted-foreground hover:bg-accent hover:text-foreground',
                isActive && 'bg-accent text-foreground',
              )
            }
          >
            <Icon size={16} />
            {label}
          </NavLink>
        ))}
      </nav>
      {pendingCount > 0 && (
        <div className="mt-2 flex flex-col gap-1 px-2">
          <Badge variant="warning">Офлайн-очередь: {pendingCount}</Badge>
          <button
            className="text-left text-xs text-muted-foreground hover:text-foreground"
            disabled={flushing}
            onClick={doFlush}
          >
            {flushing ? 'Отправка...' : 'Отправить сейчас'}
          </button>
        </div>
      )}
    </aside>
  );
}
