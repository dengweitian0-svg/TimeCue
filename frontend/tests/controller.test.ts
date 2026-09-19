import { describe, expect, it, vi } from 'vitest';
import { Controller } from '../src/controller';
import type { Activation, AppPort, RecoveryEvent, ReminderDetail, ReminderPage, ScheduleHidden, SchedulePage } from '../src/contracts';
import { themes } from '../src/themes';

const page: SchedulePage = { items: [], nextCursor: '', limit: 20 };

/** 为控制器提供不涉及浏览器或平台实现的最小端口。 */
function port(
  overrides: Partial<AppPort> = {},
): AppPort & {
  emit: (value: Activation) => void;
  emitRecovery: (value: RecoveryEvent) => void;
  emitScheduleHidden: (value: ScheduleHidden) => void;
  emitPendingRemindersChanged: () => void;
} {
  let listener: ((value: Activation) => void) | undefined;
  let recoveryListener: ((value: RecoveryEvent) => void) | undefined;
  let scheduleHiddenListener: ((value: ScheduleHidden) => void) | undefined;
  let pendingRemindersChangedListener: (() => void) | undefined;
  const base: AppPort & {
    emit: (value: Activation) => void;
    emitRecovery: (value: RecoveryEvent) => void;
    emitScheduleHidden: (value: ScheduleHidden) => void;
    emitPendingRemindersChanged: () => void;
  } = {
    listSchedules: vi.fn(async () => page),
    getSchedule: vi.fn(),
    getScheduleTimeBounds: vi.fn(async () => ({
      min: '2026-09-16T00:00',
      startMaxExclusive: '2027-09-16T00:00',
      endMaxInclusive: '2027-09-16T00:00',
    })),
    listPendingReminders: vi.fn(async () => ({
      items: [],
      nextCursor: '',
      limit: 20,
    })),
    getReminderDetail: vi.fn(),
    confirmReminder: vi.fn(),
    createSchedule: vi.fn(),
    deleteSchedule: vi.fn(),
    cancelSchedule: vi.fn(),
    restoreSchedule: vi.fn(),
    getRuntimeStatus: vi.fn(async () => ({
      visibility: 'visible' as const,
      lifecycle: 'running' as const,
      ready: true,
      recoveryStatus: 'idle' as const,
    })),
    getDesktopStatus: vi.fn(async () => ({
      runtime: {
        visibility: 'visible' as const,
        lifecycle: 'running' as const,
        ready: true,
        recoveryStatus: 'idle' as const,
      },
      closeBehavior: 'tray' as const,
      activation: null,
    })),
    getCloseBehavior: vi.fn(async () => 'tray' as const),
    saveCloseBehavior: vi.fn(),
    getTheme: vi.fn(async () => 'peach' as const),
    saveTheme: vi.fn(
      async (value: 'peach' | 'mint' | 'sky' | 'lavender') => value,
    ),
    applyWindowTheme: vi.fn(async () => undefined),
    getLogLevel: vi.fn(async () => ({
      effectiveLevel: 'info' as const,
      savedLevel: 'info' as const,
      restartRequired: false,
    })),
    getStorageDirectories: vi.fn(async () => ({
      logs: {
        kind: 'logs' as const,
        effectivePath: 'C:/TimeCue/logs',
        savedPath: 'C:/TimeCue/logs',
        restartRequired: false,
      },
      backups: {
        kind: 'backups' as const,
        effectivePath: 'C:/TimeCue/backups',
        savedPath: 'C:/TimeCue/backups',
        restartRequired: false,
      },
    })),
    chooseDirectory: vi.fn(async () => null),
    saveStorageDirectory: vi.fn(),
    saveLogLevel: vi.fn(async () => ({
      effectiveLevel: 'info' as const,
      savedLevel: 'info' as const,
      restartRequired: false,
    })),
    restoreConfigDefault: vi.fn(async () => undefined),
    frontendReady: vi.fn(async () => null),
    retryCleanup: vi.fn(),
    subscribeActivation: vi.fn((value) => {
      listener = value;
      return () => {
        listener = undefined;
      };
    }),
    subscribeScheduleHidden: vi.fn((value) => {
      scheduleHiddenListener = value;
      return () => {
        scheduleHiddenListener = undefined;
      };
    }),
    subscribePendingRemindersChanged: vi.fn((value) => {
      pendingRemindersChangedListener = value;
      return () => {
        pendingRemindersChangedListener = undefined;
      };
    }),
    subscribeRecovery: vi.fn((value) => {
      recoveryListener = value;
      return () => {
        recoveryListener = undefined;
      };
    }),
    emit: (value) => listener?.(value),
    emitScheduleHidden: (value) => scheduleHiddenListener?.(value),
    emitPendingRemindersChanged: () => pendingRemindersChangedListener?.(),
    emitRecovery: (value) => recoveryListener?.(value),
  };
  return { ...base, ...overrides, emit: base.emit };
}

