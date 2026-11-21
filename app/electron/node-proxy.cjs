// Fixed loopback destination. The renderer accesses only journal://app/api/v1/...
const { net } = require('electron');
const routes = {
  GET: /^\/(?:node|horses|observations|blobs(?:\/[0-9a-f]{64})?)$/,
  POST: /^\/(?:blobs|observations|comparisons|comparisons\/[\w-]+\/cancel)$/,
  PUT: /^\/comparisons\/[\w-]+\/detections\/[\w-]+\/decision$/,
};
module.exports = async function requestNode(request, url) {
  const path = url.pathname.slice('/api/v1'.length);
  if (!routes[request.method]?.test(path)) return new Response(null, { status: 404 });
  const headers = new Headers();
  const type = request.headers.get('content-type');
  if (type) headers.set('content-type', type);
  try {
    const upstream = await net.fetch('http://127.0.0.1:8080' + url.pathname + url.search, {
      method: request.method,
      headers,
      body: request.body ? await request.arrayBuffer() : undefined,
      credentials: 'omit',
      redirect: 'error',
      signal: request.signal,
    });
    return new Response(upstream.body, {
      status: upstream.status,
      headers: {
        'Content-Type': upstream.headers.get('content-type') || 'application/octet-stream',
        'Content-Security-Policy': "default-src 'none'",
        'X-Content-Type-Options': 'nosniff',
        'Cache-Control': 'no-store',
      },
    });
  } catch {
    return Response.json({ message: 'Локальная нода недоступна' }, { status: 502 });
  }
};
