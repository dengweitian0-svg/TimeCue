import { describe, expect, it, vi } from 'vitest';
import { toScheduleError } from '../src/contracts';
import { ACTIVATION_EVENT, SCHEDULE_HIDDEN_EVENT, WailsAdapter, type BindingsPort, type EventsOn } from '../src/wails-adapter';

const scheduleDTO = { id: 42, title: '深呼吸', notes: '离开屏幕', first_start_local: '2026-09-16T10:00', duration_minutes: 15, cron: null, until_date_local: null, month_end_policy: null, revision: 3 };
const reminderDTO = { schedule_id: '9223372036854775807', original_date: '2026-09-16', attempted_at_utc: '2026-09-16T02:00:00Z', start_unix: null, end_unix: null, title: null, notes: null, content_status: 'unavailable', status: 'pending', processed_at_utc: null };

/** 创建一组最小绑定实现，测试只验证适配器的转换而不依赖生成文件。 */
function bindings(overrides: Partial<BindingsPort> = {}): BindingsPort {
  return {
    ListSchedules: vi.fn(async () => ({ items: [scheduleDTO], next_cursor: 'next', limit: 20 })),
    GetSchedule: vi.fn(async () => scheduleDTO),
    GetScheduleTimeBounds: vi.fn(async () => ({ min: '2026-09-16T00:00', start_max_exclusive: '2027-09-16T00:00', end_max_inclusive: '2027-09-16T00:00' })),
    ListPendingReminders: vi.fn(async () => ({ items: [reminderDTO], next_cursor: 'reminder-next', limit: 20 })),
    GetReminderDetail: vi.fn(async () => reminderDTO),
    ConfirmReminder: vi.fn(async () => ({ schedule_id: reminderDTO.schedule_id, original_date: reminderDTO.original_date, processed_at_utc: '2026-09-16T03:00:00Z', changed: true })),
    CreateSchedule: vi.fn(async () => scheduleDTO),
    DeleteSchedule: vi.fn(async () => undefined),
    GetRuntimeStatus: vi.fn(async () => ({ visibility: 'visible', lifecycle: 'running', ready: true })),
    GetDesktopStatus: vi.fn(async () => ({ visibility: 'visible', lifecycle: 'running', ready: true, close_behavior: 'tray', activation: null })),
    GetCloseBehavior: vi.fn(async () => ({ behavior: 'tray' })),
    SaveCloseBehavior: vi.fn(async () => undefined),
    GetTheme: vi.fn(async () => ({ theme: 'peach' })),
    SaveTheme: vi.fn(async ({ theme }: { theme: string }) => ({ theme })),
    GetLogLevel: vi.fn(async () => ({ effective_level: 'info', saved_level: 'info', restart_required: false })),
    SaveLogLevel: vi.fn(async () => ({ effective_level: 'info', saved_level: 'debug', restart_required: true })),
    GetStorageDirectories: vi.fn(async () => ({ logs: { kind: 'logs', effective_path: 'C:/TimeCue/logs', saved_path: 'C:/TimeCue/logs', restart_required: false }, backups: { kind: 'backups', effective_path: 'C:/TimeCue/backups', saved_path: 'C:/TimeCue/backups', restart_required: false } })),
    ChooseStorageDirectory: vi.fn(async () => ''),
    SaveStorageDirectory: vi.fn(async ({ kind, path }: { kind: string; path: string }) => ({ kind, effective_path: 'C:/TimeCue/logs', saved_path: path, restart_required: true })),
    RestoreConfigDefault: vi.fn(async () => undefined),
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

  it('replaces unreadable Wails object errors with a stable message', () => {
    for (const value of [{ data: '[object Object]' }, new Error('[object Object]'), {}, { data: 'object' }]) {
      const error = toScheduleError(value);
      expect(error.code).toBe('STORAGE');
      expect(error.message).toBe('操作未完成，请重试。');
    }
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
    await expect(adapter.getTheme()).resolves.toBe('peach');
    await expect(adapter.saveTheme('mint')).resolves.toBe('mint');
    expect(port.SaveTheme).toHaveBeenCalledWith({ theme: 'mint' });
    await expect(adapter.getLogLevel()).resolves.toEqual({ effectiveLevel: 'info', savedLevel: 'info', restartRequired: false });
    await expect(adapter.saveLogLevel('debug')).resolves.toEqual({ effectiveLevel: 'info', savedLevel: 'debug', restartRequired: true });
    expect(port.SaveLogLevel).toHaveBeenCalledWith({ level: 'debug' });
    expect(await adapter.getStorageDirectories()).toMatchObject({ logs: { effectivePath: 'C:/TimeCue/logs', restartRequired: false } });
    expect(await adapter.chooseDirectory('logs')).toBeNull();
    await expect(adapter.saveStorageDirectory('logs', 'D:/新的日志')).resolves.toMatchObject({ savedPath: 'D:/新的日志', restartRequired: true });
    expect(port.SaveStorageDirectory).toHaveBeenCalledWith({ kind: 'logs', path: 'D:/新的日志' });
    await adapter.restoreConfigDefault('log.path');
    expect(port.RestoreConfigDefault).toHaveBeenCalledWith({ property: 'log.path' });
    await adapter.frontendReady(4);
    expect(port.FrontendReady).toHaveBeenCalledWith({ last_sequence: 4 });
  });

  it('maps reminder identity, nullable content, cursor, and confirmation results', async () => {
    const port = bindings(); const adapter = new WailsAdapter(port);
    const page = await adapter.listPendingReminders({ limit: 20, cursor: 'reminder-before' });
    expect(page).toEqual({ items: [{ scheduleId: '9223372036854775807', originalDate: '2026-09-16', attemptedAtUtc: '2026-09-16T02:00:00Z', startUnix: null, endUnix: null, title: null, notes: null, contentStatus: 'unavailable', status: 'pending', processedAtUtc: null }], nextCursor: 'reminder-next', limit: 20 });
    expect(port.ListPendingReminders).toHaveBeenCalledWith({ limit: 20, cursor: 'reminder-before' });

    const detail = await adapter.getReminderDetail({ scheduleId: '42', originalDate: '2026-09-16' });
    expect(detail.scheduleId).toBe('9223372036854775807');
    expect(port.GetReminderDetail).toHaveBeenCalledWith(42, '2026-09-16');
    const confirmation = await adapter.confirmReminder({ scheduleId: '42', originalDate: '2026-09-16' });
    expect(confirmation).toEqual({ scheduleId: '9223372036854775807', originalDate: '2026-09-16', processedAtUtc: '2026-09-16T03:00:00Z', changed: true });
    expect(port.ConfirmReminder).toHaveBeenCalledWith(42, '2026-09-16');
  });

  it('converts generated activation events and removes listeners', async () => {
    const callbacks = new Map<string, (...args: unknown[]) => void>();
    const eventsOn: EventsOn = vi.fn((name, listener) => { callbacks.set(name, listener); return () => { callbacks.delete(name); }; });
    const adapter = new WailsAdapter(bindings(), eventsOn); const listener = vi.fn(); const unsubscribe = adapter.subscribeActivation(listener); const hiddenListener = vi.fn(); const unsubscribeHidden = adapter.subscribeScheduleHidden(hiddenListener);
    callbacks.get(ACTIVATION_EVENT)?.({ sequence: 4, target: { schedule_id: '42', original_date: '2026-09-16' } });
    expect(listener).toHaveBeenCalledWith({ sequence: 4, targetId: '42', originalDate: '2026-09-16' });
    callbacks.get(SCHEDULE_HIDDEN_EVENT)?.({ schedule_id: '9223372036854775807' });
    expect(hiddenListener).toHaveBeenCalledWith({ scheduleId: '9223372036854775807' });
    callbacks.get(SCHEDULE_HIDDEN_EVENT)?.({ schedule_id: 43 });
    expect(hiddenListener).toHaveBeenCalledWith({ scheduleId: '43' });
    unsubscribe(); unsubscribeHidden(); callbacks.get(ACTIVATION_EVENT)?.({ sequence: 5, target: { schedule_id: 43, original_date: '2026-09-17' } }); callbacks.get(SCHEDULE_HIDDEN_EVENT)?.({ schedule_id: 44 });
    expect(listener).toHaveBeenCalledTimes(1);
    expect(hiddenListener).toHaveBeenCalledTimes(2);
    adapter.dispose(); expect(callbacks.size).toBe(0);
  });
});
