const DEFAULT_NETWORK_INTERVAL_MINUTES = 5;

function leg(participate, ok, detail) {
  return { participate, ok, detail };
}

function earningsLeg(health) {
  if (health?.error) return leg(true, false, '收益采集状态读取失败');
  if (health?.collection_state === 'disabled') return leg(false, true, '');
  const synced = typeof health?.last_successful_sync_at === 'string' && Number.isFinite(Date.parse(health.last_successful_sync_at));
  const ok = health?.data_stale === false && synced;
  return leg(true, ok, ok ? '收益采集正常' : synced ? '收益采集已中断' : '收益尚未成功采集');
}

function deviceLeg(deviceState) {
  if (deviceState?.error) return leg(true, false, deviceState.code === 'device_state_unsupported' ? '运行状态接口不可用' : '运行状态读取失败');
  if (deviceState?.meta?.collection_enabled === false) return leg(false, true, '');
  const collected = deviceState?.sample && Number.isFinite(Date.parse(deviceState.sample.collected_at));
  const ok = deviceState?.meta?.data_stale === false && collected;
  return leg(true, ok, ok ? '运行状态采集正常' : collected ? '运行状态采集已中断' : '运行状态尚未采集');
}

function networkIntervalMinutes(networkRates) {
  const minutes = networkRates?.interval_minutes;
  return Number.isInteger(minutes) && minutes >= 1 && minutes <= 60 ? minutes : DEFAULT_NETWORK_INTERVAL_MINUTES;
}

function networkLeg(networkRates, now) {
  if (networkRates?.error) return leg(true, false, networkRates.code === 'network_rates_unsupported' ? '全网数据接口不可用' : '全网数据读取失败');
  if (networkRates?.enabled === false) return leg(false, true, '');
  const failed = typeof networkRates?.last_error === 'string' && networkRates.last_error !== '';
  const updatedAt = Date.parse(networkRates?.updated_at);
  const age = now - updatedAt;
  const fresh = !failed && Number.isFinite(updatedAt) && age >= -5000 && age <= networkIntervalMinutes(networkRates) * 2 * 60_000;
  let detail = '全网数据采集正常';
  if (!fresh) detail = failed ? '全网数据最近一次采集失败' : Number.isFinite(updatedAt) ? '全网数据采集已中断' : '全网数据尚未采集';
  return leg(true, fresh, detail);
}

function itemFromLeg(label, leg, loading) {
  if (loading) return { label, text: '读取中', className: 'warning', detail: `正在读取${label}` };
  if (!leg.participate) return { label, text: '已关闭', className: 'warning', detail: `${label}已关闭` };
  if (leg.ok) return { label, text: '正常', className: '', detail: leg.detail };
  return { label, text: '异常', className: 'offline', detail: leg.detail };
}

export function collectionStatusItems({ health, deviceState, networkRates, now = Date.now() } = {}) {
  const loading = health === undefined || deviceState === undefined || networkRates === undefined;
  return [
    itemFromLeg('收益采集', earningsLeg(health), loading),
    itemFromLeg('运行状态采集', deviceLeg(deviceState), loading),
    itemFromLeg('全网数据采集', networkLeg(networkRates, now), loading),
  ];
}

export function collectionStatus(args) {
  const items = collectionStatusItems(args);
  if (items.every(item => item.text === '读取中')) {
    return { label: '读取中', className: 'warning', detail: '正在读取采集状态' };
  }
  const participating = items.filter(item => item.text !== '已关闭');
  if (!participating.length) {
    return { label: '采集已关闭', className: 'warning', detail: '收益、运行状态与全网数据采集均已关闭。' };
  }
  const failed = participating.filter(item => item.text === '异常');
  if (!failed.length) {
    return { label: '采集正常', className: '', detail: participating.map(item => item.detail).join('；') };
  }
  return { label: '采集异常', className: 'offline', detail: failed.map(item => item.detail).join('；') };
}
