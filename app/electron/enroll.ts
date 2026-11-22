// Логика first-run/enrollment локальной ноды (ADR-0014). Чистые функции —
// без Electron-зависимостей, покрываются vitest-тестами (tests/enroll-host.test.ts).
//
// Инварианты:
//  - dev-режим (npm start): бандла ноды нет → старый поток с отдельно
//    запущенной нодой на 127.0.0.1:8080 не меняется;
//  - если на 127.0.0.1:8080 уже отвечает чья-то нода — используем её
//    (старый режим), свой процесс не спавним;
//  - enrollment выполняет бинарь ноды (`node enroll`), TS только
//    оркестрирует: статус → (токен?) → enroll → спавн web.

export interface CertStatus {
  enrolled: boolean;
  node_id?: string;
  not_after?: string;
  needs_reenroll: boolean;
  reason?: string;
}

/** Что делать хосту на этом запуске. */
export type NodePlan =
  | { kind: 'skipped'; reason: string } // нет бандла ноды — старый режим
  | { kind: 'reuse-running' } // 8080 уже занят живой нодой
  | { kind: 'spawn'; token?: string; enroll: boolean };

/**
 * Разбирает stdout `node enroll --status` в CertStatus. Терпимо к мусору
 * вокруг JSON (логи в stderr не попадают, но подстраховываемся).
 */
export function parseStatus(stdout: string): CertStatus {
  const start = stdout.indexOf('{');
  if (start < 0) throw new Error('enroll: статус не содержит JSON');
  const parsed = JSON.parse(stdout.slice(start)) as Partial<CertStatus>;
  return {
    enrolled: parsed.enrolled === true,
    node_id: typeof parsed.node_id === 'string' ? parsed.node_id : undefined,
    not_after: typeof parsed.not_after === 'string' ? parsed.not_after : undefined,
    needs_reenroll: parsed.needs_reenroll === true,
    reason: typeof parsed.reason === 'string' ? parsed.reason : undefined,
  };
}

export interface PlanInput {
  /** Бинарник ноды найден в ресурсах приложения? */
  nodeBinary: string | null;
  /** 127.0.0.1:8080 уже отвечает /health 200? */
  healthOk: boolean;
  /** Статус серта (если бинарник есть и статус прочитан). */
  status: CertStatus | null;
  /** Сохранённый токен прошлой успешной enrollment (если есть). */
  storedToken: string | null;
}

/**
 * decidePlan — что делает хост. Чистая функция для тестов.
 *
 * Пере-enroll с сохранённым токеном выполняется молча; диалог ввода токена
 * открывается только если storedToken нет (первый запуск) — тогда
 * plan.enroll = true и token = undefined: хост спросит пользователя.
 */
export function decidePlan(input: PlanInput): NodePlan {
  if (!input.nodeBinary) {
    return { kind: 'skipped', reason: 'бинарник ноды неbundled — старый режим' };
  }
  if (input.healthOk) {
    return { kind: 'reuse-running' };
  }
  const needsEnroll = !input.status || input.status.needs_reenroll;
  if (!needsEnroll) {
    return { kind: 'spawn', enroll: false };
  }
  if (input.storedToken) {
    return { kind: 'spawn', enroll: true, token: input.storedToken };
  }
  return { kind: 'spawn', enroll: true };
}

/** Среда для дочернего процесса ноды — единый источник с electron/local-node.cjs. */
export interface NodeEnvInput {
  /** Каталог данных ноды (userData/journal). */
  dataDir: string;
  /** Каталог бандла ноды (resources/node). */
  nodeResourceDir: string;
  /** Каталог миграций SQL (resources/node/migrations). */
  migrationsDir: string;
  /** Платформа — от неё зависит имя onnxruntime-библиотеки. */
  platform: string;
  /** Пути к сертификатам — только когда enrollment уже выполнен. */
  certPaths?: { cert: string; key: string; ca: string };
  /** Синк с central (true — только при живом серте). */
  syncEnabled: boolean;
  /** central URL (по умолчанию — https://yc-hack-horses.cu3rd.ru). */
  centralUrl?: string;
}

/** buildNodeEnv — переменные окружения ноды (конфиг полностью через env). */
export function buildNodeEnv(input: NodeEnvInput): Record<string, string> {
  const env: Record<string, string> = {
    DATA_DIR: input.dataDir,
    STORAGE_DRIVER: 'sqlite',
    NODE_DSN: `${input.dataDir}/node.db`,
    LISTEN_ADDR: '127.0.0.1:8080',
    GRPC_LISTEN_ADDR: '127.0.0.1:0',
    MDNS_ENABLED: 'false',
    SYNC_ENABLED: input.syncEnabled ? 'true' : 'false',
    SYNC_CENTRAL_URL: input.centralUrl ?? 'https://yc-hack-horses.cu3rd.ru',
    INFER_ENGINE: 'yolo',
    INFER_REID_MODEL_DIR: `${input.dataDir}/models`,
    INFER_RUNTIME_LIB: `${input.nodeResourceDir}/${
      input.platform === 'darwin' ? 'libonnxruntime.dylib' : 'libonnxruntime.so'
    }`,
    MIGRATIONS_DIR: input.migrationsDir,
  };
  if (input.certPaths) {
    env.CERT_FILE = input.certPaths.cert;
    env.KEY_FILE = input.certPaths.key;
    env.CA_FILE = input.certPaths.ca;
  }
  return env;
}

/** Пути файлов серта внутри каталога данных ноды (как пишет `node enroll`). */
export function certPathsIn(dataDir: string): { cert: string; key: string; ca: string } {
  return {
    cert: `${dataDir}/cert.pem`,
    key: `${dataDir}/key.pem`,
    ca: `${dataDir}/ca.pem`,
  };
}

/** Сертификат жив? (грубая проверка срока из статуса — для отображения). */
export function certExpiresWithin(status: CertStatus, days: number, now = new Date()): boolean {
  if (!status.not_after) return true;
  const notAfter = new Date(status.not_after);
  if (Number.isNaN(notAfter.getTime())) return true;
  return notAfter.getTime() - now.getTime() < days * 24 * 60 * 60 * 1000;
}
