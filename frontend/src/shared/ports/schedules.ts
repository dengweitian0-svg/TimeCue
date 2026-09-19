import type { CreateScheduleRequest, Schedule, ScheduleActionResult, ScheduleBounds, ScheduleHiddenListener, ScheduleListRequest, SchedulePage } from '../../features/schedules/contracts';

/** 日程查询和状态转换所需的窄端口。 */
export interface SchedulePort {
  listSchedules(request?: ScheduleListRequest): Promise<SchedulePage>;
  getSchedule(id: string): Promise<Schedule>;
  getScheduleTimeBounds(): Promise<ScheduleBounds>;
  createSchedule(request: CreateScheduleRequest): Promise<Schedule>;
  deleteSchedule(id: string, expectedRevision: number): Promise<void>;
  cancelSchedule(id: string, expectedRevision: number): Promise<ScheduleActionResult>;
  restoreSchedule(id: string, expectedRevision: number): Promise<ScheduleActionResult>;
  subscribeScheduleHidden(listener: ScheduleHiddenListener): () => void;
}
