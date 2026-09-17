export type CloseBehavior = 'tray' | 'exit';
export type DirectoryKind = 'logs' | 'backups';
export type LogLevel = 'debug' | 'info' | 'warn' | 'error';
export type ConfigProperty = 'log.level' | 'log.path' | 'backup.dir' | 'window.close_behavior';
export type ScheduleType = 'single' | 'daily' | 'weekly' | 'monthly';
export type Lifecycle = 'running' | 'stopping' | 'stopped' | 'cleanup-failed';

export interface ScheduleDraft {
  title: string;
  notes: string;
  start: string;
  end: string;
  type: ScheduleType;
  until: string;
}

export interface CreateScheduleRequest {
  title: string;
  notes: string;
  start_unix: number;
  end_unix: number;
  cron?: string;
  until_unix?: number;
}

export interface Schedule {
  id: string;
  title: string;
  notes: string;
  firstStartLocal: string;
  durationMinutes: number;
  cron: string | null;
  untilDateLocal: string | null;
  monthEndPolicy: 'clamp' | null;
  revision: number;
}

export interface ScheduleBounds {
  min: string;
  startMaxExclusive: string;
  endMaxInclusive: string;
}

export interface SchedulePage {
  items: Schedule[];
  nextCursor: string;
  limit: number;
}

export interface ScheduleListRequest { limit: number; cursor?: string }

export type ScheduleField = 'title' | 'start' | 'end' | 'type' | 'until';

export interface RuntimeStatus {
  visibility: 'visible' | 'hidden' | 'minimized';
  lifecycle: Lifecycle;
  ready: boolean;
}

export interface Activation {
  sequence: number;
  targetId: string;
  originalDate: string;
}

export interface FrontendStatus {
  runtime: RuntimeStatus;
  closeBehavior: CloseBehavior;
  activation: Activation | null;
}

export interface StorageDirectory {
  kind: DirectoryKind;
  effectivePath: string;
  savedPath: string;
  restartRequired: boolean;
}

export interface StorageDirectories {
  logs: StorageDirectory;
  backups: StorageDirectory;
}

export interface LogLevelSettings {
  effectiveLevel: LogLevel;
  savedLevel: LogLevel;
  restartRequired: boolean;
}

export interface ReminderIdentity {
  scheduleId: string;
  originalDate: string;
}

export type ReminderContentStatus = 'available' | 'unavailable';
export type ReminderStatus = 'pending' | 'confirmed' | 'missing' | 'not_processable';
export type PendingReminderErrorCode = 'STALE_CURSOR' | 'REMINDER_NOT_PROCESSABLE' | 'NOT_FOUND' | 'RUNTIME_STOPPING';

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

export interface ReminderPage {
  items: ReminderDetail[];
  nextCursor: string;
  limit: number;
}

export interface ReminderListRequest {
  limit: number;
  cursor?: string;
}

export interface ReminderConfirmation extends ReminderIdentity {
  processedAtUtc: string;
  changed: boolean;
}

export interface ActivationListener {
  (activation: Activation): void;
}

export interface WailsPort {
  listSchedules(request?: ScheduleListRequest): Promise<SchedulePage>;
  getSchedule(id: string): Promise<Schedule>;
  getScheduleTimeBounds(): Promise<ScheduleBounds>;
  listPendingReminders(request?: ReminderListRequest): Promise<ReminderPage>;
  getReminderDetail(identity: ReminderIdentity): Promise<ReminderDetail>;
  confirmReminder(identity: ReminderIdentity): Promise<ReminderConfirmation>;
  createSchedule(request: CreateScheduleRequest): Promise<Schedule>;
  deleteSchedule(id: string, expectedRevision: number): Promise<void>;
  getRuntimeStatus(): Promise<RuntimeStatus>;
  getDesktopStatus(): Promise<FrontendStatus>;
  getCloseBehavior(): Promise<CloseBehavior>;
  saveCloseBehavior(value: CloseBehavior): Promise<CloseBehavior>;
  getLogLevel(): Promise<LogLevelSettings>;
  saveLogLevel(value: LogLevel): Promise<LogLevelSettings>;
  getStorageDirectories(): Promise<StorageDirectories>;
  chooseDirectory(kind: DirectoryKind): Promise<string | null>;
  saveStorageDirectory(kind: DirectoryKind, path: string): Promise<StorageDirectory>;
  restoreConfigDefault(property: ConfigProperty): Promise<void>;
  frontendReady(lastSequence: number): Promise<Activation | null>;
  retryCleanup(): Promise<void>;
  subscribeActivation(listener: ActivationListener): () => void;
}