/** 创建可控的提醒详情，测试只关心稳定身份和正文可用性。 */
function detail(
  scheduleId: string,
  title: string | null = titleFor(scheduleId),
): ReminderDetail {
  return {
    scheduleId,
    originalDate: '2026-09-16',
    attemptedAtUtc: '2026-09-16T02:00:00Z',
    startUnix: title ? 1799964000 : null,
    endUnix: title ? 1799964900 : null,
    title,
    notes: title ? 'notes' : null,
    contentStatus: title ? 'available' : 'unavailable',
    status: 'pending',
    processedAtUtc: null,
  };
}

/** 为默认提醒测试生成稳定标题。 */
function titleFor(scheduleId: string): string {
  return `提醒 ${scheduleId}`;
}

/** 创建 Promise 的外部完成句柄，用于验证迟到响应保护。 */
function deferred<T>(): { promise: Promise<T>; resolve: (value: T) => void } {
  let resolve!: (value: T) => void;
  const promise = new Promise<T>((value) => {
    resolve = value;
  });
  return { promise, resolve };
}

describe('Controller activation handshake', () => {
  it('deduplicates old and repeated sequence numbers', async () => {
    const platform = port();
    const controller = new Controller(platform);
    await controller.initialize();
    platform.emit({ sequence: 2, targetId: '7', originalDate: '2026-09-16' });
    platform.emit({ sequence: 2, targetId: '8', originalDate: '2026-09-17' });
    platform.emit({ sequence: 1, targetId: '9', originalDate: '2026-09-18' });
    expect(controller.snapshot().activation).toEqual({
      sequence: 2,
      targetId: '7',
      originalDate: '2026-09-16',
    });
    expect(platform.frontendReady).toHaveBeenCalledWith(0);
    controller.dispose();
  });

  it('switches to reminders, loads stable detail, and ignores a late prior activation', async () => {
    const first = deferred<ReminderDetail>();
    const second = deferred<ReminderDetail>();
    const platform = port({
      getReminderDetail: vi.fn(async (identity) =>
        identity.scheduleId === '7' ? first.promise : second.promise,
      ),
    });
    const controller = new Controller(platform);
    await controller.initialize();
    platform.emit({ sequence: 3, targetId: '7', originalDate: '2026-09-16' });
    platform.emit({ sequence: 4, targetId: '8', originalDate: '2026-09-16' });
    expect(controller.snapshot().page).toBe('reminders');
    expect(controller.snapshot().activeReminder).toEqual({
      scheduleId: '8',
      originalDate: '2026-09-16',
    });
    first.resolve(detail('7'));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(controller.snapshot().reminderDetail).toBeNull();
    second.resolve(detail('8'));
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(controller.snapshot().reminderDetail?.scheduleId).toBe('8');
    controller.dispose();
  });

  it('returns to the first page after a stale cursor', async () => {
    const firstPage: ReminderPage = {
      items: [detail('1')],
      nextCursor: 'cursor-1',
      limit: 1,
    };
    const refreshed: ReminderPage = {
      items: [detail('2')],
      nextCursor: '',
      limit: 1,
    };
    let calls = 0;
    const listPendingReminders = vi.fn(
      async (request: { limit: number; cursor?: string }) => {
        calls++;
        if (calls === 1) return firstPage;
        if (request.cursor)
          throw {
            data: JSON.stringify({ code: 'STALE_CURSOR', message: 'stale' }),
          };
        return refreshed;
      },
    );
    const platform = port({ listPendingReminders });
    const controller = new Controller(platform);
    await controller.initialize();
    await controller.nextReminderPage();
    expect(calls).toBe(3);
    expect(controller.snapshot().reminders.items[0].scheduleId).toBe('2');
    controller.dispose();
  });
});

