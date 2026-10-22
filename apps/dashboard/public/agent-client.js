import { AppError } from './errors.js';
import { validateDevice, parseConnections } from './connections.js';
import { demoQuery } from './demo.js';

const routes = { alerts: '/v1/alerts', 'alert-test': '/v1/alerts/test', health: '/v1/health', overview: '/v1/account/overview', earnings: '/v1/earnings', stats: '/v1/earnings/stats', analysis: '/v1/earnings/analysis', 'device-state': '/v1/device/state', 'device-history': '/v1/device/state/history', control: '/v1/device/control', 'control-models': '/v1/device/control/models', 'control-jobs': '/v1/device/control/jobs' };
const messages = {
  analysis_unsupported: '该设备 Agent 尚不支持收益分析，请升级 Agent。',
  device_identity_unavailable: '设备身份尚未识别，暂时无法统计本机收益。',
  alerts_disabled: '该设备尚未开启 Bark 告警，请先在设备端配置并重启 Agent。',
  alerts_unsupported: '该设备 Agent 尚不支持告警记录，请升级 Agent。',
  control_disabled: '该设备已关闭远程操作，请在设备端配置中开启。',
  control_unavailable: '远程操作不可用，请检查设备端 Darkbloom 安装与 Agent 配置。',
  control_unsupported: '该设备 Agent 尚不支持远程操作，请先升级设备端 Agent。',
  control_busy: '设备正在执行另一项操作，请等待结束后再试。',
  request_conflict: '此请求编号已用于其他操作，请刷新后重新提交。',
  origin_forbidden: '该设备仍限制网页来源，请升级 Agent 后重试。',
  unauthorized: '访问令牌无效，请移除设备后使用正确的独立令牌重新连接。',
  data_unavailable: '设备尚未完成首次采集，暂时没有收益数据。',
  database_unavailable: '设备数据库暂不可用，请稍后刷新。',
  cursor_expired: '本次翻页已过期，请刷新明细。',
  invalid_request: '查询条件无效，请调整筛选后重试。',
  amount_out_of_range: '设备汇总金额超出支持范围。',
  query_timeout: '设备查询超时，请稍后重试。',
  group_limit_exceeded: '统计分组过多，请缩小时间范围。',
  device_state_unsupported: '该设备暂未提供设备状态接口。',
};

const invalidResponse = () => new AppError(502, 'invalid_response', '设备返回了无法识别的数据，请检查 Agent 地址和版本。');
const object = value => value !== null && typeof value === 'object' && !Array.isArray(value);
function validateResponse(route, body, operation) {
  if (!object(body)) throw invalidResponse();
  const items = () => Array.isArray(body.items) && body.items.every(object);
  if (route === 'alerts' && (!items() || body.items.length > 50 || body.items.some(item => typeof item.type !== 'string' || typeof item.event !== 'string' || typeof item.body !== 'string' || typeof item.device_name !== 'string' || typeof item.delivery_status !== 'string' || typeof item.recovered !== 'boolean' || !Number.isFinite(Date.parse(item.created_at)) || !Number.isSafeInteger(item.attempts)))) throw invalidResponse();
  if (route === 'alert-test' && (typeof body.type !== 'string' || typeof body.delivery_status !== 'string')) throw invalidResponse();
  if (route === 'health' && (typeof body.version !== 'string' || !body.version || typeof body.collection_state !== 'string')) throw invalidResponse();
  if (route === 'overview' && !(body.official === null || object(body.official))) throw invalidResponse();
  if (route === 'earnings' && (!items() || body.items.length > 500 || !(body.next_cursor == null || typeof body.next_cursor === 'string') || body.items.some(item => typeof item.record_id !== 'string' || typeof item.model !== 'string' || (typeof item.amount_micro_usd !== 'string' || !/^-?\d+$/.test(item.amount_micro_usd)) || !Number.isFinite(Date.parse(item.created_at))))) throw invalidResponse();
  if (route === 'stats' && (!items() || body.items.some(item => typeof item.key !== 'string' || (typeof item.amount_micro_usd !== 'string' || !/^-?\d+$/.test(item.amount_micro_usd)) || !Number.isSafeInteger(item.record_count) || item.record_count < 0))) throw invalidResponse();
  if (route === 'analysis') {
    if (!['hour', 'day', 'month'].includes(body.granularity) || typeof body.timezone !== 'string' || !Number.isFinite(Date.parse(body.from)) || !Number.isFinite(Date.parse(body.to))) throw invalidResponse();
    const amount = value => typeof value === 'string' && /^-?\d+$/.test(value);
    const row = item => object(item) && typeof item.model === 'string' && amount(item.amount_micro_usd) && Number.isSafeInteger(item.record_count) && item.record_count >= 0;
    if (!amount(body.totals?.amount_micro_usd) || !Number.isSafeInteger(body.totals?.record_count) || body.totals.record_count < 0) throw invalidResponse();
    if (!Array.isArray(body.models) || body.models.some(item => !row(item) || !Number.isSafeInteger(item.active_buckets) || item.active_buckets < 0)) throw invalidResponse();
    if (!Array.isArray(body.series) || body.series.some(bucket => !object(bucket) || !Number.isFinite(Date.parse(bucket.from)) || !Number.isFinite(Date.parse(bucket.to)) || !Array.isArray(bucket.items) || bucket.items.some(item => !row(item)))) throw invalidResponse();
    if (!Array.isArray(body.hour_of_day) || body.hour_of_day.some(item => !row(item) || !Number.isInteger(item.hour) || item.hour < 0 || item.hour > 23)) throw invalidResponse();
    const weekdayRow = item => object(item) && amount(item.amount_micro_usd) && Number.isSafeInteger(item.record_count) && item.record_count >= 0 && Number.isInteger(item.weekday) && item.weekday >= 1 && item.weekday <= 7 && Number.isInteger(item.hour) && item.hour >= 0 && item.hour <= 23;
    if (!Array.isArray(body.weekday_hour) || body.weekday_hour.some(item => !weekdayRow(item))) throw invalidResponse();
  }
  if (route === 'device-state' && !(body.sample === null || object(body.sample))) throw invalidResponse();
  if (route === 'device-history' && (!items() || body.items.length > 500)) throw invalidResponse();
  if (route === 'control' && (typeof body.available !== 'boolean' || typeof body.enabled !== 'boolean' || !Array.isArray(body.jobs))) throw invalidResponse();
  if (route === 'control-models' && (!Array.isArray(body.local) || !Array.isArray(body.catalog))) throw invalidResponse();
  if (route === 'control-jobs' && (operation ? typeof body.id !== 'string' || typeof body.status !== 'string' : !Array.isArray(body.jobs))) throw invalidResponse();
  return body;
}

