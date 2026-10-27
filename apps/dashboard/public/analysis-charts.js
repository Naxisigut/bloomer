import { microToChartNumber } from './analysis-data.js';
import { money } from './data.js';
import { displayModelNames, isRewardModel, REWARD_LABEL } from './models.js';
import { MODEL_COLORS, REWARD_COLOR, MAX_MODELS } from './palette.js';

const modelLabel = (models) => {
  const labels = displayModelNames(models);
  return id => labels.get(id) ?? id;
};

const colors = MODEL_COLORS;
// Rewards keep their own grey so they never consume a palette slot, which would make
// two models share a colour once every palette entry is in use.
const colorFor = (model, order) => isRewardModel(model) ? REWARD_COLOR : colors[order.filter(item => !isRewardModel(item)).indexOf(model) % colors.length];
const fontFamily = '"Avenir Next","Poppins","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif';
const axisLabel = { color: '#9ea3ab', fontSize: 11 };
const categoryAxis = { axisLine: { lineStyle: { color: '#e6e8ec' } }, axisTick: { show: false }, axisLabel, splitLine: { show: false }, nameTextStyle: axisLabel };
const valueAxis = { axisLine: { show: false }, axisTick: { show: false }, axisLabel, splitLine: { lineStyle: { color: '#eef0f3' } }, nameTextStyle: axisLabel };

export const chartTheme = {
  color: colors,
  backgroundColor: 'transparent',
  textStyle: { fontFamily, color: '#41454d' },
  title: { textStyle: { color: '#1b1d21', fontWeight: 600 } },
  legend: { icon: 'circle', itemWidth: 8, itemHeight: 8, itemGap: 16, textStyle: { color: '#41454d', fontSize: 12 } },
  tooltip: {
    backgroundColor: '#1f2229', borderWidth: 0, padding: [10, 14], textStyle: { color: '#fff', fontSize: 12 },
    extraCssText: 'border-radius:12px;box-shadow:0 10px 30px rgba(27,29,33,.2);',
    axisPointer: { lineStyle: { color: '#b9bec6', type: 'dashed' }, shadowStyle: { color: 'rgba(91,141,239,.06)' } },
  },
  categoryAxis, valueAxis,
  line: { symbol: 'circle', symbolSize: 6, lineStyle: { width: 2.5 } },
  bar: { barMaxWidth: 28, itemStyle: { borderRadius: [4, 4, 0, 0] } },
  pie: { itemStyle: { borderColor: '#fff', borderWidth: 2, borderRadius: 6 } },
  visualMap: { color: ['#3f6fd8', '#ebf2fe'], textStyle: { color: '#8a8f98' } },
  dataZoom: { borderColor: '#eef0f3', fillerColor: 'rgba(91,141,239,.12)', handleStyle: { color: '#fff', borderColor: '#b9bec6' }, textStyle: { color: '#8a8f98' }, dataBackground: { lineStyle: { color: '#d5d9df' }, areaStyle: { color: '#eef0f3' } } },
};

export function trendOption({ filledBuckets, modelOrder, mode = 'bar', chartWidth }) {
  const labels = bucketLabels(filledBuckets);
  const label = modelLabel(modelOrder);
  const legendLabels = modelOrder.map(label);
  const series = modelOrder.map(model => ({
    name: label(model),
    type: mode === 'line' ? 'line' : 'bar',
    stack: mode === 'bar' ? 'amount' : undefined,
    emphasis: { focus: 'series' },
    itemStyle: { color: colorFor(model, modelOrder) },
    data: filledBuckets.map(bucket => microToChartNumber(bucket.items.find(item => item.model === model)?.amount_micro_usd || '0')),
  }));
  const totals = filledBuckets.map((_, index) => series.reduce((sum, item) => sum + (Number(item.data[index]) || 0), 0));
  const peaks = filledBuckets.map((_, index) => mode === 'bar' ? 0 : Math.max(0, ...series.map(item => Number(item.data[index]) || 0)));
  series.push({
    name: '合计',
    type: mode === 'line' ? 'scatter' : 'bar',
    stack: mode === 'bar' ? 'amount' : undefined,
    data: mode === 'bar' ? totals.map(() => 0) : peaks,
    label: {
      show: true,
      position: 'top',
      color: '#41454d',
      fontSize: 11,
      formatter: params => {
        const total = totals[params.dataIndex];
        return total ? chartMoney(total) : '';
      },
    },
    labelLayout: { hideOverlap: true },
    tooltip: { show: false },
    legendHoverLink: false,
    silent: true,
    symbol: 'none',
    itemStyle: { color: 'transparent' },
    emphasis: { disabled: true },
  });
  return {
    color: colors,
    tooltip: { trigger: 'axis', formatter: params => trendTooltip(Array.isArray(params) ? params.filter(item => item.seriesName !== '合计') : params) },
    legend: legendOption({ data: legendLabels }),
    grid: { ...trendGrid(labels.length, legendLabels, chartWidth), top: 36 },
    dataZoom: trendZoom(labels.length, legendLabels, chartWidth),
    xAxis: { type: 'category', data: labels, boundaryGap: true, min: 0, max: Math.max(labels.length - 1, 0) },
    yAxis: { type: 'value', name: 'USD' },
    series,
  };
}

