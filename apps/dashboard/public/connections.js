import { AppError } from './errors.js';
import { randomID } from './identifiers.js';

export function validateDevice(input) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new AppError(400, 'invalid_device', '设备连接信息格式无效。');
  const name = typeof input.name === 'string' ? input.name.trim() : '';
  const token = typeof input.token === 'string' ? input.token.trim() : '';
  if (!name || name.length > 60) throw new AppError(400, 'invalid_device', '请填写 1～60 字的设备名称。');
  if (!token || token.length > 4096 || /[\s\x00-\x1f\x7f]/.test(token)) throw new AppError(400, 'invalid_token', '请填写有效的 Bloomer access.token。');
  let url;
  try { url = new URL(input.url); } catch { throw new AppError(400, 'invalid_url', '请输入完整的设备地址。'); }
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  const tailscale = url.hostname.endsWith('.ts.net') && url.hostname.length > 7;
  if ((!loopback && !(tailscale && url.protocol === 'https:')) || !['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new AppError(400, 'invalid_url', '地址须为本机回环地址，或 HTTPS 的 Tailscale .ts.net 地址；请包含 HTTPS 入口端口（通常 8443），勿附加 /v1。');
  }
  return { name, url: url.origin, token };
}

export const CONNECTIONS_KEY = 'bloomer.connections.v1';
const storageError = () => new AppError(400, 'storage_unavailable', '浏览器无法保存连接。请允许此网站使用本地存储，再测试并保存。');
const invalidFile = () => new AppError(400, 'invalid_connections', '连接文件无效，请使用包含 version: 1 和 devices 列表的 JSON 文件。');
export const publicDevice = ({ id, name, url }) => ({ id, name, url, source: 'saved' });

export function parseConnections(input) {
  if (!input || input.version !== 1 || !Array.isArray(input.devices) || input.devices.length > 30) throw invalidFile();
  const devices = input.devices.map(validateDevice);
  if (new Set(devices.map(device => device.url)).size !== devices.length) throw new AppError(409, 'device_exists', '连接文件中存在重复的设备地址。');
  return devices;
}

// Only browser storage is used. No Agent files are read or discovered.
export class DeviceStore {
  constructor({ storage, makeID = randomID } = {}) {
    this.storage = storage; this.makeID = makeID; this.devices = []; this.selected = 'demo'; this.loaded = false;
  }
  backend() { return this.storage ?? globalThis.localStorage; }
  load() {
    let saved;
    try { saved = this.backend().getItem(CONNECTIONS_KEY); } catch { throw storageError(); }
    if (saved !== null) {
      try {
        const input = JSON.parse(saved), devices = parseConnections(input), ids = new Set();
        this.devices = devices.map((device, index) => {
          const id = input.devices[index].id;
          if (typeof id !== 'string' || !/^[a-f0-9-]{36}$/.test(id) || ids.has(id)) throw invalidFile();
          ids.add(id); return { ...device, id };
        });
        this.selected = input.selected === 'demo' || ids.has(input.selected) ? input.selected : this.devices[0]?.id || 'demo';
      } catch { throw new AppError(400, 'invalid_connections', '浏览器中的设备连接无法读取，请先备份或清除此网站数据，再重新添加或导入。'); }
    }
    this.loaded = true;
  }
  list() { return this.devices.map(publicDevice); }
  get(id) {
    const device = this.devices.find(item => item.id === id);
    if (!device) throw new AppError(404, 'device_not_found', '设备不存在，请重新选择。');
    return { ...device };
  }
  commit(devices, selected = this.selected) {
    if (!this.loaded) throw storageError();
    try { this.backend().setItem(CONNECTIONS_KEY, JSON.stringify({ version: 1, devices, selected })); } catch { throw storageError(); }
    this.devices = devices; this.selected = selected;
  }
  select(id) {
    if (id !== 'demo') this.get(id);
    this.commit(this.devices, id);
  }
  add(input) { return this.addMany([validateDevice(input)])[0]; }
  addMany(inputs) {
    const devices = inputs.map(validateDevice);
    if (this.devices.length + devices.length > 30) throw new AppError(400, 'device_limit', '最多保存 30 台设备。');
    const urls = [...this.devices, ...devices].map(device => device.url);
    if (new Set(urls).size !== urls.length) throw new AppError(409, 'device_exists', '这个设备地址已经添加。');
    const added = devices.map(device => ({ ...device, id: this.makeID() }));
    this.commit([...this.devices, ...added], added[0]?.id || this.selected);
    return added.map(publicDevice);
  }
  remove(id) {
    this.get(id);
    const devices = this.devices.filter(device => device.id !== id);
    this.commit(devices, this.selected === id ? devices[0]?.id || 'demo' : this.selected);
    try { this.backend().removeItem(`bloomer.control.${id}`); } catch { /* No connection or token remains. */ }
  }
}
