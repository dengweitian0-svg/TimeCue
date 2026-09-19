/** 提醒的稳定身份，不依赖列表位置或可变正文。 */
export interface ReminderIdentity {
  scheduleId: string;
  originalDate: string;
}

/** 提醒正文是否可以从当前事实重建。 */
export type ReminderContentStatus = 'available' | 'unavailable';

/** 提醒当前的处理状态。 */
export type ReminderStatus =
  | 'pending'
  | 'confirmed'
  | 'missing'
  | 'not_processable';

/** 待处理提醒端口可能返回的业务错误码。 */
export type PendingReminderErrorCode =
  | 'STALE_CURSOR'
  | 'REMINDER_NOT_PROCESSABLE'
  | 'NOT_FOUND'
  | 'RUNTIME_STOPPING';

/** 待处理提醒详情。 */
export interface ReminderDetail extends ReminderIdentity {
  attemptedAtUtc: string | null;
  startUnix: number | null;
  endUnix: number | null;
  title: string | null;
  notes: string | null;
  contentStatus: ReminderContentStatus;
  status: ReminderStatus;
  processedAtUtc: string | null;
}

/** 待处理提醒分页结果。 */
export interface ReminderPage {
  items: ReminderDetail[];
  nextCursor: string;
  limit: number;
}

/** 待处理提醒列表查询请求。 */
export interface ReminderListRequest {
  limit: number;
  cursor?: string;
}

/** 提醒确认后的状态投影。 */
export interface ReminderConfirmation extends ReminderIdentity {
  processedAtUtc: string;
  changed: boolean;
}

/** 待处理提醒列表变化监听器。 */
export interface PendingRemindersChangedListener {
  (): void;
}
