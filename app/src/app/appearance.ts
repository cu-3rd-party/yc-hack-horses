export const schemes = [
  { id: 'light', label: 'Светлая' },
  { id: 'dark', label: 'Тёмная' },
  { id: 'blue', label: 'Синяя' },
] as const;

export type Scheme = (typeof schemes)[number]['id'];
const preferenceKey = 'takhi.color-scheme';

export function initialScheme(): Scheme {
  return schemes.find(({ id }) => id === localStorage.getItem(preferenceKey))?.id ?? 'light';
}

export function selectScheme(scheme: Scheme) {
  document.documentElement.dataset.scheme = scheme;
  localStorage.setItem(preferenceKey, scheme);
}
