import type { RecoveryPort, RuntimePort } from './runtime';
import type { ReminderPort } from './reminders';
import type { SchedulePort } from './schedules';
import type { SettingsPort } from './settings';

/** Wails 之外的生产前端统一端口。 */
export interface WailsPort
  extends SchedulePort,
    ReminderPort,
    SettingsPort,
    RuntimePort {}

/** 允许 Controller 释放适配器和订阅资源的应用端口。 */
export interface AppPort extends WailsPort, RecoveryPort {
  dispose?(): void;
}
