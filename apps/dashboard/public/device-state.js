import { displayModelName } from './models.js';

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
export function runningModelLabel(response) {
  if (response?.error) return { text: '—', title: response.error };
  if (!response?.sample) return { text: '—', title: response ? '暂无状态数据' : '正在读取当前运行模型' };
  const value = reading(response.sample, 'current_model');
  if (value.known) return { text: displayModelName(value.text), title: value.text };
  return { text: value.text, title: value.reason || value.text };
}

export function activityReason(sample) {
  if (sample?.activity_error) return activityErrors[sample.activity_error] || '空闲状态暂不可用。';
  if (sample?.inference_active === true || sample?.request_work_pending === true) return '本机有正在处理或排队的任务。';
  if (sample?.is_idle === true) return '本机没有正在处理或排队的任务，且该设备最新收益距采集时超过 5 分钟。';
  if (sample?.is_idle === false) return '本机没有正在处理或排队的任务，该设备最近 5 分钟有收益明细。';
  return '尚未采集有效的本机任务状态和该设备收益。';
}

export const taskStatusField = 'task_status';
export const memoryField = 'memory';
const memoryFieldKeys = ['memory_total_bytes', 'memory_available_bytes'];

export const deviceStateSections = [
  {
    title: '机器状态',
    fields: [
      ['cpu_percent', 'CPU 使用率'],
      [memoryField, '总内存/可用内存'],
      ['memory_pressure', '内存压力'],
      ['swap_used_bytes', 'Swap 使用量'],
      ['gpu_percent', 'GPU 使用率'],
      ['thermal_state', '系统热压力'],
      ['disk_available_bytes', '磁盘可用空间'],
    ],
  },
  {
    title: 'Darkbloom 状态',
    fields: [
      ['provider_cpu_percent', 'Darkbloom CPU'],
      ['provider_running', 'Darkbloom 进程'],
      ['current_model', '当前模型'],
      ['model_ready', '模型就绪'],
      [taskStatusField, '工作状态'],
    ],
  },
];

export const deviceFields = deviceStateSections.flatMap(section => section.fields);

export function sampleFieldKeys() {
  return deviceFields.flatMap(([key]) => {
    if (key === taskStatusField) return [];
    if (key === memoryField) return memoryFieldKeys;
    return [key];
  });
}

export function memoryReading(sample) {
  const total = reading(sample, 'memory_total_bytes');
  const available = reading(sample, 'memory_available_bytes');
  const known = total.known && available.known;
  const text = known
    ? formatBytesPair(sample.memory_total_bytes, sample.memory_available_bytes)
    : `${total.text} / ${available.text}`;
  const reason = known
    ? `总内存 ${total.text}；可用内存（近似） ${available.text}`
    : [...new Set([total.known ? '' : total.reason, available.known ? '' : available.reason].filter(Boolean))].join('；') || text;
  return { text, known, reason };
}

export function reading(sample, key) {
  const status = sample?.quality?.[key];
  if (status !== 'ok') return { text: qualityLabels[status] || '未提供', known: false, reason: qualityLabels[status] || '来源未提供有效数据' };
  const value = sample[key];
  if (value === null || value === undefined) return { text: key === 'current_model' ? '无当前模型' : '未知', known: false, reason: '来源未提供具体值' };
  if (key.endsWith('_percent') || key.endsWith('_bytes') || key.endsWith('_temperature_c')) {
    if (typeof value !== 'number' || !Number.isFinite(value) || (key.endsWith('_bytes') && value < 0) || (key.endsWith('_percent') && (value < 0 || (key !== 'provider_cpu_percent' && value > 100)))) return { text: '数据异常', known: false, reason: '指标数值无效' };
    const text = key.endsWith('_bytes') ? formatBytes(value) : `${formatNumber(value)}${key.endsWith('_percent') ? '%' : ' °C'}`;
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

function formatNumber(value) {
  const rounded = Math.round(value * 10) / 10;
  if (Number.isInteger(rounded)) return String(rounded);
  return rounded.toFixed(1);
}

const byteUnits = ['B', 'K', 'M', 'G', 'T'];

function byteScale(value) {
  if (value === 0) return { index: 0, scale: 1 };
  const index = Math.min(4, Math.max(0, Math.floor(Math.log(value) / Math.log(1024))));
  return { index, scale: 1024 ** index };
}

function formatScaledBytes(value, index, scale) {
  const amount = value / scale;
  const text = index === 0 ? String(Math.round(amount)) : formatNumber(amount);
  return `${text} ${byteUnits[index]}`;
}

function formatBytes(value) {
  const { index, scale } = byteScale(value);
  return formatScaledBytes(value, index, scale);
}

function formatBytesPair(totalBytes, availableBytes) {
  const { index, scale } = byteScale(Math.max(totalBytes, availableBytes));
  const unit = byteUnits[index];
  const format = value => (index === 0 ? String(Math.round(value / scale)) : formatNumber(value / scale));
  return `${format(totalBytes)} / ${format(availableBytes)} ${unit}`;
}

export function activityStatus(response, now = Date.now()) {
  const result = (label, className = 'warning', detail = label) => ({ label, className, detail });
  if (response?.error) return result(response.code === 'device_state_unsupported' ? '状态未提供' : '状态读取失败', 'warning', response.error);
  if (!response) return result('读取状态中');
  if (!response.sample) return result('等待状态采集');
  const s = response.sample;
  if (s.provider_running === false && s.quality?.provider_running === 'ok') return result('未运行', 'offline', 'Darkbloom 进程未运行。');
  const active = reading(s, 'inference_active');
  const pending = reading(s, 'request_work_pending');
  if (active.known && s.inference_active === true) return result('处理中', '', '本机正在处理任务。');
  if (pending.known && s.request_work_pending === true) return result('排队', '', '本机有已接受或排队的任务。');
  if (active.known && pending.known) return result('无任务', '', '本机没有正在处理或排队的任务。');
  const detail = [...new Set([active, pending].filter(item => !item.known).map(item => item.reason))].join('；') || '本地任务状态暂不可用。';
  return result('未知', 'warning', detail);
}

export function pressureStatus(response, key, now = Date.now()) {
  const value = reading(response?.error ? null : response?.sample, key);
  if (!value.known) return { label: '未知', className: 'warning', detail: response?.error || value.reason };
  const raw = response.sample[key];
  return { label: value.text, className: ['serious', 'critical'].includes(raw) ? 'offline' : ['fair', 'warning'].includes(raw) ? 'warning' : '', detail: '设备端每 60 秒从 macOS 本地采集。' };
}

