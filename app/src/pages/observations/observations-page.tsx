// Observations: список наблюдений.
import { useState } from 'react';
import type { Observation } from '@yc-hack-horses/contracts';
import { Card, CardHeader, CardTitle, CardContent } from '../../shared/ui/card';
import { Badge } from '../../shared/ui/badge';
import { NodeStateBanner } from '../../shared/ui/node-state-banner';
import { useNodeResource } from '../../shared/api/hooks';
import { getObservations } from '../../shared/api/endpoints';
import { formatRelative } from '../../shared/lib/utils';
import { formatLocation, observationTargetLabel } from '../../entities/observation/model/display';

function ObservationRow({ obs }: { obs: Observation }) {
  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{observationTargetLabel(obs)}</CardTitle>
          <div className="mt-1 text-xs text-muted-foreground">
            Наблюдатель: {obs.observer_id} · нода: {obs.node_id}
          </div>
        </div>
        <Badge variant={obs.horse_id ? 'success' : 'muted'}>
          {obs.horse_id ? 'привязано' : 'unknown'}
        </Badge>
      </CardHeader>
      <CardContent className="text-xs text-muted-foreground">
        {formatRelative(obs.observed_at)} · {formatLocation(obs.location)} · блобов:{' '}
        {obs.blob_ids.length}
      </CardContent>
    </Card>
  );
}

export function ObservationsPage() {
  const observations = useNodeResource(() => getObservations({ limit: 100 }));
  const [onlyUnlinked, setOnlyUnlinked] = useState(false);

  const list = (observations.data?.observations ?? []).filter(
    (o) => !onlyUnlinked || !o.horse_id,
  );

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Observations</h1>
        <NodeStateBanner loading={observations.loading} error={observations.error} />
      </div>
      <label className="flex items-center gap-2 text-sm text-muted-foreground">
        <input
          type="checkbox"
          checked={onlyUnlinked}
          onChange={(e) => setOnlyUnlinked(e.target.checked)}
        />
        Только без привязки к лошади
      </label>
      <div className="flex flex-col gap-2">
        {list.map((o) => (
          <ObservationRow key={o.id} obs={o} />
        ))}
      </div>
      {observations.data?.observations?.length === 0 && !observations.loading && (
        <Card>
          <CardContent className="text-muted-foreground">Наблюдений пока нет.</CardContent>
        </Card>
      )}
    </div>
  );
}
