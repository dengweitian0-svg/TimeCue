// @vitest-environment happy-dom

import { createRoot } from 'react-dom/client';
import { describe, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { Controller } from '../src/controller';
import type { AppPort, ReminderDetail, ReminderPage, ScheduleBounds, SchedulePage } from '../src/contracts';

const page: SchedulePage = { items: [], nextCursor: '', limit: 20 };
const bounds: ScheduleBounds = {
  min: '2026-09-16T00:00',
  startMaxExclusive: '2027-09-16T00:00',
  endMaxInclusive: '2027-09-16T00:00',
};

/** 为组件交互测试提供稳定的内存端口。 */
function port(overrides: Partial<AppPort> = {}): AppPort {
  const base: AppPort = {
    listSchedules: vi.fn(async () => page),
    getSchedule: vi.fn(),
    getScheduleTimeBounds: vi.fn(async () => bounds),
    listPendingReminders: vi.fn(async () => ({ items: [], nextCursor: '', limit: 20 })),
    getReminderDetail: vi.fn(),
    confirmReminder: vi.fn(),
    createSchedule: vi.fn(),
    deleteSchedule: vi.fn(),
    getRuntimeStatus: vi.fn(async () => ({ visibility: 'visible' as const, lifecycle: 'running' as const, ready: true })),
    getDesktopStatus: vi.fn(async () => ({ runtime: { visibility: 'visible' as const, lifecycle: 'running' as const, ready: true }, closeBehavior: 'tray' as const, activation: null })),
    getCloseBehavior: vi.fn(async () => 'tray' as const),
    getStorageDirectories: vi.fn(async () => ({ logs: { kind: 'logs' as const, effectivePath: 'C:/TimeCue/logs', savedPath: 'C:/TimeCue/logs', restartRequired: false }, backups: { kind: 'backups' as const, effectivePath: 'C:/TimeCue/backups', savedPath: 'C:/TimeCue/backups', restartRequired: false } })),
    chooseDirectory: vi.fn(async () => null),
    saveStorageDirectory: vi.fn(),
    saveCloseBehavior: vi.fn(),
    frontendReady: vi.fn(async () => null),
    retryCleanup: vi.fn(),
    subscribeActivation: vi.fn(() => () => undefined),
  };
  return { ...base, ...overrides };
}

/** 等待 React 完成一次异步渲染提交。 */
async function flushRender(): Promise<void> {
  await new Promise<void>(resolve => setTimeout(resolve, 0));
}

describe('App', () => {
  it('renders the schedule page and opens the create form', async () => {
    const platform = port();
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();

    const addButton = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('添加日程'));
    expect(addButton).toBeDefined();
    addButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();

    expect(host.querySelector('h1')?.textContent).toBe('添加日程');
    expect(host.querySelector('input[placeholder*="例如"]')).not.toBeNull();
    expect(platform.getScheduleTimeBounds).toHaveBeenCalled();

    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('renders independent log and backup directory cards without a database path input', async () => {
    const platform = port();
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    const settingsButton = [...host.querySelectorAll('button')].find(button => button.textContent?.includes('设置'));
    settingsButton?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    expect(host.querySelector('[aria-label="日志目录设置"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="备份目录设置"]')).not.toBeNull();
    expect(host.querySelector('#logs-path')).not.toBeNull();
    expect(host.querySelector('#backups-path')).not.toBeNull();
    expect(host.querySelector('input[id*="database" i]')).toBeNull();
    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('renders the pending reminder empty state without confirming anything', async () => {
    const platform = port();
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('待处理提醒'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    expect(host.querySelector('h1')?.textContent).toBe('待处理提醒');
    expect(host.textContent).toContain('没有待处理提醒');
    expect(platform.confirmReminder).not.toHaveBeenCalled();
    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('shows unavailable reminder content without inventing a body', async () => {
    const unavailable: ReminderDetail = { scheduleId: '7', originalDate: '2026-09-16', attemptedAtUtc: '2026-09-16T02:00:00Z', startUnix: null, endUnix: null, title: null, notes: null, contentStatus: 'unavailable', status: 'pending', processedAtUtc: null };
    const reminders: ReminderPage = { items: [unavailable], nextCursor: '', limit: 20 };
    const platform = port({ listPendingReminders: vi.fn(async () => reminders), getReminderDetail: vi.fn(async () => unavailable) });
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('待处理提醒'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('提醒内容暂不可用'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    expect(host.textContent).toContain('提醒内容暂不可用');
    expect(host.textContent).not.toContain('undefined');
    expect(platform.confirmReminder).not.toHaveBeenCalled();
    root.unmount();
    controller.dispose();
    host.remove();
  });
});
