import type { Horse } from '@yc-hack-horses/contracts';

// Отображаемое имя лошади: из metadata.name/nickname, иначе "Лошадь <id>".
export function horseDisplayName(horse: Horse): string {
  const name = horse.metadata?.name || horse.metadata?.nickname;
  return name && name.length > 0 ? name : `Лошадь ${horse.id.slice(0, 8)}`;
}

export const identityStatusLabel: Record<Horse['identity_status'], string> = {
  known: 'Известная',
  unknown: 'Неизвестная',
  provisional: 'Предварительно',
};

export const identityStatusVariant: Record<
  Horse['identity_status'],
  'success' | 'muted' | 'warning'
> = {
  known: 'success',
  unknown: 'muted',
  provisional: 'warning',
};
