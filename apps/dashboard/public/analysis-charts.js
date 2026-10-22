import { microToChartNumber } from './analysis-data.js';
import { money } from './data.js';

const colors = ['#447da1', '#bd8037', '#8570ad', '#328b80', '#b86772', '#798c3a', '#97704c', '#5770b0'];
const colorFor = (model, order) => colors[order.indexOf(model) % colors.length];

export function trendOption({ filledBuckets, modelOrder, mode = 'bar' }) {
  const labels = filledBuckets.map(bucket => bucket.from);
  const series = modelOrder.map(model => ({
    name: model,
    type: mode === 'line' ? 'line' : 'bar',
    stack: mode === 'bar' ? 'amount' : undefined,
    emphasis: { focus: 'series' },
    itemStyle: { color: colorFor(model, modelOrder) },
    data: filledBuckets.map(bucket => microToChartNumber(bucket.items.find(item => item.model === model)?.amount_micro_usd || '0')),
  }));
  return {
    color: colors,
    tooltip: { trigger: 'axis', valueFormatter: value => money(String(Math.round(Number(value) * 1e6))) },
    legend: { type: 'scroll', bottom: 0 },
    grid: { left: 48, right: 16, top: 24, bottom: 48 },
    dataZoom: [{ type: 'inside' }, { type: 'slider', height: 18, bottom: 24 }],
    xAxis: { type: 'category', data: labels, axisLabel: { formatter: bucketLabel(filledBuckets) } },
    yAxis: { type: 'value', name: 'USD' },
    series,
  };
}

export function cumulativeOption({ cumulative, modelOrder }) {
  const labels = cumulative.map(point => point.from);
  const series = [
    ...modelOrder.map(model => ({
      name: model,
      type: 'line',
      smooth: true,
      showSymbol: false,
      itemStyle: { color: colorFor(model, modelOrder) },
      data: cumulative.map(point => microToChartNumber(point.byModel[model] || '0')),
    })),
    { name: '合计', type: 'line', smooth: true, showSymbol: false, lineStyle: { width: 2, type: 'dashed' }, data: cumulative.map(point => microToChartNumber(point.combined_micro_usd)) },
  ];
  return { tooltip: { trigger: 'axis', valueFormatter: value => money(String(Math.round(Number(value) * 1e6))) }, legend: { type: 'scroll', bottom: 0 }, grid: { left: 48, right: 16, top: 24, bottom: 40 }, xAxis: { type: 'category', data: labels, axisLabel: { formatter: bucketLabel(cumulative) } }, yAxis: { type: 'value', name: '累计 USD' }, series };
}

export function modelShareOption(modelShares) {
  if (!modelShares.length) return emptyOption('暂无模型收益');
  return {
    tooltip: { trigger: 'item', formatter: params => `${params.name}<br>${money(String(Math.round(params.value * 1e6)))} · ${params.percent}%` },
    legend: { orient: 'vertical', left: 'left', top: 'middle' },
    series: [{ type: 'pie', radius: ['42%', '68%'], center: ['62%', '50%'], data: modelShares.map(item => ({ name: item.model, value: microToChartNumber(item.amount_micro_usd) })) }],
  };
}

export function taskAvgOption(filledBuckets) {
  const labels = filledBuckets.map(bucket => bucket.from);
  return {
    tooltip: { trigger: 'axis' },
    legend: { bottom: 0 },
    grid: { left: 48, right: 48, top: 24, bottom: 40 },
    xAxis: { type: 'category', data: labels, axisLabel: { formatter: bucketLabel(filledBuckets) } },
    yAxis: [{ type: 'value', name: '任务数' }, { type: 'value', name: '单笔 USD' }],
    series: [
      { name: '任务数', type: 'bar', data: filledBuckets.map(bucket => bucket.record_count), itemStyle: { color: '#447da1' } },
      { name: '单笔均值', type: 'line', yAxisIndex: 1, smooth: true, data: filledBuckets.map(bucket => bucket.record_count ? microToChartNumber((BigInt(bucket.total_micro_usd) / BigInt(bucket.record_count)).toString()) : 0) },
    ],
  };
}

export function hourDistributionOption(hourDaily, modelOrder) {
  const hours = Array.from({ length: 24 }, (_, hour) => hour);
  const models = modelOrder.length ? modelOrder : [...new Set(hourDaily.map(row => row.model))];
  if (!hourDaily.length) return emptyOption('暂无时段数据');
  return {
    tooltip: { trigger: 'axis', valueFormatter: value => money(String(Math.round(Number(value) * 1e6))) },
    legend: { type: 'scroll', bottom: 0 },
    grid: { left: 48, right: 16, top: 24, bottom: 40 },
    xAxis: { type: 'category', data: hours.map(hour => `${String(hour).padStart(2, '0')}:00`) },
    yAxis: { type: 'value', name: '小时日均 USD' },
    series: models.map(model => ({
      name: model,
      type: 'line',
      smooth: true,
      showSymbol: false,
      itemStyle: { color: colorFor(model, models) },
      data: hours.map(hour => microToChartNumber(hourDaily.find(row => row.hour === hour && row.model === model)?.daily_avg_micro_usd || '0')),
    })),
  };
}

