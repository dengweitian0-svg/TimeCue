import { describe, expect, it, vi } from 'vitest';
import { Controller } from '../src/controller';
import type { Activation, AppPort, ReminderDetail, ReminderPage, SchedulePage } from '../src/contracts';

const page: SchedulePage = { items: [], nextCursor: '', limit: 20 };

/** 为控制器提供不涉及浏览器或平台实现的最小端口。 */
function port(overrides: Partial<AppPort> = {}): AppPort & { emit: (value: Activation) => void } {
  let listener: ((value: Activation) => void) | undefined;
  const base: AppPort & { emit: (value: Activation) => void } = {
    listSchedules: vi.fn(async () => page), getSchedule: vi.fn(), getScheduleTimeBounds: vi.fn(async () => ({ min: '2026-09-16T00:00', startMaxExclusive: '2027-09-16T00:00', endMaxInclusive: '2027-09-16T00:00' })), listPendingReminders: vi.fn(async () => ({ items: [], nextCursor: '', limit: 20 })), getReminderDetail: vi.fn(), confirmReminder: vi.fn(),
    createSchedule: vi.fn(), deleteSchedule: vi.fn(), getRuntimeStatus: vi.fn(async () => ({ visibility: 'visible' as const, lifecycle: 'running' as const, ready: true })), getDesktopStatus: vi.fn(async () => ({ runtime: { visibility: 'visible' as const, lifecycle: 'running' as const, ready: true }, closeBehavior: 'tray' as const, activation: null })), getCloseBehavior: vi.fn(async () => 'tray' as const), saveCloseBehavior: vi.fn(), getStorageDirectories: vi.fn(async () => ({ logs: { kind: 'logs' as const, effectivePath: 'C:/TimeCue/logs', savedPath: 'C:/TimeCue/logs', restartRequired: false }, backups: { kind: 'backups' as const, effectivePath: 'C:/TimeCue/backups', savedPath: 'C:/TimeCue/backups', restartRequired: false } })), chooseDirectory: vi.fn(async () => null), saveStorageDirectory: vi.fn(), frontendReady: vi.fn(async () => null), retryCleanup: vi.fn(), subscribeActivation: vi.fn(value => { listener = value; return () => { listener = undefined; }; }), emit: value => listener?.(value),
  };
  return { ...base, ...overrides, emit: base.emit };
}

/** 创建可控的提醒详情，测试只关心稳定身份和正文可用性。 */
function detail(scheduleId: string, title: string | null = titleFor(scheduleId)): ReminderDetail { return { scheduleId, originalDate: '2026-09-16', attemptedAtUtc: '2026-09-16T02:00:00Z', startUnix: title ? 1799964000 : null, endUnix: title ? 1799964900 : null, title, notes: title ? 'notes' : null, contentStatus: title ? 'available' : 'unavailable', status: 'pending', processedAtUtc: null }; }

/** 为默认提醒测试生成稳定标题。 */
function titleFor(scheduleId: string): string { return `提醒 ${scheduleId}`; }

/** 创建 Promise 的外部完成句柄，用于验证迟到响应保护。 */
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } { let resolve!: (value: T) => void; const promise = new Promise<T>(value => { resolve = value; }); return { promise, resolve }; }

describe('Controller activation handshake', () => {
  it('deduplicates old and repeated sequence numbers', async () => {
    const platform = port(); const controller = new Controller(platform);
    await controller.initialize(); platform.emit({ sequence: 2, targetId: '7', originalDate: '2026-09-16' }); platform.emit({ sequence: 2, targetId: '8', originalDate: '2026-09-17' }); platform.emit({ sequence: 1, targetId: '9', originalDate: '2026-09-18' });
    expect(controller.snapshot().activation).toEqual({ sequence: 2, targetId: '7', originalDate: '2026-09-16' });
    expect(platform.frontendReady).toHaveBeenCalledWith(0);
    controller.dispose();
  });

  it('switches to reminders, loads stable detail, and ignores a late prior activation', async () => {
    const first = deferred<ReminderDetail>(); const second = deferred<ReminderDetail>();
    const platform = port({ getReminderDetail: vi.fn(async identity => identity.scheduleId === '7' ? first.promise : second.promise) });
    const controller = new Controller(platform);
    await controller.initialize();
    platform.emit({ sequence: 3, targetId: '7', originalDate: '2026-09-16' });
    platform.emit({ sequence: 4, targetId: '8', originalDate: '2026-09-16' });
    expect(controller.snapshot().page).toBe('reminders');
    expect(controller.snapshot().activeReminder).toEqual({ scheduleId: '8', originalDate: '2026-09-16' });
    first.resolve(detail('7')); await new Promise(resolve => setTimeout(resolve, 0));
    expect(controller.snapshot().reminderDetail).toBeNull();
    second.resolve(detail('8')); await new Promise(resolve => setTimeout(resolve, 0));
    expect(controller.snapshot().reminderDetail?.scheduleId).toBe('8');
    controller.dispose();
  });

  it('returns to the first page after a stale cursor', async () => {
    const firstPage: ReminderPage = { items: [detail('1')], nextCursor: 'cursor-1', limit: 1 };
    const refreshed: ReminderPage = { items: [detail('2')], nextCursor: '', limit: 1 };
    let calls = 0;
    const listPendingReminders = vi.fn(async (request: { limit: number; cursor?: string }) => {
      calls++;
      if (calls === 1) return firstPage;
      if (request.cursor) throw { data: JSON.stringify({ code: 'STALE_CURSOR', message: 'stale' }) };
      return refreshed;
    });
    const platform = port({ listPendingReminders }); const controller = new Controller(platform);
    await controller.initialize(); await controller.nextReminderPage();
    expect(calls).toBe(3);
    expect(controller.snapshot().reminders.items[0].scheduleId).toBe('2');
    controller.dispose();
  });
});

describe('Controller reminder grouping', () => {
  it('groups only reminders with the same reconstructed start time', async () => {
    const platform = port(); const controller = new Controller(platform);
    await controller.initialize();
    controller.selectReminder({ scheduleId: '1', originalDate: '2026-09-16' });
    expect(controller.snapshot().activeReminder).toEqual({ scheduleId: '1', originalDate: '2026-09-16' });
    controller.dispose();
  });
});