describe('Controller reminder navigation', () => {
  it('refreshes reminders once when entering the page and again after leaving it', async () => {
    const refreshed: ReminderPage = {
      items: [detail('7')],
      nextCursor: '',
      limit: 20,
    };
    let calls = 0;
    const listPendingReminders = vi.fn(async () => {
      calls++;
      return calls === 1 ? { items: [], nextCursor: '', limit: 20 } : refreshed;
    });
    const platform = port({ listPendingReminders });
    const controller = new Controller(platform);
    await controller.initialize();

    controller.setPage('reminders');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(listPendingReminders).toHaveBeenCalledTimes(2);
    expect(
      controller.snapshot().reminders.items.map((item) => item.scheduleId),
    ).toEqual(['7']);

    controller.setPage('reminders');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(listPendingReminders).toHaveBeenCalledTimes(2);

    controller.setPage('schedules');
    controller.setPage('reminders');
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(listPendingReminders).toHaveBeenCalledTimes(3);
    controller.dispose();
  });
});

describe('Controller schedule hidden events', () => {
  const schedule = (id: string): import('../src/contracts').Schedule => ({
    id,
    title: `日程 ${id}`,
    notes: '',
    firstStartLocal: '2026-09-16T10:00:00+08:00',
    durationMinutes: 30,
    cron: null,
    untilDateLocal: null,
    monthEndPolicy: null,
    open: true,
    revision: 1,
  });

  it('removes only the matching schedule without navigating or refreshing other pages', async () => {
    const platform = port({
      listSchedules: vi.fn(async () => ({
        items: [schedule('1'), schedule('2')],
        nextCursor: 'next',
        limit: 2,
      })),
    });
    const controller = new Controller(platform);
    await controller.initialize();
    controller.setPage('reminders');
    platform.emitScheduleHidden({ scheduleId: '1' });
    expect(controller.snapshot().page).toBe('reminders');
    expect(
      controller.snapshot().schedules.items.map((item) => item.id),
    ).toEqual(['2']);
    expect(platform.listSchedules).toHaveBeenCalledTimes(1);
    controller.dispose();
  });

  it('ignores late refresh and next-page responses after a hidden event', async () => {
    const refresh = deferred<SchedulePage>();
    const next = deferred<SchedulePage>();
    let calls = 0;
    const platform = port({
      listSchedules: vi.fn((request?: { limit: number; cursor?: string }) => {
        calls++;
        if (calls === 1)
          return Promise.resolve({
            items: [schedule('1')],
            nextCursor: 'cursor',
            limit: 1,
          });
        if (!request?.cursor) return refresh.promise;
        return next.promise;
      }),
    });
    const controller = new Controller(platform);
    await controller.initialize();
    const refreshing = controller.refreshSchedules();
    platform.emitScheduleHidden({ scheduleId: '1' });
    refresh.resolve({
      items: [schedule('1'), schedule('2')],
      nextCursor: '',
      limit: 1,
    });
    await refreshing;
    expect(controller.snapshot().schedules.items).toEqual([]);

    const paging = controller.nextPage();
    platform.emitScheduleHidden({ scheduleId: '2' });
    next.resolve({ items: [schedule('2')], nextCursor: '', limit: 1 });
    await paging;
    expect(controller.snapshot().schedules.items).toEqual([]);
    controller.dispose();
  });
});

