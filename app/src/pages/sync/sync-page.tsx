// Sync: список нод-пиров, статус, last sync, счётчики, ошибки, sync now.
import { useState } from 'react';
import { Button } from '../../shared/ui/button';
import { Card, CardHeader, CardTitle, CardContent } from '../../shared/ui/card';
import { Badge } from '../../shared/ui/badge';
import { NodeStateBanner } from '../../shared/ui/node-state-banner';
import { useNodeResource } from '../../shared/api/hooks';
import { getNodeStatus } from '../../shared/api/endpoints';
import { syncNow } from '../../features/sync-now/model/sync-now';
import { formatRelative } from '../../shared/lib/utils';

const peerStateVariant: Record<string, 'success' | 'muted' | 'warning' | 'error'> = {
  connected: 'success',
  syncing: 'success',
  disconnected: 'muted',
  error: 'error',
};

export function SyncPage() {
  const node = useNodeResource(getNodeStatus);
  const [syncing, setSyncing] = useState(false);
  const [syncMsg, setSyncMsg] = useState<string | null>(null);

  const onSyncNow = async () => {
    setSyncing(true);
    setSyncMsg(null);
    try {
      await syncNow();
      setSyncMsg('Синхронизация запущена.');
      node.reload();
    } catch (err) {
      setSyncMsg(
        err instanceof Error && err.name === 'NodeUnavailableError'
          ? 'Нода недоступна — офлайн-режим.'
          : `Ошибка: ${err instanceof Error ? err.message : String(err)}`,
      );
    } finally {
      setSyncing(false);
    }
  };

  const peers = node.data?.peers ?? [];

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Sync</h1>
        <NodeStateBanner loading={node.loading} error={node.error} />
      </div>

      <div className="flex items-center gap-3">
        <Button size="sm" disabled={syncing} onClick={() => void onSyncNow()}>
          {syncing ? 'Синхронизация...' : 'Sync now'}
        </Button>
        {syncMsg && <span className="text-sm text-muted-foreground">{syncMsg}</span>}
      </div>

      <Card>
        <CardHeader>
          <CardTitle>Ноды</CardTitle>
          <Badge variant="muted">{peers.length}</Badge>
        </CardHeader>
        <CardContent className="flex flex-col gap-3">
          {peers.length === 0 && !node.loading && (
            <span className="text-muted-foreground">Пиров нет или нода недоступна.</span>
          )}
          {peers.map((p) => (
            <div key={p.peer_id} className="rounded-md border border-border p-3 text-sm">
              <div className="flex items-center justify-between">
                <span className="font-mono">{p.peer_id}</span>
                <Badge variant={peerStateVariant[p.state] ?? 'muted'}>{p.state}</Badge>
              </div>
              <div className="mt-1 text-xs text-muted-foreground">
                Last sync: {formatRelative(p.last_sync_at)} · отправлено: {p.events_sent ?? 0} ·
                получено: {p.events_received ?? 0}
              </div>
              {p.last_error && <div className="mt-1 text-xs text-red-400">{p.last_error}</div>}
            </div>
          ))}
        </CardContent>
      </Card>

      {node.data?.storage && (
        <Card>
          <CardHeader><CardTitle>Очередь событий ноды</CardTitle></CardHeader>
          <CardContent className="text-sm">
            Pending: {node.data.storage.pending_events ?? 0} · Применено:{' '}
            {node.data.storage.applied_events ?? 0} · Ошибок: {node.data.storage.errors ?? 0}
          </CardContent>
        </Card>
      )}
    </div>
  );
}
