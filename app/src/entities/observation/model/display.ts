import type { Observation } from '@yc-hack-horses/contracts';

export function formatLocation(loc: Observation['location']): string {
  if (!loc) return '—';
  const lat = loc.lat.toFixed(5);
  const lon = loc.lon.toFixed(5);
  return loc.accuracy_m != null ? `${lat}, ${lon} (±${loc.accuracy_m} м)` : `${lat}, ${lon}`;
}

export function observationTargetLabel(obs: Observation): string {
  if (obs.horse_id) return `Лошадь ${obs.horse_id.slice(0, 8)}`;
  return 'Неизвестная особь';
}
