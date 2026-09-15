import type { Theme } from './themes';

export type DirectoryKind = 'logs' | 'backups';
export type CloseBehavior = 'tray' | 'exit';
export type BackgroundConfig = { kind: 'solid' } | { kind: 'image' | 'video'; resource: string; fit: 'cover' | 'contain'; overlay: number };
export interface Settings { theme: Theme; background: BackgroundConfig; closeBehavior: CloseBehavior; logs: string; backups: string }
export interface Reminder { id: string; title: string; body: string; at: string; confirmed: boolean }
export type Detail = { status: 'pending'; message: Reminder } | { status: 'confirmed' | 'missing'; id: string };
export interface RuntimeState {
  visibility: 'visible' | 'hidden' | 'minimized';
  lifecycle: 'running' | 'stopping' | 'stopped' | 'cleanup-failed';
  ready: boolean;
  pendingTarget: string | null;
  activation: { sequence: number; target: string } | null;
  notifications: string[];
}
export interface SettingsPort {
  readSettings(): Promise<Settings>;
  savePreferences(value: Pick<Settings, 'theme' | 'closeBehavior'>): Promise<Settings>;
  chooseDirectory(kind: DirectoryKind): Promise<string | null>;
  saveDirectory(kind: DirectoryKind, path: string): Promise<{ effectivePath: string }>;
}
export interface MessagesPort {
  list(): Promise<Reminder[]>;
  detail(id: string): Promise<Detail>;
  confirm(id: string): Promise<void>;
}
export interface WindowPort {
  runtime(): RuntimeState;
  close(): Promise<void>;
  minimize(): void;
  open(): void;
  launch(): void;
  exit(): Promise<void>;
}
export interface ActivationPort { activate(id: string): void; setReady(ready: boolean): void }
export type AdapterEvent = 'runtime' | 'external' | 'reset';
export interface Ports extends SettingsPort, MessagesPort, WindowPort, ActivationPort, SchedulesPort {
  subscribe(listener: (event: AdapterEvent) => void): () => void;
}

export type ScheduleType = 'single' | 'daily' | 'weekly' | 'monthly';
export interface ScheduleDraft { title: string; notes: string; start: string; end: string; type: ScheduleType; until: string }
export interface CreateScheduleRequest { title: string; notes: string; start_unix: number; end_unix: number; cron?: string; until_unix?: number }
export interface Schedule { id: string; title: string; notes: string; firstStartLocal: string; durationMinutes: number; cron: string | null; untilDateLocal: string | null; monthEndPolicy: 'clamp' | null; revision: number }
export interface ScheduleBounds { min: string; startMaxExclusive: string; endMaxInclusive: string }
export type ScheduleField = 'title' | 'start' | 'end' | 'type' | 'until';
export interface SchedulesPort {
  listSchedules(): Promise<Schedule[]>;
  getSchedule(id: string): Promise<Schedule>;
  scheduleBounds(): Promise<ScheduleBounds>;
  createSchedule(request: CreateScheduleRequest): Promise<Schedule>;
  deleteSchedule(id: string, expectedRevision: number): Promise<void>;
}

/** 日程错误保留字段、最新边界与并发状态，供页面选择明确的恢复入口。 */
export class ScheduleError extends Error {
  /** 记录适配器或校验层返回的结构化失败信息。 */
  constructor(public readonly code: 'VALIDATION' | 'TIME_RANGE_EXCEEDED' | 'STALE_REVISION' | 'NOT_FOUND' | 'STORAGE', message: string, public readonly fields: Partial<Record<ScheduleField, string>> = {}, public readonly bounds?: ScheduleBounds) { super(message); this.name = 'ScheduleError'; }
}

/** 原型边界错误携带可呈现信息与平台返回的实际生效路径。 */
export class PrototypeError extends Error {
  /** 保存面向用户的说明和可选实际路径。 */
  constructor(message: string, public readonly effectivePath?: string) { super(message); this.name = 'PrototypeError'; }
}