export async function queryAgent(device, route, params = new URLSearchParams(), { fetcher = fetch, signal, timeoutMs = 12_000 } = {}, operation = null) {
  if (!Object.hasOwn(routes, route)) throw new AppError(404, 'not_found', '接口不存在。');
  const url = new URL(routes[route], device.url);
  const allowed = route === 'earnings' ? ['from', 'to', 'model', 'limit', 'cursor'] : route === 'stats' ? ['from', 'to', 'model', 'group_by', 'timezone'] : route === 'analysis' ? ['from', 'to', 'timezone', 'granularity', 'model'] : route === 'device-history' ? ['from', 'to', 'limit', 'before_id'] : [];
  const search = params instanceof URLSearchParams ? params : new URLSearchParams(params || undefined);
  for (const key of new Set(search.keys())) {
    if (!allowed.includes(key)) throw new AppError(400, 'invalid_request', '查询参数无效。');
    const values = search.getAll(key);
    if (key !== 'model' && values.length !== 1) throw new AppError(400, 'invalid_request', '查询参数无效。');
    if (key === 'model' && values.length > 8) throw new AppError(400, 'invalid_request', '查询参数无效。');
    for (const value of values) url.searchParams.append(key, value);
  }
  const controller = new AbortController();
  const abort = () => controller.abort();
  signal?.throwIfAborted();
  signal?.addEventListener('abort', abort, { once: true });
  let timedOut = false;
  const timer = setTimeout(() => { timedOut = true; controller.abort(); }, timeoutMs);
  let reader, response;
  try {
    response = await fetcher(url, {
      method: operation || route === 'alert-test' ? 'POST' : 'GET', body: operation ? JSON.stringify(operation) : undefined,
      headers: { Authorization: `Bearer ${device.token}`, Accept: 'application/json', ...(operation ? { 'Content-Type': 'application/json' } : {}) },
      mode: 'cors', credentials: 'omit', cache: 'no-store', referrerPolicy: 'no-referrer', redirect: 'error', signal: controller.signal,
    });
    if (['alerts', 'alert-test'].includes(route) && [404, 405, 501].includes(response.status)) throw new AppError(404, 'alerts_unsupported', messages.alerts_unsupported);
    if (route.startsWith('control') && [404, 405, 501].includes(response.status)) throw new AppError(404, 'control_unsupported', messages.control_unsupported);
    if (['device-state', 'device-history'].includes(route) && [404, 405, 501].includes(response.status)) throw new AppError(404, 'device_state_unsupported', messages.device_state_unsupported);
    if (route === 'analysis' && [404, 405, 501].includes(response.status)) throw new AppError(404, 'analysis_unsupported', messages.analysis_unsupported);
    if (Number(response.headers.get('content-length')) > 8_000_000) throw invalidResponse();
    if (!response.body) throw invalidResponse();
    reader = response.body.getReader();
    const chunks = []; let size = 0;
    while (true) {
      const { value, done } = await reader.read();
      if (done) break;
      size += value.byteLength;
      if (size > 8_000_000) throw invalidResponse();
      chunks.push(value);
    }
    const bytes = new Uint8Array(size); let offset = 0;
    for (const chunk of chunks) { bytes.set(chunk, offset); offset += chunk.byteLength; }
    let body;
    try { body = JSON.parse(new TextDecoder('utf-8', { fatal: true }).decode(bytes)); } catch { throw invalidResponse(); }
    if (!response.ok) {
      let code = response.status === 401 ? 'unauthorized' : object(body) && typeof body.code === 'string' ? body.code : 'agent_error';
      if (!Object.hasOwn(messages, code)) code = 'agent_error';
      throw new AppError(response.status, code, messages[code] || '设备暂时无法完成请求，请稍后重试。');
    }
    return validateResponse(route, body, operation);
  } catch (error) {
    if (signal?.aborted) signal.throwIfAborted();
    if (error instanceof AppError) throw error;
    if (timedOut) throw new AppError(504, 'request_timeout', '连接或读取设备超时，请检查网络与 Agent 状态后重试。');
    throw new AppError(502, 'device_unreachable', '浏览器无法完成设备请求，无法区分网络、证书或 CORS 问题。请检查 Tailscale、设备在线与 Agent 状态、HTTPS 地址和端口、证书，以及 Agent 是否已升级为允许任意网页来源的版本。');
  } finally {
    clearTimeout(timer); signal?.removeEventListener('abort', abort);
    if (reader) { try { await reader.cancel(); } catch { /* Aborted stream. */ } reader.releaseLock(); }
    else { try { await response?.body?.cancel(); } catch { /* Rejected response. */ } }
  }
}

