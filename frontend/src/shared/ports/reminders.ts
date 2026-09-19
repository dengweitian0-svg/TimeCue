import type { PendingRemindersChangedListener, ReminderConfirmation, ReminderDetail, ReminderIdentity, ReminderListRequest, ReminderPage } from '../../features/reminders/contracts';

/** 提醒查询、确认和列表变化通知所需的窄端口。 */
export interface ReminderPort {
  listPendingReminders(request?: ReminderListRequest): Promise<ReminderPage>;
  getReminderDetail(identity: ReminderIdentity): Promise<ReminderDetail>;
  confirmReminder(identity: ReminderIdentity): Promise<ReminderConfirmation>;
  subscribePendingRemindersChanged(listener: PendingRemindersChangedListener): () => void;
}