describe('Controller recurring schedule actions', () => {
  const recurring = (
    open: boolean,
    revision = 1,
  ): import('../src/contracts').Schedule => ({
    id: '7',
    title: '晨间计划',
    notes: '',
    firstStartLocal: '2026-09-16T09:00:00+08:00',
    durationMinutes: 30,
    cron: '0 9 * * *',
    untilDateLocal: null,
    monthEndPolicy: null,
    open,
    revision,
  });

  it('cancels and restores a recurring schedule without deleting its card', async () => {
    const original = recurring(true);
    const cancelSchedule = vi.fn(async () => ({
      scheduleId: '7',
      open: false,
      revision: 2,
      updatedAtUtc: '2026-09-16T03:00:00Z',
    }));
    const restoreSchedule = vi.fn(async () => ({
      scheduleId: '7',
      open: true,
      revision: 3,
      updatedAtUtc: '2026-09-16T04:00:00Z',
    }));
    const platform = port({
      listSchedules: vi.fn(async () => ({
        items: [original],
        nextCursor: '',
        limit: 20,
      })),
      cancelSchedule,
      restoreSchedule,
    });
    const controller = new Controller(platform);
    await controller.initialize();

    controller.askScheduleAction('cancel', original);
    await controller.confirmScheduleAction();
    expect(cancelSchedule).toHaveBeenCalledWith('7', 1);
    expect(controller.snapshot().schedules.items).toEqual([
      { ...original, open: false, revision: 2 },
    ]);
    expect(controller.snapshot().scheduleAction).toBeNull();
    expect(platform.deleteSchedule).not.toHaveBeenCalled();

    const cancelled = controller.snapshot().schedules.items[0];
    controller.askScheduleAction('restore', cancelled);
    await controller.confirmScheduleAction();
    expect(restoreSchedule).toHaveBeenCalledWith('7', 2);
    expect(controller.snapshot().schedules.items).toEqual([
      { ...original, open: true, revision: 3 },
    ]);
    expect(controller.snapshot().notice).toContain('已恢复');
    controller.dispose();
  });

  it('reloads the latest schedule after a stale action without retrying automatically', async () => {
    const original = recurring(true);
    const latest = recurring(true, 2);
    const cancelSchedule = vi.fn(async () => {
      throw {
        data: JSON.stringify({ code: 'STALE_REVISION', message: '版本已变化' }),
      };
    });
    const platform = port({
      listSchedules: vi.fn(async () => ({
        items: [latest],
        nextCursor: '',
        limit: 20,
      })),
      getSchedule: vi.fn(async () => latest),
      cancelSchedule,
    });
    const controller = new Controller(platform);
    await controller.initialize();

    controller.askScheduleAction('cancel', original);
    await controller.confirmScheduleAction();
    expect(cancelSchedule).toHaveBeenCalledTimes(1);
    expect(controller.snapshot().error?.code).toBe('STALE_REVISION');
    await controller.recoverScheduleAction();
    expect(controller.snapshot().scheduleAction).toEqual({
      kind: 'cancel',
      item: latest,
    });
    expect(controller.snapshot().notice).toContain('最新内容');
    expect(cancelSchedule).toHaveBeenCalledTimes(1);
    controller.dispose();
  });

  it('closes the action after the schedule disappeared', async () => {
    const original = recurring(true);
    const platform = port({
      cancelSchedule: vi.fn(async () => {
        throw {
          data: JSON.stringify({ code: 'NOT_FOUND', message: '日程不存在' }),
        };
      }),
      getSchedule: vi.fn(async () => {
        throw {
          data: JSON.stringify({ code: 'NOT_FOUND', message: '日程不存在' }),
        };
      }),
      listSchedules: vi.fn(async () => ({
        items: [],
        nextCursor: '',
        limit: 20,
      })),
    });
    const controller = new Controller(platform);
    await controller.initialize();

    controller.askScheduleAction('cancel', original);
    await controller.confirmScheduleAction();
    await controller.recoverScheduleAction();
    expect(controller.snapshot().scheduleAction).toBeNull();
    expect(controller.snapshot().notice).toContain('不存在');
    controller.dispose();
  });
});

