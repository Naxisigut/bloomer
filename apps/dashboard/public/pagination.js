// Cursor checkpoints are scoped to one frozen set of device/time/model filters.
export class CursorPager {
  constructor(load) {
    this.load = load;
    this.checkpoints = new Map([[0, null]]);
    this.end = null;
  }

  async read(offset, cursor, limit, signal) {
    signal?.throwIfAborted();
    const result = await this.load({ cursor, limit, signal });
    signal?.throwIfAborted();
    if (!Array.isArray(result.items) || result.items.length > limit ||
      (result.next_cursor && (!result.items.length || result.next_cursor === cursor))) {
      throw new Error('设备返回的分页数据无效，请刷新后重试。');
    }
    const nextOffset = offset + result.items.length;
    if (result.next_cursor) this.checkpoints.set(nextOffset, result.next_cursor);
    else this.end = nextOffset;
    return result;
  }

  async page(index, size, { signal } = {}) {
    if (!Number.isSafeInteger(index) || index < 0 || !Number.isInteger(size) || size < 1 || size > 500 || !Number.isSafeInteger(index * size)) {
      throw new Error('页码或每页条数无效。');
    }
    signal?.throwIfAborted();
    if (this.end !== null) index = Math.min(index, Math.max(0, Math.ceil(this.end / size) - 1));
    const target = index * size;
    let offset = 0, cursor = null;
    for (const [knownOffset, knownCursor] of this.checkpoints) {
      if (knownOffset <= target && knownOffset >= offset) { offset = knownOffset; cursor = knownCursor; }
    }
    const seen = new Set(cursor ? [cursor] : []);
    while (offset < target) {
      const result = await this.read(offset, cursor, Math.min(500, target - offset), signal);
      offset += result.items.length;
      if (!result.next_cursor) return this.page(index, size, { signal });
      if (seen.has(result.next_cursor)) throw new Error('设备返回了重复游标，请刷新后重试。');
      seen.add(result.next_cursor);
      cursor = result.next_cursor;
    }
    const result = await this.read(offset, cursor, size, signal);
    // Retention cleanup may shorten a previously visited range.
    if (!result.items.length && index > 0) return this.page(index, size, { signal });
    return { ...result, index };
  }
}
