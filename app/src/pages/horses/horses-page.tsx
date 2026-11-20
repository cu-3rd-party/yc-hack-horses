// Horses: список карточек животных с деталями и историей наблюдений.
import { useState } from 'react';
import { Link } from 'react-router-dom';
import type { Horse, Observation } from '@yc-hack-horses/contracts';
import { Card, CardHeader, CardTitle, CardContent } from '../../shared/ui/card';
import { Badge } from '../../shared/ui/badge';
import { Input } from '../../shared/ui/input';
import { NodeStateBanner } from '../../shared/ui/node-state-banner';
import { useNodeResource } from '../../shared/api/hooks';
import { getHorses, getObservations } from '../../shared/api/endpoints';
import { horseDisplayName, identityStatusLabel, identityStatusVariant } from '../../entities/horse/model/display';
import { formatRelative } from '../../shared/lib/utils';
import { formatLocation, observationTargetLabel } from '../../entities/observation/model/display';

function HorseCard({ horse }: { horse: Horse }) {
  const [open, setOpen] = useState(false);
  const obs = useNodeResource(
    () => getObservations({ horse_id: horse.id, limit: 10 }),
    [horse.id],
  );

  return (
    <Card>
      <CardHeader>
        <div>
          <CardTitle>{horseDisplayName(horse)}</CardTitle>
          <div className="mt-1 text-xs text-muted-foreground">ID: {horse.id}</div>
        </div>
        <Badge variant={identityStatusVariant[horse.identity_status]}>
          {identityStatusLabel[horse.identity_status]}
        </Badge>
      </CardHeader>
      <CardContent>
        <dl className="grid grid-cols-2 gap-y-1 text-sm">
          {Object.entries(horse.metadata ?? {})
            .filter(([, v]) => v !== null && v !== undefined && v !== '')
            .slice(0, 6)
            .map(([k, v]) => (
              <div key={k} className="col-span-2 flex justify-between gap-4">
                <dt className="text-muted-foreground">{k}</dt>
                <dd>{String(v)}</dd>
              </div>
            ))}
        </dl>
        <div className="mt-3">
          <button
            className="text-xs text-primary hover:underline"
            onClick={() => setOpen((v) => !v)}
          >
            {open ? 'Скрыть наблюдения' : 'История наблюдений'}
          </button>
          {open && (
            <div className="mt-2 flex flex-col gap-2 text-sm">
              {obs.loading && <span className="text-muted-foreground">Загрузка...</span>}
              {obs.error && <span className="text-red-400">{obs.error.message}</span>}
              {obs.data?.observations?.length === 0 && (
                <span className="text-muted-foreground">Наблюдений пока нет.</span>
              )}
              {obs.data?.observations?.map((o: Observation) => (
                <div key={o.id} className="rounded-md border border-border p-2">
                  <div className="flex justify-between">
                    <span>{formatRelative(o.observed_at)}</span>
                    <span className="text-muted-foreground">{observationTargetLabel(o)}</span>
                  </div>
                  <div className="text-xs text-muted-foreground">
                    {formatLocation(o.location)} · блобов: {o.blob_ids.length}
                  </div>
                </div>
              ))}
            </div>
          )}
        </div>
      </CardContent>
    </Card>
  );
}

export function HorsesPage() {
  const horses = useNodeResource(() => getHorses({ limit: 100 }));
  const [filter, setFilter] = useState('');

  const filtered = (horses.data?.horses ?? []).filter((h) => {
    if (!filter) return true;
    const name = horseDisplayName(h).toLowerCase();
    return name.includes(filter.toLowerCase()) || h.id.includes(filter);
  });

  return (
    <div className="flex flex-col gap-4">
      <div className="flex items-center justify-between">
        <h1 className="text-lg font-semibold">Horses</h1>
        <NodeStateBanner loading={horses.loading} error={horses.error} />
      </div>
      <Input
        placeholder="Поиск по имени или ID"
        value={filter}
        onChange={(e) => setFilter(e.target.value)}
        className="max-w-sm"
      />
      {horses.data?.horses?.length === 0 && !horses.loading && (
        <Card>
          <CardContent className="text-muted-foreground">
            Животных пока нет. Создайте наблюдение с неизвестной особью — запись появится
            автоматически, или добавьте вручную на странице{' '}
            <Link to="/new-observation" className="text-primary hover:underline">
              New Observation
            </Link>
            .
          </CardContent>
        </Card>
      )}
      <div className="grid grid-cols-2 gap-4">
        {filtered.map((h) => (
          <HorseCard key={h.id} horse={h} />
        ))}
      </div>
    </div>
  );
}