describe('Controller recovery events', () => {
  it('refreshes pending reminders after recovery completion and keeps the running state visible', async () => {
    const platform = port();
    const controller = new Controller(platform);
    await controller.initialize();
    const callsBefore = (
      platform.listPendingReminders as ReturnType<typeof vi.fn>
    ).mock.calls.length;
    platform.emitRecovery({ status: 'running' });
    expect(controller.snapshot().runtime?.recoveryStatus).toBe('running');
    platform.emitRecovery({ status: 'idle' });
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(platform.listPendingReminders).toHaveBeenCalledTimes(
      callsBefore + 1,
    );
    expect(controller.snapshot().runtime?.recoveryStatus).toBe('idle');
    controller.dispose();
  });
});

describe('Controller pending reminder change events', () => {
  it('refreshes in the background and preserves the current page and selected detail', async () => {
    const current = detail('1');
    const next = detail('2');
    let calls = 0;
    const listPendingReminders = vi.fn(async () => {
      calls++;
      return calls <= 2
        ? { items: [current], nextCursor: '', limit: 20 }
        : { items: [current, next], nextCursor: '', limit: 20 };
    });
    const platform = port({
      listPendingReminders,
      getReminderDetail: vi.fn(async () => current),
    });
    const controller = new Controller(platform);
    await controller.initialize();
    controller.setPage('reminders');
    await new Promise((resolve) => setTimeout(resolve, 0));
    controller.selectReminder(current);
    await new Promise((resolve) => setTimeout(resolve, 0));
    const callsBeforeEvent = calls;

    platform.emitPendingRemindersChanged();
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(calls).toBe(callsBeforeEvent + 1);
    expect(controller.snapshot().page).toBe('reminders');
    expect(
      controller.snapshot().reminders.items.map((item) => item.scheduleId),
    ).toEqual(['1', '2']);
    expect(controller.snapshot().activeReminder).toEqual({
      scheduleId: '1',
      originalDate: current.originalDate,
    });
    expect(controller.snapshot().reminderDetail?.scheduleId).toBe('1');
    controller.dispose();
  });

  it('refreshes while another page is visible and ignores an older response', async () => {
    const stale = deferred<ReminderPage>();
    const latest = detail('9');
    let calls = 0;
    const listPendingReminders = vi.fn(async () => {
      calls++;
      if (calls === 1) return { items: [], nextCursor: '', limit: 20 };
      if (calls === 2) return stale.promise;
      return { items: [latest], nextCursor: '', limit: 20 };
    });
    const platform = port({ listPendingReminders });
    const controller = new Controller(platform);
    await controller.initialize();

    platform.emitPendingRemindersChanged();
    platform.emitPendingRemindersChanged();
    await new Promise((resolve) => setTimeout(resolve, 0));
    stale.resolve({ items: [detail('stale')], nextCursor: '', limit: 20 });
    await new Promise((resolve) => setTimeout(resolve, 0));

    expect(controller.snapshot().page).toBe('schedules');
    expect(
      controller.snapshot().reminders.items.map((item) => item.scheduleId),
    ).toEqual(['9']);
    expect(calls).toBe(3);
    controller.dispose();
  });
});

