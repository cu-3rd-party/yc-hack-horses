// Хост ноды (ADR-0014): планирование запуска и сборка окружения — чистая
// логика из electron/enroll.ts, компилируется tsc -p tsconfig.electron.json.
import { readFileSync } from 'node:fs';
import { describe, expect, it } from 'vitest';
import { execFileSync } from 'node:child_process';
import {
  buildNodeEnv,
  certExpiresWithin,
  certPathsIn,
  decidePlan,
  parseStatus,
  type CertStatus,
} from '../electron/enroll';

const compile = () => {
  const cwd = new URL('..', import.meta.url).pathname;
  execFileSync('npx', ['tsc', '-p', 'tsconfig.electron.json'], {
    cwd,
    stdio: 'pipe',
  });
  // tsc выдаёт enroll.js; main.cjs требует build/enroll.cjs — тот же renamer,
  // что и в npm run build (scripts/electron-cjs.cjs), иначе свежая checkout
  // без предшествующего build падает на чтении .cjs.
  execFileSync('node', ['scripts/electron-cjs.cjs'], { cwd, stdio: 'pipe' });
};

const status = (over: Partial<CertStatus> = {}): CertStatus => ({
  enrolled: true,
  node_id: '0198a7b4-aaaa-7bbb-8ccc-ddddeee00001',
  not_after: new Date(Date.now() + 200 * 24 * 3600 * 1000).toISOString(),
  needs_reenroll: false,
  ...over,
});

describe('parseStatus', () => {
  it('читает JSON из stdout node enroll --status', () => {
    expect(parseStatus('{"enrolled":true,"node_id":"n1","needs_reenroll":false}')).toMatchObject({
      enrolled: true,
      node_id: 'n1',
      needs_reenroll: false,
    });
  });
  it('теряет выдержку мусора до JSON', () => {
    expect(parseStatus('pre {"enrolled":false,"needs_reenroll":true}')).toMatchObject({
      enrolled: false,
      needs_reenroll: true,
    });
  });
  it('падает на stdout без JSON', () => {
    expect(() => parseStatus('no json here')).toThrow();
  });
});

describe('decidePlan', () => {
  it('нет бандла → старый режим (skipped)', () => {
    expect(
      decidePlan({ nodeBinary: null, healthOk: false, status: null, storedToken: null }),
    ).toEqual({ kind: 'skipped', reason: expect.any(String) });
  });
  it('8080 уже отвечает живой нодой → reuse-running', () => {
    expect(
      decidePlan({ nodeBinary: '/x/node', healthOk: true, status: null, storedToken: null }),
    ).toEqual({ kind: 'reuse-running' });
  });
  it('серт жив → spawn без enrollment', () => {
    expect(
      decidePlan({
        nodeBinary: '/x/node',
        healthOk: false,
        status: status(),
        storedToken: 'tok',
      }),
    ).toEqual({ kind: 'spawn', enroll: false });
  });
  it('пере-enroll с сохранённым токеном — молча', () => {
    const plan = decidePlan({
      nodeBinary: '/x/node',
      healthOk: false,
      status: status({ needs_reenroll: true, reason: 'истёк' }),
      storedToken: 'tok',
    });
    expect(plan).toEqual({ kind: 'spawn', enroll: true, token: 'tok' });
  });
  it('первый запуск: enrollment с диалогом (token решает хост)', () => {
    const plan = decidePlan({
      nodeBinary: '/x/node',
      healthOk: false,
      status: status({ enrolled: false, needs_reenroll: true, reason: 'сертификат не найден' }),
      storedToken: null,
    });
    expect(plan).toEqual({ kind: 'spawn', enroll: true });
  });
  it('первый запуск без статуса (нет данных enroll) — тоже enrollment', () => {
    expect(
      decidePlan({ nodeBinary: '/x/node', healthOk: false, status: null, storedToken: null }),
    ).toEqual({ kind: 'spawn', enroll: true });
  });
});

describe('buildNodeEnv', () => {
  it('конфиг ноды полностью через env, loopback-адреса', () => {
    const env = buildNodeEnv({
      dataDir: '/u/journal',
      nodeResourceDir: '/res/node',
      migrationsDir: '/res/node/migrations',
      platform: 'linux',
      certPaths: certPathsIn('/u/journal'),
      syncEnabled: true,
      centralUrl: 'https://yc-hack-horses.cu3rd.ru',
    });
    expect(env.LISTEN_ADDR).toBe('127.0.0.1:8080');
    expect(env.GRPC_LISTEN_ADDR).toBe('127.0.0.1:0');
    expect(env.STORAGE_DRIVER).toBe('sqlite');
    expect(env.NODE_DSN).toBe('/u/journal/node.db');
    expect(env.GRPC_LISTEN_ADDR).toBe('127.0.0.1:0');
    expect(env.SYNC_CENTRAL_URL).toBe('https://yc-hack-horses.cu3rd.ru');
    expect(env.INFER_ENGINE).toBe('yolo');
    expect(env.INFER_MODEL_PATH).toBe('/u/journal/models/yolov8n.onnx');
    expect(env.INFER_REID_MODEL_DIR).toBe('/u/journal/models');
    expect(env.INFER_RUNTIME_LIB).toBe('/res/node/libonnxruntime.so');
    expect(env.MIGRATIONS_DIR).toBe('/res/node/migrations');
    expect(env.MDNS_ENABLED).toBe('false');
    expect(env.CERT_FILE).toBe('/u/journal/cert.pem');
    expect(env.KEY_FILE).toBe('/u/journal/key.pem');
    expect(env.CA_FILE).toBe('/u/journal/ca.pem');
  });
  it('без сертификатов env не содержит CERT_FILE, sync выключен (plain-режим)', () => {
    const env = buildNodeEnv({
      dataDir: '/u/journal',
      nodeResourceDir: '/r',
      migrationsDir: '/r/m',
      platform: 'linux',
      syncEnabled: false,
    });
    expect(env.CERT_FILE).toBeUndefined();
    expect(env.KEY_FILE).toBeUndefined();
    expect(env.CA_FILE).toBeUndefined();
    expect(env.SYNC_ENABLED).toBe('false');
  });
  it('darwin: onnxruntime-библиотека .dylib', () => {
    const env = buildNodeEnv({
      dataDir: '/u/journal',
      nodeResourceDir: '/r',
      migrationsDir: '/r/m',
      platform: 'darwin',
      syncEnabled: false,
    });
    expect(env.INFER_RUNTIME_LIB).toBe('/r/libonnxruntime.dylib');
  });
});

describe('certExpiresWithin', () => {
  it('не истекает в ближайшие 7 дней → false', () => {
    expect(certExpiresWithin(status(), 7)).toBe(false);
  });
  it('истекает в ближайшие 7 дней → true', () => {
    expect(
      certExpiresWithin(status({ not_after: new Date(Date.now() + 3 * 86400_000).toISOString() }), 7),
    ).toBe(true);
  });
  it('нет данных срока → true (осторожная сторона)', () => {
    expect(certExpiresWithin(status({ not_after: undefined }), 7)).toBe(true);
  });
});

describe('compile', () => {
  it('electron/enroll.ts компилируется в build/enroll.cjs', () => {
    compile();
    const compiled = readFileSync(new URL('../electron/build/enroll.cjs', import.meta.url), 'utf8');
    expect(compiled).toContain('decidePlan');
  });
});