export function cumulativeOption({ cumulative, modelOrder, chartWidth }) {
  const labels = cumulative.map(point => point.from);
  const label = modelLabel(modelOrder);
  const legendLabels = [...modelOrder.map(label), '合计'];
  const series = [
    ...modelOrder.map(model => ({
      name: label(model),
      type: 'line',
      smooth: true,
      showSymbol: false,
      itemStyle: { color: colorFor(model, modelOrder) },
      data: cumulative.map(point => microToChartNumber(point.byModel[model] || '0')),
    })),
    { name: '合计', type: 'line', smooth: true, showSymbol: false, lineStyle: { width: 2, type: 'dashed' }, data: cumulative.map(point => microToChartNumber(point.combined_micro_usd)) },
  ];
  return {
    tooltip: { trigger: 'axis', valueFormatter: value => money(String(Math.round(Number(value) * 1e6))) },
    legend: legendOption({ data: legendLabels }),
    grid: { left: 48, right: 16, top: 24, bottom: legendReserve(legendLabels, chartWidth) + LEGEND_AXIS_RESERVE },
    xAxis: { type: 'category', data: labels, axisLabel: { formatter: bucketLabel(cumulative) } },
    yAxis: { type: 'value', name: '累计 USD' },
    series,
  };
}

export function modelShareOption(modelShares) {
  if (!modelShares.length) return emptyOption('暂无模型收益');
  const label = modelLabel(modelShares.map(item => item.model));
  // The vertical legend only fits eight rows at the fixed 300px chart height, so a
  // fuller list shrinks the text and lets ECharts paginate it.
  const many = modelShares.length > 8;
  return {
    tooltip: { trigger: 'item', formatter: params => `${params.name}<br>${money(String(Math.round(params.value * 1e6)))} · ${params.percent}%` },
    legend: {
      type: 'scroll', orient: 'vertical', left: 8, top: 'middle', width: '38%',
      itemGap: many ? 8 : 16,
      textStyle: { overflow: 'truncate', width: many ? 126 : 150, fontSize: many ? 11 : 12 },
    },
    series: [{ type: 'pie', radius: ['48%', '72%'], center: ['68%', '50%'], label: { show: false }, emphasis: { scale: true, scaleSize: 6 }, data: modelShares.map(item => ({ name: label(item.model), value: microToChartNumber(item.amount_micro_usd), ...(item.isReward ? { itemStyle: { color: REWARD_COLOR } } : {}) })) }],
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
      // Reward rows are lump sums, so both axes use task-only values.
      { name: '任务数', type: 'bar', data: filledBuckets.map(bucket => bucket.task_record_count), itemStyle: { color: '#5b8def' } },
      { name: '单笔均值', type: 'line', yAxisIndex: 1, smooth: true, itemStyle: { color: '#f2994a' }, data: filledBuckets.map(bucket => bucket.task_record_count ? microToChartNumber((BigInt(bucket.task_micro_usd) / BigInt(bucket.task_record_count)).toString()) : 0) },
    ],
  };
}

