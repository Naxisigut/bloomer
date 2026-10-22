import { DASHBOARD_TIMEZONE, dateKey, money, sumMoney, timeRange } from './data.js';

const HOUR = 3_600_000;
const TOTAL_COLOR = '#5b8def';
const colors = ['#f2994a', '#8b7cf6', '#2bb3a3', '#e5677e', '#9ab843', '#c08a4f', '#3f6fd8', '#d4a72c'];

// Choose exact 1/2/5 monetary steps before computing labels or drawing coordinates.
export function revenueAxis(amounts) {
  let low = 0n, high = 0n;
  for (const amount of amounts) {
    const value = BigInt(amount);
    if (value < low) low = value;
    if (value > high) high = value;
  }
  const padded = value => (value * 105n + 99n) / 100n;
  low = -padded(-low); high = padded(high);
  if (low === high) high = 1_000_000n;
  const roughStep = (high - low + 4n) / 5n;
  const magnitude = 10n ** BigInt(roughStep.toString().length - 1);
  const step = [1n, 2n, 5n, 10n].map(factor => factor * magnitude).find(value => value >= roughStep);
  const first = -((-low + step - 1n) / step), last = (high + step - 1n) / step;
  const digits = Math.max(2, 6 - (step.toString().length - 1));
  const ticks = [];
  for (let index = first; index <= last; index++) {
    const amount = index * step;
    ticks.push({ value: Number(amount) / 1e6, label: money(amount, digits) });
  }
  return { low: ticks[0].value, high: ticks.at(-1).value, ticks };
}

// Successful queries include zero earnings for elapsed hours, never future hours.
export function hourlyPoints(items, range) {
  const from = Date.parse(range.from), to = Date.parse(range.to);
  if (!Number.isFinite(from) || !Number.isFinite(to) || to <= from) return [];
  const saved = new Map(items.map(item => [Date.parse(item.from), item])
    .filter(([instant]) => instant >= from && instant < to));
  const points = [], lastHour = Math.min(from + 23 * HOUR, Math.floor(to / HOUR) * HOUR);
  for (let instant = from; instant <= lastHour; instant += HOUR) {
    points.push({ from: new Date(instant).toISOString(), to: new Date(instant + HOUR).toISOString(),
      record_count: 0, amount_micro_usd: '0', ...saved.get(instant), instant });
  }
  return points;
}

// Monotone cubic tangents smooth joins without inventing peaks or dipping below zero.
export function smoothLinePath(points) {
  if (!points.length) return '';
  const slopes = points.slice(1).map((point, index) => (point.y - points[index].y) / (point.x - points[index].x));
  const tangents = points.map((point, index) => {
    if (index === 0) return slopes[0] || 0;
    if (index === points.length - 1) return slopes.at(-1) || 0;
    const before = slopes[index - 1], after = slopes[index];
    if (before * after <= 0) return 0;
    const previousWidth = point.x - points[index - 1].x, nextWidth = points[index + 1].x - point.x;
    const a = 2 * nextWidth + previousWidth, b = nextWidth + 2 * previousWidth;
    return (a + b) / (a / before + b / after);
  });
  let path = `M${points[0].x},${points[0].y}`;
  for (let index = 1; index < points.length; index++) {
    const before = points[index - 1], point = points[index], third = (point.x - before.x) / 3;
    path += ` C${before.x + third},${before.y + tangents[index - 1] * third} ${point.x - third},${point.y - tangents[index] * third} ${point.x},${point.y}`;
  }
  return path;
}

