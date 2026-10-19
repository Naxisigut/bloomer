const qualityLabels = { unsupported: '未支持', unavailable: '未读取', permission_denied: '无权限', invalid: '数据异常', stale: '数据已过期', not_running: '进程未运行' };
const valueLabels = {
  memory_pressure: { normal: '正常', warning: '偏高', critical: '严重' },
  thermal_state: { nominal: '正常', fair: '轻度热压力', serious: '较高热压力', critical: '严重热压力' },
  provider_running: { true: '运行中', false: '未运行' },
  provider_connected: { true: '已连接', false: '未连接' },
  model_ready: { true: '本地已就绪', false: '本地未就绪' },
  inference_active: { true: '正在处理', false: '没有任务处理中' },
  request_work_pending: { true: '有待处理任务', false: '无待处理任务' },
  is_idle: { true: '空闲', false: '非空闲' },
};

const activityErrors = {
  provider_not_running: 'Darkbloom 进程未运行，无法确认当前空闲状态。',
  task_state_unavailable: '本地任务状态未提供或已过期，等待有效采集。',
  device_identity_unavailable: '尚未匹配本机 Provider 身份，不使用账户内其他设备收益。',
  device_earnings_unavailable: '该设备的收益或身份映射暂不可用，等待后续采集。',
  earnings_stale: '收益同步已过期，不能确认最近 5 分钟是否有该设备收益。',
  no_device_earnings: '尚无该设备的收益明细，暂不能确认空闲。',
  earnings_time_invalid: '该设备的最新收益时间异常。',
};
export function activityReason(sample) {
  if (sample?.activity_error) return activityErrors[sample.activity_error] || '空闲状态暂不可用。';
  if (sample?.inference_active === true || sample?.request_work_pending === true) return '本机有正在处理或排队的任务。';
  if (sample?.is_idle === true) return '本机没有正在处理或排队的任务，且该设备最新收益距采集时超过 5 分钟。';
  if (sample?.is_idle === false) return '本机没有正在处理或排队的任务，该设备最近 5 分钟有收益明细。';
  return '尚未采集有效的本机任务状态和该设备收益。';
}

export const deviceFields = [
  ['cpu_percent', 'CPU 使用率'], ['provider_cpu_percent', 'Darkbloom CPU'],
  ['memory_total_bytes', '总内存'], ['memory_available_bytes', '可用内存（近似）'],
  ['memory_pressure', '内存压力'], ['swap_used_bytes', 'Swap 使用量'],
  ['gpu_percent', 'GPU 使用率'], ['cpu_temperature_c', 'CPU 温度'], ['gpu_temperature_c', 'GPU 温度'],
  ['thermal_state', '系统热压力'], ['disk_available_bytes', '磁盘可用空间'],
  ['provider_running', 'Darkbloom 进程'], ['inference_active', '任务处理'], ['request_work_pending', '待处理任务'],
  ['current_model', '当前模型'], ['model_ready', '模型就绪'],
  ['is_idle', '当前是否空闲'],
];

export function reading(sample, key) {
  const status = sample?.quality?.[key];
  if (status !== 'ok') return { text: qualityLabels[status] || '未提供', known: false, reason: qualityLabels[status] || '来源未提供有效数据' };
  const value = sample[key];
  if (value === null || value === undefined) return { text: key === 'current_model' ? '无当前模型' : '未知', known: false, reason: '来源未提供具体值' };
  if (key.endsWith('_percent') || key.endsWith('_bytes') || key.endsWith('_temperature_c')) {
    if (typeof value !== 'number' || !Number.isFinite(value) || (key.endsWith('_bytes') && value < 0) || (key.endsWith('_percent') && (value < 0 || (key !== 'provider_cpu_percent' && value > 100)))) return { text: '数据异常', known: false, reason: '指标数值无效' };
    const text = key.endsWith('_bytes') ? formatBytes(value) : `${value.toFixed(1)}${key.endsWith('_percent') ? '%' : ' °C'}`;
    return { text, known: true, reason: '' };
  }
  if (valueLabels[key]) {
    if (['provider_running', 'provider_connected', 'model_ready', 'inference_active', 'request_work_pending', 'is_idle'].includes(key) && typeof value !== 'boolean') return { text: '数据异常', known: false, reason: '来源没有提供有效的布尔状态' };
    const text = valueLabels[key][String(value)];
    return text ? { text, known: true, reason: '' } : { text: '未知', known: false, reason: '来源状态尚未识别' };
  }
  if (typeof value !== 'string' || !value.trim()) return { text: '未知', known: false, reason: '来源未提供有效文本' };
  return { text: value, known: true, reason: '' };
}

function formatBytes(value) {
  if (value === 0) return '0 B';
  const index = Math.min(4, Math.max(0, Math.floor(Math.log(value) / Math.log(1024))));
  return `${(value / 1024 ** index).toFixed(index ? 2 : 0)} ${['B', 'KiB', 'MiB', 'GiB', 'TiB'][index]}`;
}

