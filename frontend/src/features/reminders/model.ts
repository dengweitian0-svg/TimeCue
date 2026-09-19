import type { ReminderDetail, ReminderIdentity } from './contracts';

/** 以稳定身份比较两条提醒，避免依赖列表位置或可变正文。 */
export function sameReminder(left: ReminderIdentity | null | undefined, right: ReminderIdentity | null | undefined): boolean {
  return Boolean(left && right && left.scheduleId === right.scheduleId && left.originalDate === right.originalDate);
}

/** 返回当前提醒同一实际开始时刻的可切换项；无法重建时间的项不被错误合并。 */
export function groupReminders(items: ReminderDetail[], active: ReminderIdentity | null): ReminderDetail[] {
  const selected = items.find(item => sameReminder(item, active));
  if (!selected || selected.startUnix === null) return selected ? [selected] : [];
  return items.filter(item => item.startUnix === selected.startUnix);
}