export function validateControl(body) {
  if (!body || typeof body !== 'object' || Array.isArray(body) || Object.keys(body).some(key => !['request_id', 'action', 'models'].includes(key)) || typeof body.request_id !== 'string' || !/^[A-Za-z0-9_-]{16,80}$/.test(body.request_id)) throw new AppError(400, 'invalid_request', '操作请求格式无效。');
  const models = body.models ?? [];
  if (!Array.isArray(models) || models.length > 16 || new Set(models).size !== models.length || models.some(id => typeof id !== 'string' || !/^[A-Za-z0-9][A-Za-z0-9._/-]{0,199}$/.test(id) || /(^|\/)\.\.?(\/|$)/.test(id))) throw new AppError(400, 'invalid_request', '模型选择无效。');
  if (!['start', 'stop', 'restart', 'switch', 'download'].includes(body.action) || (['start', 'switch'].includes(body.action) && !models.length) || (body.action === 'download' && models.length !== 1) || (['stop', 'restart'].includes(body.action) && models.length)) throw new AppError(400, 'invalid_request', '操作与模型选择不匹配。');
  return { request_id: body.request_id, action: body.action, ...(models.length ? { models } : {}) };
}
export function submitControl(device, body, options) {
  return queryAgent(device, 'control-jobs', new URLSearchParams(), options, validateControl(body));
}

export function createAgentClient(store, options = {}) {
  return {
    query(route, params, id = store.selected, signal) {
      if (id === 'demo') {
        if (route.startsWith('control') || route === 'alert-test') return Promise.reject(new AppError(403, 'demo_read_only', '演示设备不执行远程操作。'));
        signal?.throwIfAborted();
        return Promise.resolve(demoQuery(route, params instanceof URLSearchParams ? params : new URLSearchParams(params || {})));
      }
      const search = params instanceof URLSearchParams ? params : new URLSearchParams(params || undefined);
      const timeoutMs = route === 'analysis' ? 10_000 : options.timeoutMs;
      return queryAgent(store.get(id), route, search, { ...options, signal, timeoutMs });
    },
    submit(id, body) {
      if (id === 'demo') return Promise.reject(new AppError(403, 'demo_read_only', '演示设备不执行远程操作。'));
      return submitControl(store.get(id), body, options);
    },
    async connect(input, save = false) {
      const device = validateDevice(input);
      const health = await queryAgent(device, 'health', undefined, options);
      return { health, ...(save ? { device: store.add(device) } : {}) };
    },
    async importConnections(input) {
      const devices = parseConnections(input);
      for (const device of devices) await queryAgent(device, 'health', undefined, options);
      return store.addMany(devices);
    },
  };
}