export function sampleStale(response, now = Date.now()) {
  const at = Date.parse(response?.sample?.collected_at);
  return response?.meta?.data_stale === true || !Number.isFinite(at) || at > +now || +now - at > 120_000;
}

export function activityStatus(response, now = Date.now()) {
  const result = (label, className = 'warning', detail = label) => ({ label, className, detail });
  if (response?.error) return result(response.code === 'device_state_unsupported' ? '状态未提供' : '状态读取失败', 'warning', response.error);
  if (!response) return result('读取状态中');
  if (!response.sample) return result('等待状态采集');
  if (sampleStale(response, now)) return result('状态已过期', 'warning', '当前显示最近保存的状态，不能确认当前是否空闲。');
  const s = response.sample;
  if (s.provider_running === false && s.quality?.provider_running === 'ok') return result('未运行', 'offline', activityReason(s));
  if (!reading(s, 'is_idle').known) return result('未知', 'warning', activityReason(s));
  if (s.is_idle) return result('空闲', 'warning', activityReason(s));
  return result(s.inference_active === true || s.request_work_pending === true ? '处理中' : '近期有收益', '', activityReason(s));
}

export function pressureStatus(response, key, now = Date.now()) {
  const value = reading(response?.error ? null : response?.sample, key);
  if (!value.known) return { label: '未知', className: 'warning', detail: response?.error || value.reason };
  if (sampleStale(response, now)) return { label: `${value.text} · 过期`, className: 'warning', detail: '最近保存的读数已过期。' };
  const raw = response.sample[key];
  return { label: value.text, className: ['serious', 'critical'].includes(raw) ? 'offline' : ['fair', 'warning'].includes(raw) ? 'warning' : '', detail: '设备端每 60 秒从 macOS 本地采集。' };
}

export function localInput(value, timezone) {
  const parts = Object.fromEntries(new Intl.DateTimeFormat('en-CA', { timeZone: timezone, year: 'numeric', month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' }).formatToParts(new Date(value)).map(part => [part.type, part.value]));
  return `${parts.year}-${parts.month}-${parts.day}T${parts.hour}:${parts.minute}`;
}

export function inputRange(from, to, timezone) {
  const offset = { 'Asia/Shanghai': '+08:00', UTC: 'Z' }[timezone];
  if (!offset) throw new Error('请选择支持的查询时区。');
  const parse = value => {
    if (!/^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/.test(value)) throw new Error('请填写完整的起止时间。');
    const date = new Date(value + offset);
    if (!Number.isFinite(+date) || localInput(date, timezone) !== value) throw new Error('请填写有效的日期和时间。');
    return date;
  };
  const first = parse(from), last = parse(to);
  if (+first >= +last) throw new Error('开始时间需要早于结束时间。');
  if (+last - +first > 365 * 86_400_000) throw new Error('每次最多查询 365 天的设备状态。');
  return { from: first.toISOString(), to: last.toISOString() };
}

// Historical pages stay anchored while fresh latest-state polls continue.
export class DeviceHistoryPager {
  constructor(fetchPage, range, limit) { this.fetchPage = fetchPage; this.range = { ...range }; this.limit = limit; this.pages = []; }
  async page(index, signal) {
    if (this.pages[index]) return this.pages[index];
    if (index < 0 || index !== this.pages.length || (index && !this.pages[index - 1].next_before_id)) throw new Error('该页不可用，请重新查询。');
    const before = index ? this.pages[index - 1].next_before_id : null;
    const response = await this.fetchPage({ ...this.range, limit: this.limit, ...(before ? { before_id: before } : {}) }, signal);
    signal?.throwIfAborted();
    if (!response || !Array.isArray(response.items) || response.items.length > this.limit) throw new Error('设备返回了无效的状态明细。');
    let previous = before ?? Infinity;
    for (const item of response.items) {
      if (!Number.isSafeInteger(item.id) || item.id <= 0 || item.id >= previous || !Number.isFinite(Date.parse(item.collected_at))) throw new Error('设备返回的状态记录顺序无效，请重新查询。');
      previous = item.id;
    }
    const next = response.next_before_id;
    if (next !== null && next !== undefined && (!Number.isSafeInteger(next) || !response.items.length || next !== previous)) throw new Error('设备返回了无效的状态翻页位置。');
    if (!index && response.meta?.effective_from && response.meta?.effective_to) {
      const from = Date.parse(response.meta.effective_from), to = Date.parse(response.meta.effective_to);
      if (Number.isFinite(from) && Number.isFinite(to) && from < to && from >= Date.parse(this.range.from) && to <= Date.parse(this.range.to)) this.range = { from: response.meta.effective_from, to: response.meta.effective_to };
    }
    const page = { ...response, next_before_id: next ?? null };
    this.pages.push(page);
    return page;
  }
}
