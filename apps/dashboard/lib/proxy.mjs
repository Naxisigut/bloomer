import { AppError } from './config.mjs';

const routes = { health: '/v1/health', overview: '/v1/account/overview', earnings: '/v1/earnings', stats: '/v1/earnings/stats', 'device-state': '/v1/device/state', 'device-history': '/v1/device/state/history' };
const messages = {
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

export async function queryAgent(device, route, params = new URLSearchParams(), fetcher = fetch) {
  if (!routes[route]) throw new AppError(404, 'not_found', '接口不存在。');
  const url = new URL(routes[route], device.url);
  const allowed = route === 'earnings' ? ['from', 'to', 'model', 'limit', 'cursor'] : route === 'stats' ? ['from', 'to', 'model', 'group_by', 'timezone'] : route === 'device-history' ? ['from', 'to', 'limit', 'before_id'] : [];
  for (const [key, value] of params) {
    if (!allowed.includes(key) || params.getAll(key).length !== 1) throw new AppError(400, 'invalid_request', '查询参数无效。');
    url.searchParams.set(key, value);
  }
  try {
    const response = await fetcher(url, {
      headers: { Authorization: `Bearer ${device.token}`, Accept: 'application/json' },
      redirect: 'error', signal: AbortSignal.timeout(12_000),
    });
    if (['device-state', 'device-history'].includes(route) && [404, 405, 501].includes(response.status)) {
      await response.body?.cancel();
      throw new AppError(404, 'device_state_unsupported', messages.device_state_unsupported);
    }
    if (Number(response.headers.get('content-length')) > 8_000_000) throw new AppError(502, 'invalid_response', '设备响应超出大小限制。');
    let size = 0;
    const chunks = [];
    for await (const chunk of response.body) {
      size += chunk.length;
      if (size > 8_000_000) throw new AppError(502, 'invalid_response', '设备响应超出大小限制。');
      chunks.push(chunk);
    }
    let body;
    try { body = JSON.parse(Buffer.concat(chunks).toString('utf8')); } catch { throw new AppError(502, 'invalid_response', '设备返回了无法识别的响应。'); }
    if (!response.ok) {
      const code = response.status === 401 ? 'unauthorized' : Object.hasOwn(messages, body.code) ? body.code : 'agent_error';
      throw new AppError(response.status, code, messages[code] || '设备暂时无法完成查询，请稍后重试。');
    }
    if (!body || typeof body !== 'object' || Array.isArray(body)) throw new AppError(502, 'invalid_response', '设备返回的数据格式无效。');
    return body;
  } catch (error) {
    if (error instanceof AppError) throw error;
    throw new AppError(502, 'device_unreachable', '无法连接设备。请确认设备已开机、Tailscale 已连接，且 Agent 正在运行。');
  }
}
