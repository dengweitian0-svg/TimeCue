import { describe, expect, it, vi } from 'vitest';
import { toScheduleError } from '../src/contracts';
import { WailsAdapter, type BindingsPort, type EventsOn } from '../src/wails-adapter';

const scheduleDTO = { id: 42, title: '深呼吸', notes: '离开屏幕', first_start_local: '2026-09-16T10:00', duration_minutes: 15, cron: null, until_date_local: null, month_end_policy: null, revision: 3 };

/** 创建一组最小绑定实现，测试只验证适配器的转换而不依赖生成文件。 */
function bindings(overrides: Partial<BindingsPort> = {}): BindingsPort {
  return {
    ListSchedules: vi.fn(async () => ({ items: [scheduleDTO], next_cursor: 'next', limit: 20 })),
    GetSchedule: vi.fn(async () => scheduleDTO),
    GetScheduleTimeBounds: vi.fn(async () => ({ min: '2026-09-16T00:00', start_max_exclusive: '2027-09-16T00:00', end_max_inclusive: '2027-09-16T00:00' })),
    CreateSchedule: vi.fn(async () => scheduleDTO),
    DeleteSchedule: vi.fn(async () => undefined),
    GetRuntimeStatus: vi.fn(async () => ({ visibility: 'visible', lifecycle: 'running', ready: true })),
    GetDesktopStatus: vi.fn(async () => ({ visibility: 'visible', lifecycle: 'running', ready: true, close_behavior: 'tray', activation: null })),
    GetCloseBehavior: vi.fn(async () => ({ behavior: 'tray' })),
    SaveCloseBehavior: vi.fn(async () => undefined),
    GetStorageDirectories: vi.fn(async () => ({ logs: { kind: 'logs', effective_path: 'C:/TimeCue/logs', saved_path: 'C:/TimeCue/logs', restart_required: false }, backups: { kind: 'backups', effective_path: 'C:/TimeCue/backups', saved_path: 'C:/TimeCue/backups', restart_required: false } })),
    ChooseStorageDirectory: vi.fn(async () => ''),
    SaveStorageDirectory: vi.fn(async ({ kind, path }: { kind: string; path: string }) => ({ kind, effective_path: 'C:/TimeCue/logs', saved_path: path, restart_required: true })),
    FrontendReady: vi.fn(async () => null),
    RetryCleanup: vi.fn(async () => undefined),
    ...overrides,
  };
}

describe('WailsAdapter', () => {
  it('maps the structured Go ErrorDTO fields array to UI field messages', () => {
    const dto = {
      code: 'VALIDATION',
      message: '时间范围无效。',
      fields: ['start_unix', 'end_unix'],
    };
    const error = toScheduleError({ data: JSON.stringify(dto) });
    expect(error.code).toBe('VALIDATION');
    expect(error.message).toBe('时间范围无效。');
    expect(error.fields).toEqual({ start: '时间范围无效。', end: '时间范围无效。' });
  });

  it('converts snake_case DTOs and keeps int64 IDs lossless', async () => {
    const port = bindings(); const adapter = new WailsAdapter(port);
    const page = await adapter.listSchedules({ limit: 20, cursor: 'previous' });
    expect(page.items[0].id).toBe('42');
    expect(page.nextCursor).toBe('next');
    expect(port.ListSchedules).toHaveBeenCalledWith({ limit: 20, cursor: 'previous' });
    await adapter.createSchedule({ title: 'x', notes: '', start_unix: 1, end_unix: 61 });
    expect(port.CreateSchedule).toHaveBeenCalledWith(JSON.stringify({ title: 'x', notes: '', start_unix: 1, end_unix: 61 }));
    await adapter.deleteSchedule(page.items[0].id, 3);
    expect(port.DeleteSchedule).toHaveBeenCalledWith(42, JSON.stringify({ expected_revision: 3 }));
    await expect(adapter.deleteSchedule('9223372036854775807', 3)).rejects.toThrow('超出桌面端');
    expect(await adapter.saveCloseBehavior('exit')).toBe('exit');
    expect(port.SaveCloseBehavior).toHaveBeenCalledWith({ behavior: 'exit' });
    expect(await adapter.getStorageDirectories()).toMatchObject({ logs: { effectivePath: 'C:/TimeCue/logs', restartRequired: false } });
    expect(await adapter.chooseDirectory('logs')).toBeNull();
    await expect(adapter.saveStorageDirectory('logs', 'D:/新的日志')).resolves.toMatchObject({ savedPath: 'D:/新的日志', restartRequired: true });
    expect(port.SaveStorageDirectory).toHaveBeenCalledWith({ kind: 'logs', path: 'D:/新的日志' });
    await adapter.frontendReady(4);
    expect(port.FrontendReady).toHaveBeenCalledWith({ last_sequence: 4 });
  });

  it('converts generated activation events and removes listeners', async () => {
    let callback: ((...args: unknown[]) => void) | undefined;
    const eventsOn: EventsOn = vi.fn((_name, listener) => { callback = listener; return () => { callback = undefined; }; });
    const adapter = new WailsAdapter(bindings(), eventsOn); const listener = vi.fn(); const unsubscribe = adapter.subscribeActivation(listener);
    callback?.({ sequence: 4, target: { schedule_id: '42', original_date: '2026-09-16' } });
    expect(listener).toHaveBeenCalledWith({ sequence: 4, targetId: '42', originalDate: '2026-09-16' });
    unsubscribe(); callback?.({ sequence: 5, target: { schedule_id: 43, original_date: '2026-09-17' } });
    expect(listener).toHaveBeenCalledTimes(1);
    adapter.dispose(); expect(callback).toBeUndefined();
  });
});