describe('Controller configuration settings', () => {
  it('saves log level and refreshes all settings after restoring a default', async () => {
    const platform = port();
    const controller = new Controller(platform);
    await controller.initialize();
    await controller.saveLogLevel('debug');
    expect(platform.saveLogLevel).toHaveBeenCalledWith('debug');
    await controller.restoreConfigDefault('log.level');
    expect(platform.restoreConfigDefault).toHaveBeenCalledWith('log.level');
    expect(platform.getLogLevel).toHaveBeenCalledTimes(2);
    controller.dispose();
  });

  it('reads the theme during initialization, saves on change, and keeps the old value on failure', async () => {
    const saveTheme = vi.fn(
      async (value: 'peach' | 'mint' | 'sky' | 'lavender') => {
        if (value === 'sky')
          throw {
            data: JSON.stringify({ code: 'STORAGE', message: '主题保存失败' }),
          };
        return value;
      },
    );
    const platform = port({
      getTheme: vi.fn(async () => 'peach' as const),
      saveTheme,
    });
    const controller = new Controller(platform);
    await controller.initialize();
    expect(controller.snapshot().theme).toBe('peach');
    expect(controller.snapshot().themeLoading).toBe(false);
    expect(platform.applyWindowTheme).toHaveBeenCalledWith({
      background: themes.peach.bg,
      text: themes.peach.text,
    });
    await controller.saveTheme('mint');
    expect(controller.snapshot().theme).toBe('mint');
    expect(saveTheme).toHaveBeenCalledTimes(1);
    expect(platform.applyWindowTheme).toHaveBeenLastCalledWith({
      background: themes.mint.bg,
      text: themes.mint.text,
    });
    await controller.saveTheme('mint');
    expect(saveTheme).toHaveBeenCalledTimes(1);
    await controller.saveTheme('sky');
    expect(controller.snapshot().theme).toBe('mint');
    expect(controller.snapshot().error?.code).toBe('STORAGE');
    controller.dispose();
  });

  it('keeps the saved theme and shows a non-blocking notice when native sync fails', async () => {
    const applyWindowTheme = vi.fn(async () => {
      throw new Error('DWM unavailable');
    });
    const platform = port({ applyWindowTheme });
    const controller = new Controller(platform);
    await controller.initialize();
    await controller.saveTheme('mint');
    expect(controller.snapshot().theme).toBe('mint');
    expect(controller.snapshot().notice).toBe(
      '主题已应用，但窗口标题栏暂未同步。',
    );
    expect(applyWindowTheme).toHaveBeenCalledTimes(2);
    controller.dispose();
  });

  it('falls back to peach and keeps the initialization error when theme loading fails', async () => {
    const platform = port({
      getTheme: vi.fn(async () => {
        throw {
          data: JSON.stringify({ code: 'STORAGE', message: '读取主题失败' }),
        };
      }),
    });
    const controller = new Controller(platform);
    await controller.initialize();
    expect(controller.snapshot().theme).toBe('peach');
    expect(controller.snapshot().themeLoading).toBe(false);
    expect(controller.snapshot().error?.message).toBe('读取主题失败');
    controller.dispose();
  });

  it('keeps a readable error when a Wails operation rejects an object', async () => {
    const platform = port({
      saveTheme: vi.fn(async () => {
        throw { data: '[object Object]' };
      }),
    });
    const controller = new Controller(platform);
    await controller.initialize();
    await controller.saveTheme('mint');
    expect(controller.snapshot().error?.message).toBe('操作未完成，请重试。');
    controller.dispose();
  });

  it('refreshes the theme when restoring its default', async () => {
    const getTheme = vi.fn<
      () => Promise<'peach' | 'mint' | 'sky' | 'lavender'>
    >(async () => 'mint');
    const platform = port({ getTheme });
    const controller = new Controller(platform);
    await controller.initialize();
    await controller.restoreConfigDefault('theme');
    expect(platform.restoreConfigDefault).toHaveBeenCalledWith('theme');
    expect(getTheme).toHaveBeenCalledTimes(2);
    expect(controller.snapshot().theme).toBe('mint');
    expect(platform.applyWindowTheme).toHaveBeenCalledTimes(2);
    controller.dispose();
  });
});

describe('Controller reminder grouping', () => {
  it('groups only reminders with the same reconstructed start time', async () => {
    const platform = port();
    const controller = new Controller(platform);
    await controller.initialize();
    controller.selectReminder({ scheduleId: '1', originalDate: '2026-09-16' });
    expect(controller.snapshot().activeReminder).toEqual({
      scheduleId: '1',
      originalDate: '2026-09-16',
    });
    controller.dispose();
  });
});

