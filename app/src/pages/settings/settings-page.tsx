// Settings: node id, статус сертификата/доверия, хранилище.
// Приватные ключи не отображаются и не экспортируются.
import { Card, CardHeader, CardTitle, CardContent } from '../../shared/ui/card';
import { Badge } from '../../shared/ui/badge';
import { NodeStateBanner } from '../../shared/ui/node-state-banner';
import { useNodeResource } from '../../shared/api/hooks';
import { getNodeStatus } from '../../shared/api/endpoints';
import { formatBytes, formatRelative } from '../../shared/lib/utils';

export function SettingsPage() {
  const node = useNodeResource(getNodeStatus);
  const cert = node.data?.certificate;
  const storage = node.data?.storage;

  return (
    <div className="flex max-w-2xl flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Settings</h1>
        <NodeStateBanner loading={node.loading} error={node.error} />
      </div>

      <Card>
        <CardHeader><CardTitle>Нода</CardTitle></CardHeader>
        <CardContent>
          <dl className="grid grid-cols-2 gap-y-2 text-sm">
            <dt className="text-muted-foreground">Node ID</dt>
            <dd className="font-mono">{node.data?.node_id ?? '—'}</dd>
            <dt className="text-muted-foreground">Роль</dt>
            <dd>{node.data?.role ?? '—'}</dd>
            <dt className="text-muted-foreground">Версия</dt>
            <dd>{node.data?.version ?? '—'}</dd>
          </dl>
        </CardContent>
      </Card>

      <Card>
        <CardHeader>
          <CardTitle>Сертификат и доверие</CardTitle>
          {cert ? (
            <Badge variant={cert.valid && cert.trust_anchor_ok ? 'success' : 'warning'}>
              {cert.valid && cert.trust_anchor_ok ? 'доверенный' : 'проверка не пройдена'}
            </Badge>
          ) : (
            <Badge variant="muted">нет данных</Badge>
          )}
        </CardHeader>
        <CardContent className="text-sm">
          <div>Subject: {cert?.subject ?? '—'}</div>
          <div>Истекает: {formatRelative(cert?.expires_at ?? null)}</div>
          <div className="mt-1 text-xs text-muted-foreground">
            Приватные ключи хранятся на ноде и в интерфейсе не отображаются.
          </div>
        </CardContent>
      </Card>

      <Card>
        <CardHeader><CardTitle>Хранилище</CardTitle></CardHeader>
        <CardContent className="text-sm">
          <div>Движок: {storage?.engine ?? '—'}</div>
          <div>Размер: {storage?.size_bytes != null ? formatBytes(storage.size_bytes) : '—'}</div>
          <div>
            Pending: {storage?.pending_events ?? 0} · Применено: {storage?.applied_events ?? 0} ·
            Ошибок: {storage?.errors ?? 0}
          </div>
        </CardContent>
      </Card>
    </div>
  );
}