export function weekdayHourHeatmap(weekdayHour) {
  if (!weekdayHour.length) return emptyOption('暂无热力数据');
  const data = weekdayHour.map(item => [item.hour, item.weekday - 1, microToChartNumber(item.amount_micro_usd)]);
  const max = Math.max(...data.map(item => item[2]), 0.000001);
  return {
    tooltip: { position: 'top', formatter: params => `周${'一二三四五六日'[params.value[1]]} ${String(params.value[0]).padStart(2, '0')}:00 · ${money(String(Math.round(params.value[2] * 1e6)))}` },
    grid: { left: 48, right: 16, top: 16, bottom: 40 },
    xAxis: { type: 'category', data: Array.from({ length: 24 }, (_, hour) => `${String(hour).padStart(2, '0')}:00`), splitArea: { show: true } },
    yAxis: { type: 'category', data: ['周一', '周二', '周三', '周四', '周五', '周六', '周日'], splitArea: { show: true } },
    visualMap: { min: 0, max, calculable: true, orient: 'horizontal', left: 'center', bottom: 0 },
    series: [{ type: 'heatmap', data, emphasis: { itemStyle: { shadowBlur: 10, shadowColor: 'rgba(0,0,0,0.3)' } } }],
  };
}

export function modelHourHeatmap(hourOfDay, modelOrder) {
  const models = modelOrder.length ? modelOrder : [...new Set(hourOfDay.map(item => item.model))];
  if (!hourOfDay.length || !models.length) return emptyOption('暂无模型时段数据');
  const data = [];
  for (const model of models) {
    for (let hour = 0; hour < 24; hour++) {
      const item = hourOfDay.find(row => row.model === model && row.hour === hour);
      data.push([hour, models.indexOf(model), microToChartNumber(item?.amount_micro_usd || '0')]);
    }
  }
  const max = Math.max(...data.map(item => item[2]), 0.000001);
  return {
    tooltip: { position: 'top' },
    grid: { left: 80, right: 16, top: 16, bottom: 40 },
    xAxis: { type: 'category', data: Array.from({ length: 24 }, (_, hour) => `${String(hour).padStart(2, '0')}:00`) },
    yAxis: { type: 'category', data: models },
    visualMap: { min: 0, max, calculable: true, orient: 'horizontal', left: 'center', bottom: 0 },
    series: [{ type: 'heatmap', data }],
  };
}

export function modelCompareOption(models, spanDays, granularity = 'day') {
  if (!models.length) return emptyOption('暂无模型对比');
  const days = BigInt(Math.max(1, spanDays));
  const rows = models.map(item => ({
    model: item.model,
    avg: item.record_count ? Number(BigInt(item.amount_micro_usd) / BigInt(item.record_count)) / 1e6 : 0,
    daily: Number(BigInt(item.amount_micro_usd) / days) / 1e6,
    active: item.active_buckets ? Number(BigInt(item.amount_micro_usd) / BigInt(item.active_buckets)) / 1e6 : 0,
  }));
  return {
    tooltip: { trigger: 'axis' },
    legend: { bottom: 0 },
    grid: { left: 48, right: 16, top: 24, bottom: 40 },
    xAxis: { type: 'category', data: rows.map(row => row.model) },
    yAxis: { type: 'value', name: 'USD' },
    series: [
      { name: '单笔均值', type: 'bar', data: rows.map(row => row.avg) },
      { name: '日均收益', type: 'bar', data: rows.map(row => row.daily) },
      { name: `每活跃${({ hour: '小时', day: '天', month: '月' })[granularity] || '分桶'}`, type: 'bar', data: rows.map(row => row.active) },
    ],
  };
}

const labelFormats = {
  hour: { month: '2-digit', day: '2-digit', hour: '2-digit', minute: '2-digit', hourCycle: 'h23' },
  day: { month: '2-digit', day: '2-digit' },
  month: { year: 'numeric', month: '2-digit' },
};

function bucketLabel(buckets) {
  const span = buckets.length ? Date.parse(buckets[0].to) - Date.parse(buckets[0].from) : 0;
  const formatter = new Intl.DateTimeFormat('zh-CN', { timeZone: 'Asia/Shanghai', ...labelFormats[span <= 3_600_000 ? 'hour' : span <= 90_000_000 ? 'day' : 'month'] });
  return value => formatter.format(new Date(value));
}

function emptyOption(text) {
  return { title: { text, left: 'center', top: 'middle', textStyle: { color: '#87907e', fontSize: 12, fontWeight: 400 } }, series: [] };
}
