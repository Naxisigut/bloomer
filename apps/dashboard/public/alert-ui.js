export const alertHistoryLimit = 10;
const kinds = { coordinator_disconnected: '协调器连接中断', connection_flapping: '连接频繁重连', provider_not_running: 'Provider 进程停止', provider_state_unknown: 'Provider 状态未知', model_not_ready: '模型未就绪', no_earnings: '设备长时间无收益', test: '测试推送' };
const events = { triggered: '首次告警', reminder: '持续提醒', recovered: '已恢复', test: '测试推送' };
const delivery = { pending: '等待推送', retrying: '等待重试', sent: '推送成功', failed: '推送失败', superseded: '已被后续状态替代' };

export function visibleAlerts(items) {
  return Array.isArray(items) ? items.slice(0, alertHistoryLimit) : [];
}

export function alertRecordHTML(record, escape, dateTime) {
  const warning = record.level === 'critical' || record.delivery_status === 'failed';
  return `<article class="control-job alert-record"><span><strong>${escape(kinds[record.type] || record.type)} · ${escape(events[record.event] || record.event)}</strong><small>${escape(dateTime(record.created_at))} · ${escape(record.device_name)}</small><small class="alert-body">${escape(record.body)}</small><small>${escape(delivery[record.delivery_status] || '未知推送状态')} · 已尝试 ${escape(record.attempts)} 次${record.next_attempt_at ? ` · 下次重试 ${escape(dateTime(record.next_attempt_at))}` : ''}${record.error_code ? ' · 请在设备端检查 Bark 配置与网络' : ''}</small></span><span class="status-tag ${warning ? 'warning' : ''}">${record.recovered ? '已恢复' : record.level === 'critical' ? '严重' : '提醒'}</span></article>`;
}

export function createAlertUI({ query, selectedDevice, deviceChoices, selectDevice, currentView, escape, dateTime }) {
  const $ = selector => document.querySelector(selector);
  let deviceID, items = [], error = '', message = '', loading = false, testing = false, generation = 0;
  function render() {
    const device = selectedDevice();
    if (deviceID !== device?.id) { deviceID = device?.id; items = []; error = ''; message = ''; loading = false; testing = false; generation++; }
    $('#alert-device').innerHTML = deviceChoices().map(item => `<option value="${escape(item.id)}" ${item.id === deviceID ? 'selected' : ''}>${escape(item.name)}</option>`).join('');
    $('#alert-test').disabled = !device || device.id === 'demo' || testing;
    $('#alert-refresh').disabled = loading;
    $('#alert-message').textContent = error || message;
    $('#alert-message').hidden = !error && !message;
    $('#alert-message').className = `state-message${error ? ' warning' : ''}`;
    $('#alert-records').innerHTML = items.length ? items.map(record => alertRecordHTML(record, escape, dateTime)).join('') : `<p class="control-note">${loading ? '正在读取告警记录…' : '暂无告警记录'}</p>`;
  }
  async function refresh() {
    render(); const id = deviceID, request = ++generation; loading = true; error = ''; render();
    try { const response = await query('alerts', null, id); if (request === generation && selectedDevice()?.id === id) items = visibleAlerts(response.items); }
    catch (err) { if (request === generation && selectedDevice()?.id === id) error = err.message; }
    finally { if (request === generation) { loading = false; render(); } }
  }
  $('#alert-device').addEventListener('change', event => selectDevice(event.target.value));
  $('#alert-refresh').addEventListener('click', refresh);
  $('#alert-test').addEventListener('click', async () => {
    const id = deviceID; testing = true; error = ''; message = ''; render();
    try { await query('alert-test', null, id); if (selectedDevice()?.id === id) { message = '测试推送已加入队列，可在记录中查看发送结果。'; await refresh(); } }
    catch (err) { if (selectedDevice()?.id === id) error = err.message; }
    finally { if (selectedDevice()?.id === id) { testing = false; render(); } }
  });
  return { render, refresh, refreshIfVisible() { if (currentView() === 'alerts') return refresh(); } };
}
