// @vitest-environment happy-dom

import { createRoot } from 'react-dom/client';
import { describe, expect, it, vi } from 'vitest';
import { App } from '../src/App';
import { Controller } from '../src/controller';
import { themes } from '../src/themes';
import type { AppPort, ReminderDetail, ReminderPage, Schedule, ScheduleBounds, SchedulePage, Theme } from '../src/contracts';

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
    cancelSchedule: vi.fn(),
    restoreSchedule: vi.fn(),
    getRuntimeStatus: vi.fn(async () => ({ visibility: 'visible' as const, lifecycle: 'running' as const, ready: true })),
    getDesktopStatus: vi.fn(async () => ({ runtime: { visibility: 'visible' as const, lifecycle: 'running' as const, ready: true }, closeBehavior: 'tray' as const, activation: null })),
    getCloseBehavior: vi.fn(async () => 'tray' as const),
    getTheme: vi.fn(async () => 'peach' as const),
    saveTheme: vi.fn(async (value: 'peach' | 'mint' | 'sky' | 'lavender') => value), applyWindowTheme: vi.fn(async () => undefined),
    getLogLevel: vi.fn(async () => ({ effectiveLevel: 'info' as const, savedLevel: 'info' as const, restartRequired: false })),
    getStorageDirectories: vi.fn(async () => ({ logs: { kind: 'logs' as const, effectivePath: 'C:/TimeCue/logs', savedPath: 'C:/TimeCue/logs', restartRequired: false }, backups: { kind: 'backups' as const, effectivePath: 'C:/TimeCue/backups', savedPath: 'C:/TimeCue/backups', restartRequired: false } })),
    chooseDirectory: vi.fn(async () => null),
    restoreConfigDefault: vi.fn(async () => undefined),
    saveLogLevel: vi.fn(async () => ({ effectiveLevel: 'info' as const, savedLevel: 'info' as const, restartRequired: false })),
    saveStorageDirectory: vi.fn(),
    saveCloseBehavior: vi.fn(),
    frontendReady: vi.fn(async () => null),
    retryCleanup: vi.fn(),
    subscribeActivation: vi.fn(() => () => undefined),
    subscribeScheduleHidden: vi.fn(() => () => undefined),
    subscribePendingRemindersChanged: vi.fn(() => () => undefined),
  };
  return { ...base, ...overrides };
}

