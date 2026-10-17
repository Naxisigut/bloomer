import { readFile, mkdir, writeFile, rename, chmod } from 'node:fs/promises';
import { homedir } from 'node:os';
import path from 'node:path';
import { randomUUID } from 'node:crypto';

export class AppError extends Error {
  constructor(status, code, message) { super(message); this.status = status; this.code = code; }
}

export function validateDevice(input) {
  if (!input || typeof input !== 'object' || Array.isArray(input)) throw new AppError(400, 'invalid_device', '设备连接信息格式无效。');
  const name = typeof input.name === 'string' ? input.name.trim() : '';
  const token = typeof input.token === 'string' ? input.token.trim() : '';
  if (!name || name.length > 60) throw new AppError(400, 'invalid_device', '请填写 1～60 字的设备名称。');
  if (!token || token.length > 4096 || /[\s\x00-\x1f\x7f]/.test(token)) throw new AppError(400, 'invalid_token', '请填写有效的 Bloomer 独立访问令牌。');
  let url;
  try { url = new URL(input.url); } catch { throw new AppError(400, 'invalid_url', '请输入完整的设备地址。'); }
  const loopback = ['localhost', '127.0.0.1', '[::1]'].includes(url.hostname);
  const tailscale = url.hostname.endsWith('.ts.net') && url.hostname.length > 7;
  if ((!loopback && !(tailscale && url.protocol === 'https:')) || !['http:', 'https:'].includes(url.protocol) || url.username || url.password || url.search || url.hash || url.pathname !== '/') {
    throw new AppError(400, 'invalid_url', '地址须为本机回环地址，或 HTTPS 的 Tailscale .ts.net 地址；请勿附加路径。');
  }
  return { name, url: url.origin, token };
}

export const publicDevice = ({ id, name, url, source }) => ({ id, name, url, source: source || 'saved' });

export class DeviceStore {
  constructor({ dataDir, agentDir } = {}) {
    const base = process.platform === 'darwin' ? path.join(homedir(), 'Library/Application Support/Bloomer') : path.join(homedir(), '.local/share/bloomer');
    this.file = path.join(dataDir || process.env.BLOOMER_DASHBOARD_DATA_DIR || path.join(base, 'dashboard'), 'devices.json');
    this.agentDir = agentDir === undefined ? path.join(base, 'agent') : agentDir;
    this.devices = [];
    this.local = null;
    this.queue = Promise.resolve();
  }
  async load() {
    try {
      const saved = JSON.parse(await readFile(this.file, 'utf8'));
      if (saved.version !== 1 || !Array.isArray(saved.devices) || saved.devices.length > 30) throw new Error('invalid');
      const ids = new Set();
      this.devices = saved.devices.map(device => {
        if (typeof device.id !== 'string' || !/^[a-f0-9-]{36}$/.test(device.id) || ids.has(device.id)) throw new Error('invalid');
        ids.add(device.id);
        return { id: device.id, ...validateDevice(device) };
      });
    } catch (error) {
      if (error.code !== 'ENOENT') throw new AppError(500, 'config_unavailable', '设备配置无法读取，请检查私有配置文件。');
    }
    await this.discoverLocal();
  }
  async discoverLocal() {
    if (!this.agentDir) return;
    try {
      const configFile = path.join(this.agentDir, 'config.json');
      const config = JSON.parse(await readFile(configFile, 'utf8'));
      const tokenPath = config.access_token_file || path.join(this.agentDir, 'access.token');
      const expanded = tokenPath.startsWith('~/') ? path.join(homedir(), tokenPath.slice(2)) : path.resolve(this.agentDir, tokenPath);
      const token = (await readFile(expanded, 'utf8')).trim();
      this.local = { id: 'local-agent', source: 'local', ...validateDevice({ name: '本机设备', url: `http://${config.listen || '127.0.0.1:17890'}`, token }) };
    } catch { this.local = null; }
  }
  list() { return [...(this.local ? [this.local] : []), ...this.devices]; }
  async get(id) {
    if (id === 'local-agent') await this.discoverLocal();
    const device = this.list().find(item => item.id === id);
    if (!device) throw new AppError(404, 'device_not_found', '设备不存在，请重新选择。');
    return device;
  }
  mutate(operation) {
    const task = this.queue.then(async () => {
      const next = operation(this.devices);
      const dir = path.dirname(this.file);
      await mkdir(dir, { recursive: true, mode: 0o700 });
      await chmod(dir, 0o700);
      const temp = `${this.file}.${randomUUID()}.tmp`;
      await writeFile(temp, JSON.stringify({ version: 1, devices: next }, null, 2) + '\n', { mode: 0o600, flag: 'wx' });
      await rename(temp, this.file);
      this.devices = next;
    });
    this.queue = task.catch(() => {});
    return task;
  }
  async add(input) {
    const device = { id: randomUUID(), ...validateDevice(input) };
    await this.mutate(devices => {
      if (devices.length >= 30) throw new AppError(400, 'device_limit', '最多保存 30 台设备。');
      if (this.list().some(item => item.url === device.url)) throw new AppError(409, 'device_exists', '这个设备地址已经添加。');
      return [...devices, device];
    });
    return publicDevice(device);
  }
  async remove(id) {
    if (id === 'local-agent') throw new AppError(400, 'local_device', '本机设备由安装配置自动识别，无需移除。');
    await this.mutate(devices => {
      if (!devices.some(device => device.id === id)) throw new AppError(404, 'device_not_found', '设备不存在。');
      return devices.filter(device => device.id !== id);
    });
  }
}
