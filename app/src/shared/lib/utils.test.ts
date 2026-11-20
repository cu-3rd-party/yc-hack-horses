import { describe, expect, it } from 'vitest';
import { formatBytes, formatRelative } from './utils';

describe('formatBytes', () => {
  it('форматирует байты и килобайты', () => {
    expect(formatBytes(512)).toBe('512 B');
    expect(formatBytes(2048)).toBe('2.0 KB');
    expect(formatBytes(3 * 1024 * 1024)).toBe('3.0 MB');
  });
});

describe('formatRelative', () => {
  it('пустая дата — прочерк', () => {
    expect(formatRelative(null)).toBe('—');
    expect(formatRelative(undefined)).toBe('—');
  });

  it('некорректная дата возвращается как есть', () => {
    expect(formatRelative('not-a-date')).toBe('not-a-date');
  });

  it('свежая дата — секунды назад', () => {
    const iso = new Date(Date.now() - 5000).toISOString();
    expect(formatRelative(iso)).toBe('5 с назад');
  });

  it('час назад — минуты', () => {
    const iso = new Date(Date.now() - 45 * 60 * 1000).toISOString();
    expect(formatRelative(iso)).toBe('45 мин назад');
  });
});