/** 等待 React 完成一次异步渲染提交。 */
async function flushRender(): Promise<void> {
  await new Promise<void>(resolve => setTimeout(resolve, 0));
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

  it('shows cancel and restore controls only for recurring schedules', async () => {
    const recurring: Schedule = { id: '7', title: '晨间计划', notes: '', firstStartLocal: '2026-09-16T09:00:00+08:00', durationMinutes: 30, cron: '0 9 * * *', untilDateLocal: null, monthEndPolicy: null, open: true, revision: 1 };
    const single: Schedule = { id: '8', title: '单次计划', notes: '', firstStartLocal: '2026-09-16T10:00:00+08:00', durationMinutes: 30, cron: null, untilDateLocal: null, monthEndPolicy: null, open: true, revision: 1 };
    const cancelled: Schedule = { ...recurring, open: false, revision: 2 };
    const cancelSchedule = vi.fn(async () => ({ scheduleId: '7', open: false, revision: 2, updatedAtUtc: '2026-09-16T03:00:00Z' }));
    const restoreSchedule = vi.fn(async () => ({ scheduleId: '7', open: true, revision: 3, updatedAtUtc: '2026-09-16T04:00:00Z' }));
    const platform = port({ listSchedules: vi.fn(async () => ({ items: [recurring, single], nextCursor: '', limit: 20 })), cancelSchedule, restoreSchedule });
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();

    expect(host.querySelector('[aria-label="取消 晨间计划 的提醒"]')).not.toBeNull();
    expect(host.querySelector('[aria-label="恢复 单次计划 的提醒"]')).toBeNull();
    expect(host.textContent).not.toContain('提醒已取消');

    const recurringButton = host.querySelector<HTMLButtonElement>('[aria-label="取消 晨间计划 的提醒"]')!;
    recurringButton.click();
    await flushRender();
    expect(host.textContent).toContain('取消这条日程的提醒？');
    host.querySelector<HTMLButtonElement>('.dialog .primary-button')?.click();
    await flushRender();
    expect(cancelSchedule).toHaveBeenCalledWith('7', 1);
    expect(controller.snapshot().schedules.items.find(item => item.id === '7')?.open).toBe(false);

    platform.listSchedules = vi.fn(async () => ({ items: [cancelled, single], nextCursor: '', limit: 20 }));
    await controller.refreshSchedules();
    await flushRender();
    expect(host.querySelector('[aria-label="恢复 晨间计划 的提醒"]')).not.toBeNull();
    expect(host.textContent).toContain('提醒已取消');
    host.querySelector<HTMLButtonElement>('[aria-label="恢复 晨间计划 的提醒"]')?.click();
    await flushRender();
    expect(host.textContent).toContain('恢复这条日程的提醒？');
    host.querySelector<HTMLButtonElement>('.dialog .primary-button')?.click();
    await flushRender();
    expect(restoreSchedule).toHaveBeenCalledWith('7', 2);

    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('renders type-specific recurring controls without single endpoints or cutoff', async () => {
    const platform = port();
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('添加日程'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent === '每周')?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();

    expect(host.querySelectorAll('input[type="datetime-local"], input[type="date"]')).toHaveLength(0);
    expect((host.querySelector('input[aria-label="重复生效日期"]') as HTMLInputElement)?.value).toBe('2026-09-16');
    expect(host.querySelectorAll('select')).toHaveLength(2);
    expect(host.querySelector('input[type="number"]')?.getAttribute('value')).toBe('1');
    expect(host.textContent).toContain('周一');
    expect(host.textContent).toContain('周日');
    expect(host.textContent).not.toContain('重复截止日期');

    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('keeps recurring required marks with their field labels', async () => {
    const platform = port();
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('添加日程'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();

    for (const type of ['每天', '每周', '每月']) {
      [...host.querySelectorAll('button')].find(button => button.textContent === type)?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
      await flushRender();

      const selectFields = [...host.querySelectorAll<HTMLLabelElement>('.recurring-fields .select-field')];
      const fieldLabels = selectFields.map(field => field.querySelector('.field-label'));
      expect(fieldLabels.map(label => label?.textContent?.trim())).toEqual(type === '每月' ? ['小时 *', '分钟 *', '每月日期 *'] : ['小时 *', '分钟 *']);
      expect(fieldLabels.every(label => label?.querySelector('.required-mark') !== null)).toBe(true);
      expect(host.querySelector('input[type="number"]')?.parentElement?.querySelector('.required-mark')).not.toBeNull();
      expect(host.querySelectorAll('.recurring-fields .required-mark')).toHaveLength(type === '每天' ? 3 : 4);
      expect(host.querySelector('.recurring-fields legend .required-mark') !== null).toBe(type === '每周');
    }

    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('renders configuration controls without a database path input', async () => {
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
    expect(host.querySelector('#log-level')).not.toBeNull();
    expect([...host.querySelectorAll('button')].filter(button => button.textContent?.includes('恢复默认'))).toHaveLength(4);
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

  it('refreshes pending reminders when entering the page from navigation', async () => {
    const latest: ReminderDetail = { scheduleId: '7', originalDate: '2026-09-16', attemptedAtUtc: '2026-09-16T02:00:00Z', startUnix: 1799964000, endUnix: 1799964900, title: '最新提醒', notes: 'notes', contentStatus: 'available', status: 'pending', processedAtUtc: null };
    let calls = 0;
    const platform = port({ listPendingReminders: vi.fn(async () => { calls++; return calls === 1 ? { items: [], nextCursor: '', limit: 20 } : { items: [latest], nextCursor: '', limit: 20 }; }) });
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();

    expect(platform.listPendingReminders).toHaveBeenCalledTimes(1);
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('待处理提醒'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();

    expect(platform.listPendingReminders).toHaveBeenCalledTimes(2);
    expect(host.textContent).toContain('最新提醒');
    expect(host.querySelector('button.reminder-card')).not.toBeNull();
    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('shows unavailable reminder content without inventing a body', async () => {
    const unavailable: ReminderDetail = { scheduleId: '7', originalDate: '2026-09-16', attemptedAtUtc: '2026-09-16T02:00:00Z', startUnix: null, endUnix: null, title: null, notes: null, contentStatus: 'unavailable', status: 'pending', processedAtUtc: null };
    const reminders: ReminderPage = { items: [unavailable], nextCursor: '', limit: 20 };
    const confirmReminder = vi.fn(async () => ({ scheduleId: '7', originalDate: unavailable.originalDate, processedAtUtc: '2026-09-16T03:00:00Z', changed: true }));
    const platform = port({ listPendingReminders: vi.fn(async () => reminders), getReminderDetail: vi.fn(async () => unavailable), confirmReminder });
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
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('确认已投递提醒'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    expect(confirmReminder).toHaveBeenCalledWith({ scheduleId: '7', originalDate: unavailable.originalDate });
    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('renders all four theme cards with keyboard-friendly pressed states', async () => {
    const platform = port();
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('设置'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    const cards = [...host.querySelectorAll<HTMLButtonElement>('.theme-card')];
    expect(cards.map(card => card.getAttribute('aria-label'))).toEqual(['奶油桃', '薄荷绿', '晴空蓝', '薰衣草紫']);
    expect(cards.every(card => card.tagName === 'BUTTON' && card.tabIndex >= 0 && card.getAttribute('aria-pressed') !== null)).toBe(true);
    expect(cards.filter(card => card.getAttribute('aria-pressed') === 'true')).toHaveLength(1);
    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('applies a selected theme only after the save promise resolves', async () => {
    let resolveSave!: (value: Theme) => void;
    const saveTheme = vi.fn(() => new Promise<Theme>(resolve => { resolveSave = resolve; }));
    const platform = port({ saveTheme });
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('设置'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    const mint = host.querySelector<HTMLButtonElement>('[aria-label="薄荷绿"]')!;
    mint.click();
    await flushRender();
    expect(saveTheme).toHaveBeenCalledWith('mint');
    expect(host.querySelector<HTMLButtonElement>('[aria-label="奶油桃"]')?.getAttribute('aria-pressed')).toBe('true');
    expect(host.querySelector<HTMLButtonElement>('[aria-label="薄荷绿"]')?.getAttribute('aria-pressed')).toBe('false');
    resolveSave('mint');
    await flushRender();
    expect(host.querySelector<HTMLButtonElement>('[aria-label="薄荷绿"]')?.getAttribute('aria-pressed')).toBe('true');
    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('keeps the original theme and shows an error when saving fails', async () => {
    const platform = port({ saveTheme: vi.fn(async () => { throw { data: JSON.stringify({ code: 'STORAGE', message: '主题保存失败' }) }; }) });
    const controller = new Controller(platform);
    const host = document.createElement('div');
    document.body.append(host);
    const root = createRoot(host);
    root.render(<App controller={controller} />);
    await flushRender();
    [...host.querySelectorAll('button')].find(button => button.textContent?.includes('设置'))?.dispatchEvent(new MouseEvent('click', { bubbles: true }));
    await flushRender();
    host.querySelector<HTMLButtonElement>('[aria-label="晴空蓝"]')?.click();
    await flushRender();
    expect(host.querySelector<HTMLButtonElement>('[aria-label="奶油桃"]')?.getAttribute('aria-pressed')).toBe('true');
    expect(host.querySelector<HTMLButtonElement>('[aria-label="晴空蓝"]')?.getAttribute('aria-pressed')).toBe('false');
    expect(host.textContent).toContain('主题保存失败');
    root.unmount();
    controller.dispose();
    host.remove();
  });

  it('keeps theme text and feedback colors above the AA contrast threshold', () => {
    for (const theme of Object.values(themes)) {
      expect(contrast(theme.text, theme.bg)).toBeGreaterThanOrEqual(4.5);
      expect(contrast(theme.muted, theme.bg)).toBeGreaterThanOrEqual(4.5);
      expect(contrast(theme.error, theme.errorBg)).toBeGreaterThanOrEqual(4.5);
      expect(contrast(theme.success, theme.successBg)).toBeGreaterThanOrEqual(4.5);
    }
  });
});

/** 计算两个十六进制颜色的 WCAG 相对对比度。 */
function contrast(foreground: string, background: string): number {
  const luminance = (hex: string): number => {
    const channels = [0, 2, 4].map(index => Number.parseInt(hex.slice(index + 1, index + 3), 16) / 255).map(channel => channel <= 0.03928 ? channel / 12.92 : ((channel + 0.055) / 1.055) ** 2.4);
    return channels[0] * 0.2126 + channels[1] * 0.7152 + channels[2] * 0.0722;
  };
  const light = Math.max(luminance(foreground), luminance(background));
  const dark = Math.min(luminance(foreground), luminance(background));
  return (light + 0.05) / (dark + 0.05);
}