export interface AppPort extends WailsPort {
  dispose?(): void;
}

export class ScheduleError extends Error {
  constructor(
    public readonly code: 'VALIDATION' | 'TIME_RANGE_EXCEEDED' | 'STALE_REVISION' | 'STALE_CURSOR' | 'REMINDER_NOT_PROCESSABLE' | 'NOT_FOUND' | 'STORAGE' | 'RUNTIME_STOPPING',
    message: string,
    public readonly fields: Partial<Record<ScheduleField, string>> = {},
    public readonly bounds?: ScheduleBounds,
  ) {
    super(message);
    this.name = 'ScheduleError';
  }
}

/** 将未知的 Wails 拒绝值转换为稳定的业务错误，避免 UI 依赖 IPC 细节。 */
export function toScheduleError(value: unknown): ScheduleError {
  if (value instanceof ScheduleError) return value;
  const payload = unwrapError(value);
  const code = isScheduleCode(payload.code) ? payload.code : 'STORAGE';
  const fields = toFieldMessages(payload.fields, typeof payload.message === 'string' ? payload.message : '请检查此字段。');
  const bounds = isRecord(payload.bounds) ? payload.bounds as unknown as ScheduleBounds : undefined;
  return new ScheduleError(code, typeof payload.message === 'string' ? payload.message : '操作未完成，请重试。', fields, bounds);
}

/** 把后端字段名数组转为表单可显示的字段错误，同时兼容端口直接返回的映射。 */
function toFieldMessages(value: unknown, message: string): Partial<Record<ScheduleField, string>> {
  const result: Partial<Record<ScheduleField, string>> = {};
  if (Array.isArray(value)) {
    for (const field of value) {
      const name = mapBackendField(String(field));
      if (name) result[name] = message;
    }
    return result;
  }
  if (isRecord(value)) {
    for (const [field, fieldMessage] of Object.entries(value)) {
      const name = mapBackendField(field);
      if (name) result[name] = typeof fieldMessage === 'string' ? fieldMessage : message;
    }
  }
  return result;
}

/** 将 Go JSON 解码器使用的 snake_case 字段名映射到表单字段。 */
function mapBackendField(value: string): ScheduleField | undefined {
  const fields: Record<string, ScheduleField> = { title: 'title', start: 'start', start_unix: 'start', end: 'end', end_unix: 'end', type: 'type', cron: 'type', until: 'until', until_unix: 'until' };
  return fields[value];
}

/** 展开 Wails 常见的 data/message 错误包装并接受结构化 JSON 字符串。 */
function unwrapError(value: unknown): Record<string, unknown> {
  if (isRecord(value)) {
    const nested = value.data ?? value.error;
    if (typeof nested === 'string') return parseErrorText(nested);
    if (isRecord(nested)) return nested;
    return value;
  }
  if (value instanceof Error) return parseErrorText(value.message);
  if (typeof value === 'string') return parseErrorText(value);
  return {};
}

/** 解析后端传回的 JSON 错误而不泄漏未结构化对象。 */
function parseErrorText(value: string): Record<string, unknown> {
  try {
    const parsed: unknown = JSON.parse(value);
    return isRecord(parsed) ? parsed : { message: value };
  } catch {
    return { message: value };
  }
}

/** 判断值是否为可读取字段的普通对象。 */
function isRecord(value: unknown): value is Record<string, unknown> {
  return typeof value === 'object' && value !== null;
}

/** 限定后端错误码，未知错误统一按存储失败处理。 */
function isScheduleCode(value: unknown): value is ScheduleError['code'] {
  return value === 'VALIDATION' || value === 'TIME_RANGE_EXCEEDED' || value === 'STALE_REVISION' || value === 'STALE_CURSOR' || value === 'REMINDER_NOT_PROCESSABLE' || value === 'NOT_FOUND' || value === 'STORAGE' || value === 'RUNTIME_STOPPING';
}
