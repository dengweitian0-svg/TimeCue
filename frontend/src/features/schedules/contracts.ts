import type { FieldErrors } from '../../shared/contracts/errors';

/** 日程创建表单支持的重复类型。 */
export type ScheduleType = 'single' | 'daily' | 'weekly' | 'monthly';

/** 日程创建表单的本地草稿。 */
export interface ScheduleDraft {
  title: string;
  notes: string;
  start: string;
  end: string;
  type: ScheduleType;
  until: string;
  effectiveDate: string;
  hour: string;
  minute: string;
  weekdays: number[];
  monthDay: string;
  durationMinutes: string;
}

/** 创建日程时提交给应用端口的请求。 */
export interface CreateScheduleRequest {
  title: string;
  notes: string;
  start_unix: number;
  end_unix: number;
  cron?: string;
  until_unix?: number;
}

/** 页面使用的日程定义模型。 */
export interface Schedule {
  id: string;
  title: string;
  notes: string;
  firstStartLocal: string;
  durationMinutes: number;
  cron: string | null;
  untilDateLocal: string | null;
  monthEndPolicy: 'clamp' | null;
  open: boolean;
  revision: number;
}

/** 取消或恢复重复提醒后的状态投影。 */
export interface ScheduleActionResult {
  scheduleId: string;
  open: boolean;
  revision: number;
  updatedAtUtc: string;
}

/** 单次日程可用的时间边界。 */
export interface ScheduleBounds {
  min: string;
  startMaxExclusive: string;
  endMaxInclusive: string;
}

/** 日程分页结果。 */
export interface SchedulePage {
  items: Schedule[];
  nextCursor: string;
  limit: number;
}

/** 日程列表查询请求。 */
export interface ScheduleListRequest {
  limit: number;
  cursor?: string;
}

/** 创建表单中可以出现结构化错误的字段。 */
export type ScheduleField =
  | 'title'
  | 'start'
  | 'end'
  | 'type'
  | 'until'
  | 'effectiveDate'
  | 'hour'
  | 'minute'
  | 'weekdays'
  | 'monthDay'
  | 'durationMinutes';

/** 单次日程提交后从页面投影中隐藏的事件。 */
export interface ScheduleHidden {
  scheduleId: string;
}

/** 订阅日程隐藏事件的监听器。 */
export interface ScheduleHiddenListener {
  (event: ScheduleHidden): void;
}

/** 重复日程取消或恢复动作的页面状态。 */
export type ScheduleActionKind = 'cancel' | 'restore';

/** 重复日程动作确认对象。 */
export interface ScheduleActionState {
  kind: ScheduleActionKind;
  item: Schedule;
}

/** 让日程字段错误在 feature 内保持明确的结构类型。 */
export type ScheduleFieldErrors = Partial<Record<ScheduleField, string>> &
  FieldErrors;
