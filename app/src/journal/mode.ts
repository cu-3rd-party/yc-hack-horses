import { useSyncExternalStore } from 'react';

const preference = 'loshid.demo-scenarios';
const change = new EventTarget();
function subscribe(listener: () => void) {
  change.addEventListener('change', listener);
  window.addEventListener('storage', listener);
  return () => {
    change.removeEventListener('change', listener);
    window.removeEventListener('storage', listener);
  };
}
export function useDemo() {
  return useSyncExternalStore(subscribe, () => localStorage.getItem(preference) === 'on');
}
export function setDemo(enabled: boolean) {
  localStorage.setItem(preference, enabled ? 'on' : 'off');
  change.dispatchEvent(new Event('change'));
}