describe('Controller reminder confirmation', () => {
  it('refreshes after success and prefers the next reminder at the same time', async () => {
    const first = detail('1');
    const next = detail('2');
    const later = {
      ...detail('3'),
      startUnix: 1799967600,
      endUnix: 1799968500,
    };
    let calls = 0;
    const listPendingReminders = vi.fn(async () => {
      calls++;
      return calls === 1
        ? { items: [first, next, later], nextCursor: '', limit: 20 }
        : { items: [next, later], nextCursor: '', limit: 20 };
    });
    const confirmReminder = vi.fn(async () => ({
      scheduleId: '1',
      originalDate: first.originalDate,
      processedAtUtc: '2026-09-16T03:00:00Z',
      changed: true,
    }));
    const platform = port({
      listPendingReminders,
      getReminderDetail: vi.fn(async (identity) =>
        identity.scheduleId === '2' ? next : first,
      ),
      confirmReminder,
    });
    const controller = new Controller(platform);
    await controller.initialize();
    controller.selectReminder(first);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await controller.confirmReminder();
    expect(confirmReminder).toHaveBeenCalledWith({
      scheduleId: '1',
      originalDate: first.originalDate,
    });
    expect(controller.snapshot().activeReminder).toEqual({
      scheduleId: '2',
      originalDate: next.originalDate,
    });
    expect(
      controller.snapshot().reminders.items.map((item) => item.scheduleId),
    ).toEqual(['2', '3']);
    expect(controller.snapshot().reminderConfirming).toBe(false);
    controller.dispose();
  });

  it('keeps detail after failure and allows retry', async () => {
    const current = detail('1');
    let attempts = 0;
    const confirmReminder = vi.fn(async () => {
      attempts++;
      if (attempts === 1)
        throw { data: JSON.stringify({ code: 'STORAGE', message: 'failed' }) };
      return {
        scheduleId: '1',
        originalDate: current.originalDate,
        processedAtUtc: '2026-09-16T03:00:00Z',
        changed: true,
      };
    });
    const platform = port({
      listPendingReminders: vi.fn(async () => ({
        items: [current],
        nextCursor: '',
        limit: 20,
      })),
      getReminderDetail: vi.fn(async () => current),
      confirmReminder,
    });
    const controller = new Controller(platform);
    await controller.initialize();
    controller.selectReminder(current);
    await new Promise((resolve) => setTimeout(resolve, 0));
    await controller.confirmReminder();
    expect(controller.snapshot().reminderDetail?.title).toBe(current.title);
    expect(controller.snapshot().reminderError?.code).toBe('STORAGE');
    await controller.confirmReminder();
    expect(confirmReminder).toHaveBeenCalledTimes(2);
    controller.dispose();
  });

  it('lets a new activation win over an in-flight confirmation', async () => {
    const current = detail('1');
    const activated = detail('2');
    const pending = deferred<{
      scheduleId: string;
      originalDate: string;
      processedAtUtc: string;
      changed: boolean;
    }>();
    const platform = port({
      listPendingReminders: vi.fn(async () => ({
        items: [current, activated],
        nextCursor: '',
        limit: 20,
      })),
      getReminderDetail: vi.fn(async (identity) =>
        identity.scheduleId === '2' ? activated : current,
      ),
      confirmReminder: vi.fn(() => pending.promise),
    });
    const controller = new Controller(platform);
    await controller.initialize();
    controller.selectReminder(current);
    await new Promise((resolve) => setTimeout(resolve, 0));
    const confirmation = controller.confirmReminder();
    platform.emit({
      sequence: 9,
      targetId: '2',
      originalDate: activated.originalDate,
    });
    pending.resolve({
      scheduleId: '1',
      originalDate: current.originalDate,
      processedAtUtc: '2026-09-16T03:00:00Z',
      changed: true,
    });
    await confirmation;
    await new Promise((resolve) => setTimeout(resolve, 0));
    expect(controller.snapshot().activeReminder?.scheduleId).toBe('2');
    expect(controller.snapshot().reminderConfirming).toBe(false);
    controller.dispose();
  });
});
