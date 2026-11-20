// Dashboard: статус ноды, синхронизации, счётчики, central-соединение.
import { useMemo } from 'react';
import { Card, CardHeader, CardTitle, CardContent } from '../../shared/ui/card';
import { Badge } from '../../shared/ui/badge';
import { NodeStateBanner } from '../../shared/ui/node-state-banner';
import { useNodeResource, useNodeEvents, isNodeUnavailable } from '../../shared/api/hooks';
import { getNodeStatus, getHorses, getObservations } from '../../shared/api/endpoints';
import { formatRelative, formatBytes } from '../../shared/lib/utils';

function StatCard({ title, value, sub }: { title: string; value: string; sub?: string }) {
  return (
    <Card>
      <CardHeader><CardTitle>{title}</CardTitle></CardHeader>
      <CardContent>
        <div className="text-2xl font-semibold">{value}</div>
        {sub && <div className="mt-1 text-xs text-muted-foreground">{sub}</div>}
      </CardContent>
    </Card>
  );
}

export function DashboardPage() {
  const node = useNodeResource(getNodeStatus);
  const horses = useNodeResource(() => getHorses({ limit: 1 }));
  const observations = useNodeResource(() => getObservations({ limit: 1 }));

  const wsStatus = useNodeEvents(() => {
    // События стрима в V0 просто подтверждают живость соединения.
  });

  const centralPeer = useMemo(
    () =>
      node.data?.peers?.find(
        (p) => p.peer_id.includes('central') || p.peer_id.includes('yc-hack-horses.cu3rd.ru'),
      ),
    [node.data],
  );

  const offline = isNodeUnavailable(node.error);

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Dashboard</h1>
        <NodeStateBanner loading={node.loading} error={node.error} />
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Нода</CardTitle>
          {offline ? <Badge variant="warning">offline</Badge> : <Badge variant="success">online</Badge>}
        </CardHeader>
        <CardContent>
          {node.loading ? (
            <p className="text-muted-foreground">Загрузка статуса ноды...</p>
          ) : node.data ? (
            <dl className="grid grid-cols-2 gap-y-2 text-sm">
              <dt className="text-muted-foreground">Node ID</dt>
              <dd className="font-mono">{node.data.node_id}</dd>
              <dt className="text-muted-foreground">Роль</dt>
              <dd>{node.data.role}</dd>
              <dt className="text-muted-foreground">Версия</dt>
              <dd>{node.data.version ?? '—'}</dd>
              <dt className="text-muted-foreground">Uptime</dt>
              <dd>{node.data.uptime_s != null ? `${node.data.uptime_s} с` : '—'}</dd>
            </dl>
          ) : (
            <p className="text-muted-foreground">Нода недоступна. Приложение работает офлайн.</p>
          )}
        </CardContent>
      </Card>

      <div className="grid grid-cols-3 gap-4">
        <StatCard
          title="Животные"
          value={offline ? '—' : String(horses.data?.total ?? horses.data?.horses.length ?? 0)}
        />
        <StatCard
          title="Наблюдения"
          value={
            offline ? '—' : String(observations.data?.total ?? observations.data?.observations.length ?? 0)
          }
        />
        <StatCard
          title="Pending events"
          value={offline ? '—' : String(node.data?.storage?.pending_events ?? 0)}
          sub={
            node.data?.storage?.size_bytes != null
              ? `Хранилище: ${formatBytes(node.data.storage.size_bytes)}`
              : undefined
          }
        />
      </div>

      <div className="grid grid-cols-2 gap-4">
        <Card>
          <CardHeader>
            <CardTitle>Central-соединение</CardTitle>
            <Badge variant={centralPeer?.state === 'connected' ? 'success' : 'muted'}>
              {centralPeer?.state ?? 'нет данных'}
            </Badge>
          </CardHeader>
          <CardContent>
            <div className="text-sm text-muted-foreground">
              Last sync: {formatRelative(centralPeer?.last_sync_at)}
            </div>
            {centralPeer?.last_error && (
              <div className="mt-1 text-xs text-red-400">{centralPeer.last_error}</div>
            )}
          </CardContent>
        </Card>
        <Card>
          <CardHeader>
            <CardTitle>Стрим событий (WS)</CardTitle>
            <Badge
              variant={wsStatus === 'online' ? 'success' : wsStatus === 'connecting' ? 'muted' : 'warning'}
            >
              {wsStatus}
            </Badge>
          </CardHeader>
          <CardContent className="text-sm text-muted-foreground">
            {wsStatus === 'online'
              ? 'События ноды доставляются в реальном времени.'
              : wsStatus === 'offline'
                ? 'Соединение потеряно, переподключение.'
                : 'Подключение к /api/v1/events.'}
          </CardContent>
        </Card>
      </div>
    </div>
  );
}
