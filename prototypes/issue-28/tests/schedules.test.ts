import { describe, expect, it } from 'vitest';
import { MockAdapter, seed, STORAGE_KEY, type StorageLike } from '../src/adapter';
import { draftRequest, emptyDraft, parseLocal, timeBounds, validateSchedule } from '../src/schedule-model';
import type { ScheduleType } from '../src/contracts';

/** 每个测试拥有独立持久化快照，便于检查失败前后的完整事实。 */
function storage(): StorageLike { const data = new Map<string, string>(); return { getItem: key => data.get(key) ?? null, setItem: (key, value) => { data.set(key, value); } }; }
/** 固定测试时钟，避免跨日期运行导致单次边界用例漂移。 */
function now(): Date { return new Date('2026-09-15T12:00'); }
/** 提供与 Figma 对应的四种创建输入，初始日为周日及月末。 */
function request(type: ScheduleType = 'single') { return draftRequest({ title: '  读完一本喜欢的书  ', notes: '留半小时给自己。', start: '2027-01-31T19:00', end: '2027-01-31T19:30', type, until: '2027-03-31' }); }

describe('日程定义与模拟存储', () => {
  it.each(['single', 'daily', 'weekly', 'monthly'] as const)('%s 保存、重开、永久删除，不修改待处理消息', async type => {
    const store = storage(); const adapter = new MockAdapter(store, 1, now); const messages = await adapter.list();
    const item = await adapter.createSchedule(request(type));
    expect(item).toMatchObject({ title: '读完一本喜欢的书', durationMinutes: 30, revision: 1 });
    expect(item.cron).toBe({ single: null, daily: '0 19 * * *', weekly: '0 19 * * 0', monthly: '0 19 31 * *' }[type]);
    expect(item.monthEndPolicy).toBe(type === 'monthly' ? 'clamp' : null);
    const reopened = new MockAdapter(store, 1, now); expect(await reopened.getSchedule(item.id)).toEqual(item);
    await reopened.deleteSchedule(item.id, item.revision);
    expect(await new MockAdapter(store, 1, now).listSchedules()).toEqual([]);
    await expect(reopened.getSchedule(item.id)).rejects.toMatchObject({ code: 'NOT_FOUND' });
    expect(await reopened.list()).toEqual(messages);
  });
  it('旧快照迁移为空日程集合，保留设置与消息', async () => {
    const store = storage(); const legacy: Partial<ReturnType<typeof seed>> = seed(); delete legacy.schedules;
    store.setItem(STORAGE_KEY, JSON.stringify(legacy)); const adapter = new MockAdapter(store, 1, now);
    expect(await adapter.listSchedules()).toEqual([]); expect(await adapter.list()).toHaveLength(3);
    await adapter.createSchedule(request()); expect(await new MockAdapter(store, 1, now).listSchedules()).toHaveLength(1);
  });
  it('损坏的日程快照明确报错且不覆盖', async () => {
    const store = storage(); const raw = JSON.stringify({ ...seed(), schedules: [{ title: 'broken' }] }); store.setItem(STORAGE_KEY, raw);
    await expect(new MockAdapter(store).listSchedules()).rejects.toThrow('无法读取'); expect(store.getItem(STORAGE_KEY)).toBe(raw);
  });
  it('创建失败可重试；删除失败保留全部定义', async () => {
    const adapter = new MockAdapter(storage(), 1, now); adapter.inject('schedule-create');
    await expect(adapter.createSchedule(request())).rejects.toMatchObject({ code: 'STORAGE' }); expect(await adapter.listSchedules()).toEqual([]);
    const item = await adapter.createSchedule(request()); adapter.inject('schedule-delete');
    await expect(adapter.deleteSchedule(item.id, item.revision)).rejects.toMatchObject({ code: 'STORAGE' }); expect(await adapter.getSchedule(item.id)).toEqual(item);
    await adapter.deleteSchedule(item.id, item.revision); expect(await adapter.listSchedules()).toEqual([]);
  });
  it('旧版本不能删除最新定义，重新读取确认后才成功', async () => {
    const adapter = new MockAdapter(storage(), 1, now); const item = await adapter.createSchedule(request()); adapter.inject('schedule-stale');
    await expect(adapter.deleteSchedule(item.id, item.revision)).rejects.toMatchObject({ code: 'STALE_REVISION' });
    const fresh = await adapter.getSchedule(item.id); expect(fresh.revision).toBe(2); expect(fresh.title).toContain('已更新');
    await expect(adapter.deleteSchedule(item.id, item.revision)).rejects.toMatchObject({ code: 'STALE_REVISION' });
    await adapter.deleteSchedule(item.id, fresh.revision); expect(await adapter.listSchedules()).toEqual([]);
  });
  it('并发删除只能成功一次，另一请求明确返回不存在', async () => {
    const adapter = new MockAdapter(storage(), 1, now); const item = await adapter.createSchedule(request());
    const result = await Promise.allSettled([adapter.deleteSchedule(item.id, 1), adapter.deleteSchedule(item.id, 1)]);
    expect(result.filter(r => r.status === 'fulfilled')).toHaveLength(1);
    expect(result.find(r => r.status === 'rejected')).toMatchObject({ reason: { code: 'NOT_FOUND' } });
  });
  it('持久化失败不伪造成功，已有记录可重新读取', async () => {
    const store = storage(); const adapter = new MockAdapter(store, 1, now); const item = await adapter.createSchedule(request());
    const raw = store.getItem(STORAGE_KEY); store.setItem = () => { throw new Error('quota'); };
    await expect(adapter.createSchedule(request())).rejects.toThrow('写入失败');
    await expect(adapter.deleteSchedule(item.id, 1)).rejects.toThrow('写入失败'); expect(store.getItem(STORAGE_KEY)).toBe(raw);
  });
  it('重置会废弃尚未提交的创建，退出会拒绝新写入', async () => {
    const adapter = new MockAdapter(storage(), 5, now); const pending = adapter.createSchedule(request()); adapter.reset();
    await expect(pending).rejects.toThrow('场景已重置'); expect(await adapter.listSchedules()).toEqual([]);
    await adapter.exit(); await expect(adapter.createSchedule(request())).rejects.toThrow('正在退出');
  });
});

