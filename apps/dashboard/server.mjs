import http from 'node:http';
import { readFile } from 'node:fs/promises';
import { fileURLToPath } from 'node:url';
import path from 'node:path';
import { randomUUID } from 'node:crypto';
import { AppError, DeviceStore, publicDevice, validateDevice } from './lib/config.mjs';
import { queryAgent } from './lib/proxy.mjs';
import { demoQuery } from './lib/demo.mjs';

const publicDir = fileURLToPath(new URL('./public/', import.meta.url));
const assets = new Map([['/', ['index.html', 'text/html']], ['/styles.css', ['styles.css', 'text/css']], ['/app.js', ['app.js', 'text/javascript']], ['/data.js', ['data.js', 'text/javascript']], ['/pagination.js', ['pagination.js', 'text/javascript']], ['/favicon.svg', ['favicon.svg', 'image/svg+xml']]]);

async function jsonBody(req) {
  if (!req.headers['content-type']?.startsWith('application/json')) throw new AppError(415, 'invalid_body', '请使用 JSON 提交连接信息。');
  let size = 0;
  const chunks = [];
  for await (const chunk of req) {
    size += chunk.length;
    if (size > 16_384) throw new AppError(413, 'body_too_large', '连接信息过长。');
    chunks.push(chunk);
  }
  try { return JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw new AppError(400, 'invalid_body', '连接信息格式无效。'); }
}

export async function createDashboard(options = {}) {
  const store = options.store || new DeviceStore();
  await store.load();
  const server = http.createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store');
    res.setHeader('X-Content-Type-Options', 'nosniff');
    res.setHeader('Referrer-Policy', 'no-referrer');
    res.setHeader('X-Frame-Options', 'DENY');
    res.setHeader('Content-Security-Policy', "default-src 'self'; script-src 'self'; style-src 'self' 'unsafe-inline'; img-src 'self' data:; connect-src 'self'; frame-ancestors 'none'; base-uri 'none'; form-action 'self'");
    const send = (status, body) => {
      res.writeHead(status, { 'Content-Type': 'application/json; charset=utf-8' });
      res.end(JSON.stringify(body));
    };
    try {
      const host = req.headers.host;
      const port = server.address()?.port;
      const allowedHosts = [`127.0.0.1:${port}`, `localhost:${port}`, `[::1]:${port}`];
      if (!allowedHosts.includes(host)) throw new AppError(403, 'host_forbidden', '请通过本机地址访问看板。');
      const origin = req.headers.origin;
      if ((origin && origin !== `http://${host}`) || req.headers['sec-fetch-site'] === 'cross-site') throw new AppError(403, 'origin_forbidden', '该访问来源未获允许。');
      const url = new URL(req.url, `http://${host}`);
      const { pathname, searchParams } = url;
      if (req.method === 'GET' && assets.has(pathname)) {
        const [file, type] = assets.get(pathname);
        const body = await readFile(path.join(publicDir, file));
        res.writeHead(200, { 'Content-Type': `${type}; charset=utf-8` });
        res.end(body);
      } else if (req.method === 'GET' && pathname === '/api/devices') {
        await store.discoverLocal();
        send(200, { devices: store.list().map(publicDevice) });
      } else if (req.method === 'POST' && pathname === '/api/devices/test') {
        const device = validateDevice(await jsonBody(req));
        const health = await queryAgent(device, 'health');
        send(200, { health });
      } else if (req.method === 'POST' && pathname === '/api/devices') {
        const device = validateDevice(await jsonBody(req));
        await queryAgent(device, 'health');
        send(201, await store.add(device));
      } else if (req.method === 'DELETE' && /^\/api\/devices\/[\w-]+$/.test(pathname)) {
        await store.remove(pathname.split('/')[3]);
        send(200, { ok: true });
      } else if (req.method === 'GET' && /^\/api\/devices\/[\w-]+\/(health|overview|earnings|stats)$/.test(pathname)) {
        const [, , , id, route] = pathname.split('/');
        send(200, id === 'demo' ? demoQuery(route, searchParams) : await queryAgent(await store.get(id), route, searchParams));
      } else {
        throw new AppError(404, 'not_found', '页面或接口不存在。');
      }
    } catch (error) {
      const known = error instanceof AppError;
      send(known ? error.status : 500, { code: known ? error.code : 'internal_error', message: known ? error.message : '看板暂时无法完成操作，请稍后重试。', request_id: randomUUID() });
    }
  });
  server.requestTimeout = 20_000;
  server.headersTimeout = 15_000;
  return server;
}

if (process.argv[1] && path.resolve(process.argv[1]) === fileURLToPath(import.meta.url)) {
  const port = Number(process.env.BLOOMER_DASHBOARD_PORT || 17891);
  if (!Number.isInteger(port) || port < 1 || port > 65535) throw new Error('BLOOMER_DASHBOARD_PORT must be 1–65535');
  const server = await createDashboard();
  server.on('error', error => { console.error(error.code === 'EADDRINUSE' ? `端口 ${port} 已被占用，请设置 BLOOMER_DASHBOARD_PORT。` : '看板服务启动失败。'); process.exitCode = 1; });
  server.listen(port, '127.0.0.1', () => console.log(`Bloomer Dashboard · http://127.0.0.1:${port}`));
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => { server.close(); server.closeIdleConnections(); });
}
