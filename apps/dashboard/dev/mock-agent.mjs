// Development-only synthetic Agents. Never executes Darkbloom or touches Agent files.
// node dev/mock-agent.mjs -- serves ports 18190/18191 for the static-browser checklist.
import http from 'node:http';
import { demoQuery } from '../public/demo.js';
import { validateControl } from '../public/agent-client.js';
import { validateNetworkRatesSettings } from '../public/network-rates.js';

function mockNetworkRates(autoSwitch) {
  const now = new Date();
  const stamp = now.toISOString();
  return {
    enabled: true,
    auto_switch: autoSwitch,
    auto_switch_since: autoSwitch ? new Date(+now - 45 * 60_000).toISOString() : null,
    window_samples: 6,
    samples: 5,
    updated_at: stamp,
    snapshot_at: stamp,
    current_model: 'mock/model-a',
    recommendation: { model: 'mock/model-b', usd_per_hour_micro: '88000', fallback: false },
    candidates: [
      { model: 'mock/model-b', downloaded: true, avg_providers: 34.0, requests_per_hour: 100, tokens_per_hour: 300000, usd_per_hour_micro: '88000', price_source: 'official', insufficient: false },
      { model: 'mock/model-a', downloaded: true, avg_providers: 30.0, requests_per_hour: 80, tokens_per_hour: 240000, usd_per_hour_micro: '65000', price_source: 'official', insufficient: false },
    ],
    switch: { next_eligible_at: new Date(+now + 20 * 60_000).toISOString(), blocked_reason: autoSwitch ? 'warming_up' : 'auto_switch_off', backoff_until: null },
    last_error: null,
  };
}

for (const port of [18190, 18191]) {
  const jobs = []; let loseFirstResponse = true; let networkRatesAutoSwitch = false;
  const switches = [{ id: 1, at: new Date(Date.now() - 7200_000).toISOString(), from_model: 'mock/model-a', to_model: 'mock/model-b', reason: 'better_model', status: 'succeeded', error: null, job_id: 'mock-switch-1', summary: [{ model: 'mock/model-b', usd_per_hour_micro: '86000', avg_providers: 33.1 }] }];
  const server = http.createServer(async (req, res) => {
    res.setHeader('Cache-Control', 'no-store'); res.setHeader('Access-Control-Allow-Origin', '*');
    res.setHeader('Content-Type', 'application/json');
    const send = (status, body) => { res.writeHead(status); res.end(JSON.stringify(body)); };
    if (req.method === 'OPTIONS') {
      res.setHeader('Access-Control-Allow-Methods', 'GET, POST');
      res.setHeader('Access-Control-Allow-Headers', 'Authorization, Content-Type');
      res.writeHead(204); res.end(); return;
    }
    if (req.headers.authorization !== 'Bearer bloomer-mock-only') { send(401, { code: 'unauthorized' }); return; }
    const url = new URL(req.url, `http://127.0.0.1:${port}`);
    // Log method/path only; no headers, bodies or tokens.
    console.log(`${port} ${req.method} ${url.pathname}`);
    if (url.pathname.startsWith('/v1/device/network-rates')) {
      if (req.method === 'POST' && url.pathname === '/v1/device/network-rates/settings') {
        try {
          const chunks = []; for await (const chunk of req) chunks.push(chunk);
          const input = validateNetworkRatesSettings(JSON.parse(Buffer.concat(chunks).toString()));
          networkRatesAutoSwitch = input.auto_switch;
          send(200, mockNetworkRates(networkRatesAutoSwitch));
        } catch { send(400, { code: 'invalid_request' }); }
        return;
      }
      if (req.method === 'GET' && url.pathname === '/v1/device/network-rates/switches') { send(200, { switches }); return; }
      if (req.method === 'GET' && url.pathname === '/v1/device/network-rates') { send(200, mockNetworkRates(networkRatesAutoSwitch)); return; }
      send(404, { code: 'not_found' }); return;
    }
    if (url.pathname.startsWith('/v1/device/control')) {
      if (req.method === 'POST' && url.pathname === '/v1/device/control/jobs') {
        try {
          const chunks = []; for await (const chunk of req) chunks.push(chunk);
          const input = validateControl(JSON.parse(Buffer.concat(chunks).toString()));
          let job = jobs.find(job => job.request_id === input.request_id);
          if (job && JSON.stringify(job.models || []) !== JSON.stringify(input.models || []) || job && job.action !== input.action) { send(409, { code: 'request_conflict' }); return; }
          if (!job) {
            job = { ...input, id: `mock-job-${jobs.length+1}`, status: 'running', created_at: new Date().toISOString() }; jobs.unshift(job);
            setTimeout(() => { job.status = 'succeeded'; job.finished_at = new Date().toISOString(); }, 6000);
          }
          if (loseFirstResponse) { loseFirstResponse = false; res.destroy(); return; }
          send(202, job);
        } catch { send(400, { code: 'invalid_request' }); }
      } else if (url.pathname.endsWith('/models')) send(200, { local: [{ id: 'mock/model-a', display_name: '模拟本地模型 A' }], catalog: [{ id: 'mock/model-b', display_name: '模拟目录模型 B' }] });
      else send(200, { enabled: true, available: true, jobs });
      return;
    }
    const routes = { '/v1/alerts': 'alerts', '/v1/health': 'health', '/v1/account/overview': 'overview', '/v1/earnings': 'earnings', '/v1/earnings/stats': 'stats', '/v1/earnings/analysis': 'analysis', '/v1/device/state': 'device-state' };
    try {
      if (!routes[url.pathname] || req.method !== 'GET') { send(404, { code: 'not_found' }); return; }
      const body = demoQuery(routes[url.pathname], url.searchParams);
      if (url.pathname === '/v1/health') {
        const now = new Date();
        body.version = `mock-${port}`;
        body.server_time = now.toISOString();
        body.next_collection_at = new Date(+now + 45_000).toISOString();
      }
      send(200, body);
    } catch (error) { send(error.status || 500, { code: error.code || 'internal_error' }); }
  });
  server.listen(port, '127.0.0.1', () => console.log(`Synthetic Agent http://127.0.0.1:${port}; allow any origin`));
  for (const signal of ['SIGINT', 'SIGTERM']) process.on(signal, () => server.close());
}
