import { describe, expect, it, vi } from 'vitest';
import { Controller } from '../src/controller';
import type { Activation, AppPort, SchedulePage } from '../src/contracts';

const page: SchedulePage = { items: [], nextCursor: '', limit: 20 };

/** 为控制器提供不涉及浏览器或平台实现的最小端口。 */
function port(): AppPort & { emit: (value: Activation) => void } {
  let listener: ((value: Activation) => void) | undefined;
  return {
    listSchedules: vi.fn(async () => page), getSchedule: vi.fn(), getScheduleTimeBounds: vi.fn(async () => ({ min: '2026-09-16T00:00', startMaxExclusive: '2027-09-16T00:00', endMaxInclusive: '2027-09-16T00:00' })),
    createSchedule: vi.fn(), deleteSchedule: vi.fn(), getRuntimeStatus: vi.fn(async () => ({ visibility: 'visible' as const, lifecycle: 'running' as const, ready: true })), getDesktopStatus: vi.fn(async () => ({ runtime: { visibility: 'visible' as const, lifecycle: 'running' as const, ready: true }, closeBehavior: 'tray' as const, activation: null })), getCloseBehavior: vi.fn(async () => 'tray' as const), saveCloseBehavior: vi.fn(), frontendReady: vi.fn(async () => null), retryCleanup: vi.fn(), subscribeActivation: vi.fn(value => { listener = value; return () => { listener = undefined; }; }), emit: value => listener?.(value),
  };
}

describe('Controller activation handshake', () => {
  it('deduplicates old and repeated sequence numbers', async () => {
    const platform = port(); const controller = new Controller(platform);
    await controller.initialize(); platform.emit({ sequence: 2, targetId: '7', originalDate: '2026-09-16' }); platform.emit({ sequence: 2, targetId: '8', originalDate: '2026-09-17' }); platform.emit({ sequence: 1, targetId: '9', originalDate: '2026-09-18' });
    expect(controller.snapshot().activation).toEqual({ sequence: 2, targetId: '7', originalDate: '2026-09-16' });
    expect(platform.frontendReady).toHaveBeenCalledWith(0);
    controller.dispose();
  });
});