export function createHomeRevenueChart({ query, selectedDevice, currentView, escape, correctedNow = () => new Date() }) {
  const $ = selector => document.querySelector(selector);
  const chart = $('#home-revenue-chart'), choices = $('#home-revenue-models');
  const hourLabel = new Intl.DateTimeFormat('zh-CN', { timeZone: DASHBOARD_TIMEZONE, hour: '2-digit', minute: '2-digit', hourCycle: 'h23' });
  const state = {
    device: null, day: null, range: null, all: null, models: [], showTotal: true, selected: new Set(),
    series: new Map(), pending: new Set(), seriesErrors: new Map(), errors: [], loading: false,
    generation: 0, controller: null,
  };
  const colorFor = model => colors[state.models.indexOf(model) % colors.length];

  function renderChoices() {
    choices.innerHTML = `<label class="home-model-choice" style="--series-color:${TOTAL_COLOR}"><input type="checkbox" data-home-total ${state.showTotal ? 'checked' : ''}><i aria-hidden="true"></i><span>全部收益</span></label>` + state.models.map(model => `<label class="home-model-choice" style="--series-color:${colorFor(model)}"><input type="checkbox" data-home-model value="${escape(model)}" ${state.selected.has(model) ? 'checked' : ''}><i aria-hidden="true"></i><span>${escape(model)}</span></label>`).join('');
  }

  function render() {
    if (currentView() !== 'overview') return;
    $('#home-revenue-total').textContent = money(state.all ? sumMoney(state.all.items) : null, 4);
    const errors = [...state.errors, ...[...state.seriesErrors].filter(([model]) => state.selected.has(model)).map(([model, error]) => `${model}：${error}`)];
    const status = $('#home-revenue-status');
    status.textContent = errors.join('；'); status.hidden = !errors.length;
    chart.setAttribute('aria-busy', String(state.loading || state.pending.size > 0));
    const series = [
      ...(state.showTotal && state.all ? [{ name: '全部收益', color: TOTAL_COLOR, items: state.all.items }] : []),
      ...state.models.filter(model => state.selected.has(model) && state.series.has(model))
        .map(model => ({ name: model, color: colorFor(model), items: state.series.get(model).items })),
    ].map(item => ({ ...item, points: hourlyPoints(item.items, state.range) }));
    const allPoints = series.flatMap(item => item.points);
    if (!allPoints.length) {
      const message = !state.showTotal && !state.selected.size ? '选择“全部收益”或勾选模型，可同时查看多条折线。'
        : state.loading || state.pending.size ? '正在读取当天收益…'
        : errors.length ? '部分收益数据暂不可用，请刷新重试。' : '今天暂无所选模型的收益记录';
      chart.innerHTML = `<div class="empty-state">${message}</div>`;
      return;
    }
    const width = Math.max(600, chart.clientWidth), height = chart.clientHeight || 260;
    const axis = revenueAxis(allPoints.map(point => point.amount_micro_usd));
    const left = Math.max(47, ...axis.ticks.map(tick => tick.label.length * 6 + 10)), right = 18, top = 14, bottom = 28;
    const plotWidth = width - left - right, plotHeight = height - top - bottom;
    // Micro-USD amounts stay exact; only drawing coordinates use floating point.
    const { low, high } = axis;
    const y = value => top + (high - value) / (high - low) * plotHeight;
    const start = Date.parse(state.range.from);
    const x = instant => left + (instant - start) / (23 * HOUR) * plotWidth;
    let svg = '';
    for (const { value, label } of axis.ticks) {
      svg += `<line class="chart-grid" x1="${left}" x2="${width - right}" y1="${y(value)}" y2="${y(value)}"/><text class="chart-label" x="${left - 10}" y="${y(value) + 3}" text-anchor="end">${escape(label)}</text>`;
    }
    const labelEvery = plotWidth / 23 < 36 ? 2 : 1;
    const ticks = Array.from({ length: 24 }, (_, hour) => hour).filter(hour => hour % labelEvery === 0);
    for (const hour of ticks) svg += `<text class="chart-label" x="${x(start + hour * HOUR)}" y="${height - 5}" text-anchor="middle">${String(hour).padStart(2, '0')}:00</text>`;
    const points = [], baseline = y(Math.min(Math.max(0, low), high));
    let defs = '';
    for (const [lineIndex, line] of series.entries()) {
      const coords = line.points.map(point => ({ x: x(point.instant), y: y(Number(BigInt(point.amount_micro_usd)) / 1e6) }));
      const path = smoothLinePath(coords);
      if (lineIndex === 0) {
        defs = `<defs><linearGradient id="home-revenue-area" x1="0" y1="0" x2="0" y2="1"><stop offset="0" stop-color="${line.color}" stop-opacity=".22"/><stop offset="1" stop-color="${line.color}" stop-opacity="0"/></linearGradient></defs>`;
        svg += `<path fill="url(#home-revenue-area)" d="${path} L${coords.at(-1).x},${baseline} L${coords[0].x},${baseline} Z"/>`;
      }
      svg += `<path class="home-revenue-line" stroke="${line.color}" d="${path}"/>`;
      for (const point of line.points) {
        const index = points.push({ ...point, color: line.color, name: line.name }) - 1;
        const label = `${hourLabel.format(new Date(point.instant))} · ${line.name} · ${money(point.amount_micro_usd, 4)}`;
        svg += `<circle class="home-revenue-point" data-home-point="${index}" tabindex="0" role="img" aria-label="${escape(label)}" fill="${line.color}" cx="${x(point.instant)}" cy="${y(Number(BigInt(point.amount_micro_usd)) / 1e6)}" r="3.5"/>`;
      }
    }
    chart.innerHTML = `<svg viewBox="0 0 ${width} ${height}" role="group" aria-label="当天按小时收益折线图，无收益小时显示零值点，未来小时保持空白">${defs}${svg}</svg><div class="chart-crosshair" hidden></div><div class="chart-tooltip home-revenue-tooltip" hidden></div>`;
    const tooltip = chart.querySelector('.chart-tooltip'), crosshair = chart.querySelector('.chart-crosshair');
    const showPoint = event => {
      const target = event.target.closest('[data-home-point]');
      if (!target) return;
      const point = points[Number(target.dataset.homePoint)];
      tooltip.innerHTML = `<span class="tooltip-time">${escape(hourLabel.format(new Date(point.instant)))}</span><span class="tooltip-row"><i style="background:${point.color}"></i>${escape(point.name)}<strong>${escape(money(point.amount_micro_usd, 4))}</strong></span>`;
      tooltip.hidden = false;
      const bounds = chart.getBoundingClientRect(), dot = target.getBoundingClientRect();
      const gap = 10, tooltipWidth = tooltip.offsetWidth, tooltipHeight = tooltip.offsetHeight;
      const center = dot.left - bounds.left + dot.width / 2;
      const left = center - tooltipWidth / 2;
      const above = dot.top - bounds.top - tooltipHeight - gap;
      const top = above >= gap ? above : dot.bottom - bounds.top + gap;
      tooltip.style.left = `${chart.scrollLeft + Math.max(gap, Math.min(left, chart.clientWidth - tooltipWidth - gap))}px`;
      tooltip.style.top = `${Math.max(gap, Math.min(top, chart.clientHeight - tooltipHeight - gap))}px`;
      crosshair.style.left = `${chart.scrollLeft + center}px`;
      crosshair.hidden = false;
    };
    const hidePoint = () => { tooltip.hidden = true; crosshair.hidden = true; };
    chart.onpointerover = showPoint;
    chart.onpointerleave = hidePoint;
    chart.onpointerout = event => { if (event.target.closest('[data-home-point]')) hidePoint(); };
    chart.querySelectorAll('[data-home-point]').forEach(point => { point.onfocus = showPoint; point.onblur = hidePoint; });
  }

  async function loadSelected() {
    if (!state.range || state.loading) return;
    const generation = state.generation, device = state.device, range = { ...state.range }, signal = state.controller.signal;
    const missing = [...state.selected].filter(model => !state.series.has(model) && !state.pending.has(model) && !state.seriesErrors.has(model));
    await Promise.all(missing.map(async model => {
      state.pending.add(model); render();
      try {
        const response = await query('stats', { ...range, group_by: 'hour', timezone: DASHBOARD_TIMEZONE, model }, device, signal);
        if (generation === state.generation) state.series.set(model, response);
      } catch (error) {
        if (generation === state.generation && error.name !== 'AbortError') state.seriesErrors.set(model, error.message);
      } finally {
        if (generation === state.generation) { state.pending.delete(model); render(); }
      }
    }));
  }

  async function refresh() {
    const device = selectedDevice()?.id;
    if (!device) return;
    if (state.device !== device) { state.selected.clear(); state.showTotal = true; state.models = []; }
    state.device = device;
    state.controller?.abort(); state.controller = new AbortController();
    const generation = ++state.generation, now = correctedNow();
    state.day = dateKey(now, DASHBOARD_TIMEZONE);
    state.range = timeRange('1', DASHBOARD_TIMEZONE, now);
    state.all = null; state.series.clear(); state.pending.clear(); state.seriesErrors.clear(); state.errors = []; state.loading = true;
    renderChoices(); render();
    const params = { ...state.range, timezone: DASHBOARD_TIMEZONE };
    const results = await Promise.allSettled([
      query('stats', { ...params, group_by: 'hour' }, device, state.controller.signal),
      query('stats', { ...params, group_by: 'model' }, device, state.controller.signal),
    ]);
    if (generation !== state.generation) return;
    if (results[0].status === 'fulfilled') state.all = results[0].value;
    else state.errors.push(`全部收益：${results[0].reason.message}`);
    if (results[1].status === 'fulfilled') state.models = [...new Set([...results[1].value.items.map(item => item.key), ...state.selected])].sort();
    else state.errors.push(`模型列表：${results[1].reason.message}`);
    state.loading = false; renderChoices(); render();
    await loadSelected();
  }

  choices.addEventListener('change', event => {
    const input = event.target;
    if (input.hasAttribute('data-home-total')) state.showTotal = input.checked;
    else if (input.hasAttribute('data-home-model')) {
      if (input.checked) state.selected.add(input.value); else state.selected.delete(input.value);
    } else return;
    render(); loadSelected();
  });
  new ResizeObserver(render).observe(chart);
  renderChoices();
  return { render, refresh };
}