export function hourDistributionOption(hourDaily, modelOrder, chartWidth) {
  const hours = Array.from({ length: 24 }, (_, hour) => hour);
  const models = modelOrder.length ? modelOrder : [...new Set(hourDaily.map(row => row.model))];
  if (!hourDaily.length) return emptyOption('暂无时段数据');
  const label = modelLabel(models);
  const legendLabels = models.map(label);
  return {
    tooltip: { trigger: 'axis', valueFormatter: value => money(String(Math.round(Number(value) * 1e6))) },
    legend: legendOption({ data: legendLabels }),
    grid: { left: 48, right: 16, top: 24, bottom: legendReserve(legendLabels, chartWidth) + LEGEND_AXIS_RESERVE },
    xAxis: { type: 'category', data: hours.map(hour => `${String(hour).padStart(2, '0')}:00`) },
    yAxis: { type: 'value', name: '小时日均 USD' },
    series: models.map(model => ({
      name: label(model),
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
  const label = modelLabel(models);
  return {
    tooltip: { position: 'top' },
    grid: { left: 148, right: 16, top: 16, bottom: 40 },
    xAxis: { type: 'category', data: Array.from({ length: 24 }, (_, hour) => `${String(hour).padStart(2, '0')}:00`) },
    yAxis: { type: 'category', data: models.map(label), axisLabel: { overflow: 'truncate', width: 128 } },
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
  const label = modelLabel(rows.map(row => row.model));
  // Three bars per model: past roughly six models the names no longer fit side by side.
  const rotated = rows.length > 6;
  return {
    tooltip: { trigger: 'axis' },
    legend: legendOption(),
    grid: { left: 48, right: 16, top: 24, bottom: rotated ? 76 : 40 },
    xAxis: {
      type: 'category',
      data: rows.map(row => label(row.model)),
      axisLabel: rotated
        ? { overflow: 'truncate', width: 72, interval: 0, rotate: 40, fontSize: 10, margin: 10 }
        : { overflow: 'truncate', width: 96, interval: 0 },
    },
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

function bucketLabels(buckets) {
  const format = bucketLabel(buckets);
  return buckets.map(bucket => format(bucket.from));
}

function trendGrid(count, labels = [], chartWidth) {
  const slider = count >= 8;
  const bottom = legendReserve(labels, chartWidth) + (slider ? 26 : 0) + LEGEND_AXIS_RESERVE;
  if (count >= 6) return { left: 48, right: 16, top: 24, bottom };
  const used = Math.min(70, Math.max(36, count * 16));
  const side = (100 - used) / 2;
  return { left: `${Math.max(side, 8)}%`, right: `${Math.max(side, 4)}%`, top: 24, bottom };
}

function trendZoom(count, labels = [], chartWidth) {
  // The slider sits just above the legend row rather than overlapping it.
  return count >= 8 ? [{ type: 'inside' }, { type: 'slider', height: 18, bottom: legendReserve(labels, chartWidth) }] : [{ type: 'inside' }];
}

// Series legends wrap onto extra rows instead of paginating, so every model name stays
// visible at once. The row count is measured from the real label widths (see
// `legendLayout`) and the canvas grows by the extra rows, keeping the plot area intact.
const LEGEND_ROW_HEIGHT = 26;
const LEGEND_ITEM_GAP = 16;
const LEGEND_ITEM_WIDTH = 8;
const LEGEND_FONT = '12px "Avenir Next","Poppins","PingFang SC","Hiragino Sans GB","Microsoft YaHei",sans-serif';
const LEGEND_H_PADDING = 20;
const LEGEND_AXIS_RESERVE = 14;

let measureContext = null;
function textWidth(text) {
  if (typeof document === 'undefined') return String(text).length * 7;
  if (!measureContext) measureContext = document.createElement('canvas').getContext('2d');
  if (!measureContext) return String(text).length * 7;
  measureContext.font = LEGEND_FONT;
  return measureContext.measureText(String(text)).width;
}

// Greedy wrap over the same items ECharts lays out, so the reserved band matches the
// legend instead of guessing from the number of series.
export function legendLayout(labels, chartWidth) {
  const available = Math.max(120, (chartWidth || 640) - LEGEND_H_PADDING);
  let rows = 1, used = 0;
  for (const label of labels) {
    const width = LEGEND_ITEM_WIDTH + 6 + textWidth(label) + LEGEND_ITEM_GAP;
    if (used > 0 && used + width > available) { rows += 1; used = 0; }
    used += width;
  }
  return { rows, height: rows * LEGEND_ROW_HEIGHT };
}

export function legendExtraHeight(labels, chartWidth) {
  return legendLayout(labels, chartWidth).height - LEGEND_ROW_HEIGHT;
}

function legendOption(extra = {}) {
  return { bottom: 0, itemGap: LEGEND_ITEM_GAP, itemWidth: LEGEND_ITEM_WIDTH, ...extra };
}
// Room the wrapped legend needs below the axis labels.
function legendReserve(labels, chartWidth) {
  return legendLayout(labels, chartWidth).height;
}

function chartMoney(value) {
  return money(String(Math.round(Number(value) * 1e6)));
}

function tooltipRow(name, value, marker = '') {
  return `${marker}${name}<span style="float:right;margin-left:20px">${chartMoney(value)}</span>`;
}

function trendTooltip(params) {
  if (!Array.isArray(params) || !params.length) return '';
  const title = params[0].axisValueLabel || params[0].axisValue;
  return [title, ...params.map(item => tooltipRow(item.seriesName, item.value, item.marker))].join('<br/>');
}

function emptyOption(text) {
  return { title: { text, left: 'center', top: 'middle', textStyle: { color: '#8a8f98', fontSize: 13, fontWeight: 400 } }, series: [] };
}
