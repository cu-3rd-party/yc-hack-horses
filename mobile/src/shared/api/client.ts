// Typed fetch wrapper к локальной ноде — портирован из app/src/shared/api/client.ts.
// Отличия от веб-версии: без new URL() (нет URL-парсера в Hermes) и uploadBlob
// принимает RN-ресурс { uri, name, type } вместо File. Ошибки нормализуются
// в ApiError / NodeUnavailableError так же, как в web-приложении.
export class ApiError extends Error {
  readonly status: number;
  readonly body: unknown;

  constructor(message: string, status: number, body: unknown = null) {
    super(message);
    this.name = 'ApiError';
    this.status = status;
    this.body = body;
  }
}

export class NodeUnavailableError extends Error {
  constructor(cause: unknown) {
    super('Локальная нода недоступна');
    this.name = 'NodeUnavailableError';
    this.cause = cause;
  }
}

export interface RequestOptions {
  method?: 'GET' | 'POST' | 'PATCH' | 'DELETE';
  body?: unknown;
  query?: Record<string, string | number | undefined>;
  signal?: AbortSignal;
  timeoutMs?: number;
}

export interface FileResource {
  uri: string;
  name: string;
  type: string;
}

function buildUrl(base: string, path: string, query?: RequestOptions['query']): string {
  let url = base.endsWith('/') ? base : `${base}/`;
  url += path.replace(/^\//, '');
  if (query) {
    const parts: string[] = [];
    for (const [k, v] of Object.entries(query)) {
      if (v !== undefined) parts.push(`${encodeURIComponent(k)}=${encodeURIComponent(String(v))}`);
    }
    if (parts.length > 0) url += `?${parts.join('&')}`;
  }
  return url;
}

async function withTimeout(
  fetchFn: typeof fetch,
  url: string,
  init: RequestInit,
  timeoutMs: number,
): Promise<Response> {
  const ctrl = new AbortController();
  const timer = setTimeout(() => ctrl.abort(), timeoutMs);
  const signal = init.signal;
  const merged: RequestInit = {
    ...init,
    signal: signal ? anySignal([signal, ctrl.signal]) : ctrl.signal,
  };
  try {
    return await fetchFn(url, merged);
  } finally {
    clearTimeout(timer);
  }
}

function anySignal(signals: AbortSignal[]): AbortSignal {
  const ctrl = new AbortController();
  for (const s of signals) {
    if (s.aborted) {
      ctrl.abort();
      break;
    }
    s.addEventListener('abort', () => ctrl.abort(), { once: true });
  }
  return ctrl.signal;
}

export class ApiClient {
  constructor(
    private readonly baseUrl: string,
    private readonly fetchFn: typeof fetch = fetch,
  ) {}

  async request<T>(path: string, opts: RequestOptions = {}): Promise<T> {
    const { method = 'GET', body, query, signal, timeoutMs = 5000 } = opts;
    const url = buildUrl(this.baseUrl, path, query);
    let res: Response;
    try {
      res = await withTimeout(this.fetchFn, url, {
        method,
        headers: body !== undefined ? { 'Content-Type': 'application/json' } : undefined,
        body: body !== undefined ? JSON.stringify(body) : undefined,
        signal,
      }, timeoutMs);
    } catch (err) {
      throw new NodeUnavailableError(err);
    }
    if (!res.ok) {
      let parsed: unknown = null;
      try {
        parsed = await res.json();
      } catch {
        // тело не JSON — оставляем null
      }
      throw new ApiError(`HTTP ${res.status} ${method} ${path}`, res.status, parsed);
    }
    if (res.status === 204) return undefined as T;
    return (await res.json()) as T;
  }

  /** Загрузка файла на /api/v1/blobs (multipart). RN-версия FormData принимает ресурс-файл. */
  async uploadBlob(file: FileResource): Promise<{ id?: string; sha256: string }> {
    const url = buildUrl(this.baseUrl, '/api/v1/blobs');
    const form = new FormData();
    // RN FormData: часть с uri/name/type уходит как файл; web-часть не используется.
    form.append('file', {
      uri: file.uri,
      name: file.name,
      type: file.type,
    } as unknown as Parameters<FormData['append']>[1]);
    let res: Response;
    try {
      res = await withTimeout(this.fetchFn, url, { method: 'POST', body: form }, 30000);
    } catch (err) {
      throw new NodeUnavailableError(err);
    }
    if (!res.ok) throw new ApiError(`HTTP ${res.status} POST /api/v1/blobs`, res.status, null);
    return (await res.json()) as { id?: string; sha256: string };
  }

  get<T>(path: string, opts?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
    return this.request<T>(path, { ...opts, method: 'GET' });
  }

  post<T>(path: string, body: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
    return this.request<T>(path, { ...opts, method: 'POST', body });
  }

  patch<T>(path: string, body: unknown, opts?: Omit<RequestOptions, 'method' | 'body'>): Promise<T> {
    return this.request<T>(path, { ...opts, method: 'PATCH', body });
  }
}