describe('页面提示与适配器最终校验', () => {
  it('必填与非法真实日期得到字段提示', () => {
    expect(() => draftRequest(emptyDraft())).toThrow('请检查');
    expect(parseLocal('2027-02-30T12:00')).toBeNull(); expect(parseLocal('2027-01-01T12:00:01')).toBeNull();
    expect(() => validateSchedule({ ...request(), start_unix: request().start_unix + 1 }, now())).toThrow('请检查');
    expect(() => validateSchedule({ ...request(), end_unix: request().start_unix }, now())).toThrow('请检查');
  });
  it('单次取当天零点、开始排除周年上界、结束包含上界', () => {
    const bounds = timeBounds(now()); expect(bounds).toEqual({ min: '2026-09-15T00:00', startMaxExclusive: '2027-09-15T00:00', endMaxInclusive: '2027-09-15T00:00' });
    const lower = new Date(bounds.min).getTime() / 1000; const upper = new Date(bounds.startMaxExclusive).getTime() / 1000;
    expect(validateSchedule({ ...request(), start_unix: lower, end_unix: lower + 60 }, now()).durationMinutes).toBe(1);
    expect(validateSchedule({ ...request(), start_unix: upper - 60, end_unix: upper }, now()).durationMinutes).toBe(1);
    expect(() => validateSchedule({ ...request(), start_unix: upper, end_unix: upper + 60 }, now())).toThrow('超过时间限制');
  });
  it('提交期间跨午夜时重新获取边界，失败不写存储', async () => {
    let clock = new Date('2026-09-15T23:59'); const store = storage(); const adapter = new MockAdapter(store, 5, () => clock);
    const pending = adapter.createSchedule({ ...request(), start_unix: new Date('2026-09-15T23:00').getTime() / 1000, end_unix: new Date('2026-09-15T23:30').getTime() / 1000 });
    clock = new Date('2026-09-16T00:00'); await expect(pending).rejects.toMatchObject({ code: 'TIME_RANGE_EXCEEDED', bounds: { min: '2026-09-16T00:00' } });
    expect(store.getItem(STORAGE_KEY)).toBeNull();
  });
  it('闰年夹取到 2 月 28 日，重复不受一年限制且截止包含开始日', () => {
    expect(timeBounds(new Date('2028-02-29T10:00')).startMaxExclusive).toBe('2029-02-28T00:00');
    const recurring = draftRequest({ ...emptyDraft(), title: '每月读书', type: 'monthly', start: '2030-01-31T19:00', end: '2030-01-31T19:30', until: '2030-01-31' });
    expect(validateSchedule(recurring, now())).toMatchObject({ untilDateLocal: '2030-01-31', cron: '0 19 31 * *', monthEndPolicy: 'clamp' });
    expect(() => validateSchedule({ ...recurring, until_unix: new Date('2030-01-30T12:00').getTime() / 1000 }, now())).toThrow('不能早于');
    expect(() => validateSchedule({ ...recurring, cron: '*/5 19 * * *' }, now())).toThrow('不受支持');
  });
});
