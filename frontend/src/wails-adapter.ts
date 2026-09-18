import type { Activation, ActivationListener, AppPort, CloseBehavior, ConfigProperty, CreateScheduleRequest, DirectoryKind, FrontendStatus, LogLevel, LogLevelSettings, RecoveryEvent, ReminderConfirmation, ReminderDetail, ReminderIdentity, ReminderListRequest, ReminderPage, RuntimeStatus, Schedule, ScheduleBounds, ScheduleListRequest, SchedulePage, StorageDirectories, StorageDirectory } from './contracts';
import * as generatedBindings from '../wailsjs/go/runtime/WailsBindings';
import { EventsOn as generatedEventsOn } from '../wailsjs/runtime/runtime';

/** 可注入的 Go bindings 端口；生成文件只在生产启动时接入，测试不依赖其存在。 */
export interface BindingsPort {
  ListSchedules(request: ScheduleListRequest): Promise<unknown>;
  GetSchedule(id: number | string): Promise<unknown>;
  GetScheduleTimeBounds(): Promise<unknown>;
  ListPendingReminders(request: ReminderListRequest): Promise<unknown>;
  GetReminderDetail(scheduleID: number | string, originalDate: string): Promise<unknown>;
  ConfirmReminder(scheduleID: number | string, originalDate: string): Promise<unknown>;
  CreateSchedule(payload: string): Promise<unknown>;
  DeleteSchedule(id: number, payload: string): Promise<unknown>;
  GetRuntimeStatus(): Promise<unknown>;
  GetDesktopStatus(): Promise<unknown>;
  GetCloseBehavior(): Promise<unknown>;
  SaveCloseBehavior(request: { behavior: CloseBehavior }): Promise<unknown>;
  GetLogLevel(): Promise<unknown>;
  SaveLogLevel(request: { level: LogLevel }): Promise<unknown>;
  GetStorageDirectories(): Promise<unknown>;
  ChooseStorageDirectory(kind: DirectoryKind): Promise<unknown>;
  SaveStorageDirectory(request: { kind: DirectoryKind; path: string }): Promise<unknown>;
  RestoreConfigDefault(request: { property: ConfigProperty }): Promise<unknown>;
  FrontendReady(request: { last_sequence: number }): Promise<unknown>;
  RetryCleanup(): Promise<unknown>;
}

/** Wails runtime EventsOn 的最小端口，便于在绑定文件生成前做契约测试。 */
export type EventsOn = (name: string, listener: (...args: unknown[]) => void) => void | (() => void);

export const ACTIVATION_EVENT = 'activation';
export const RECOVERY_EVENT = 'reminder_recovery';

/** 将 Wails 生成模块包成生产前端需要的类型安全端口。 */
export class WailsAdapter implements AppPort {
  private readonly activationListeners = new Set<ActivationListener>();
  private readonly recoveryListeners = new Set<(event: RecoveryEvent) => void>();
  private readonly unsubscribeEvents: (() => void) | null;
  private unsubscribeRecoveryEvents: (() => void) | null = null;

  constructor(private readonly bindings: BindingsPort, private readonly eventsOn?: EventsOn) {
    this.unsubscribeEvents = eventsOn ? normalizeUnsubscribe(eventsOn(ACTIVATION_EVENT, (...args) => this.onActivation(args))) : null;
  }

  /** 释放事件监听，避免热更新或窗口重建后重复接收激活。 */
  dispose(): void { this.unsubscribeEvents?.(); this.unsubscribeRecoveryEvents?.(); this.activationListeners.clear(); this.recoveryListeners.clear(); }

  /** 注册激活监听并返回可重复调用的取消函数。 */
  subscribeActivation(listener: ActivationListener): () => void {
    this.activationListeners.add(listener);
    return () => this.activationListeners.delete(listener);
  }

  /** 注册冷启动恢复状态监听，完成事件由控制器触发待处理列表刷新。 */
  subscribeRecovery(listener: (event: RecoveryEvent) => void): () => void {
    if (this.recoveryListeners.size === 0 && this.eventsOn) {
      this.unsubscribeRecoveryEvents = normalizeUnsubscribe(this.eventsOn(RECOVERY_EVENT, (...args) => this.onRecovery(args)));
    }
    this.recoveryListeners.add(listener);
    return () => this.recoveryListeners.delete(listener);
  }

  /** 将 Wails 事件参数转换为稳定的 sequence/target 结构。 */
  private onActivation(args: unknown[]): void {
    const activation = toActivation(args.length === 1 ? args[0] : args);
    if (!activation) return;
    for (const listener of this.activationListeners) listener(activation);
  }

  /** 将 Wails 恢复事件转换为受控的前端状态枚举。 */
  private onRecovery(args: unknown[]): void {
    const item = asRecord(args.length === 1 ? args[0] : args);
    const status = read(item, 'status', 'Status');
    if (status !== 'idle' && status !== 'running' && status !== 'failed') return;
    const event: RecoveryEvent = { status, errorCode: typeof read(item, 'error_code', 'errorCode', 'ErrorCode') === 'string' ? String(read(item, 'error_code', 'errorCode', 'ErrorCode')) : undefined };
    for (const listener of this.recoveryListeners) listener(event);
  }

  /** 读取分页日程，并把后端 int64 ID 转为 UI string。 */
  async listSchedules(request: ScheduleListRequest = { limit: 20 }): Promise<SchedulePage> {
    const raw = await this.bindings.ListSchedules(request);
    if (Array.isArray(raw)) return { items: raw.map(toSchedule), nextCursor: '', limit: request.limit };
    const value = asRecord(raw);
    const values = read(value, 'items', 'Items', 'schedules', 'Schedules');
    const items = Array.isArray(values) ? values.map(toSchedule) : [];
    return { items, nextCursor: String(read(value, 'next_cursor', 'nextCursor', 'NextCursor') ?? ''), limit: asNumber(read(value, 'limit', 'Limit')) ?? request.limit };
  }

  /** 读取单条日程，供并发删除失败后的重新确认使用。 */
  async getSchedule(id: string): Promise<Schedule> { return toSchedule(await this.bindings.GetSchedule(int64Value(id))); }

  /** 读取后端当前时间边界。 */
  async getScheduleTimeBounds(): Promise<ScheduleBounds> { return toBounds(await this.bindings.GetScheduleTimeBounds()); }

  /** 读取待处理提醒分页，并把后端身份和可空正文转换为前端模型。 */
  async listPendingReminders(request: ReminderListRequest = { limit: 20 }): Promise<ReminderPage> {
    return toReminderPage(await this.bindings.ListPendingReminders(request), request.limit);
  }

  /** 按稳定提醒身份读取详情，不把读取动作解释为确认。 */
  async getReminderDetail(identity: ReminderIdentity): Promise<ReminderDetail> {
    return toReminderDetail(await this.bindings.GetReminderDetail(int64Value(identity.scheduleId), identity.originalDate));
  }

  /** 按稳定提醒身份提交确认，并保留后端幂等结果。 */
  async confirmReminder(identity: ReminderIdentity): Promise<ReminderConfirmation> {
    return toReminderConfirmation(await this.bindings.ConfirmReminder(int64Value(identity.scheduleId), identity.originalDate));
  }

  /** 提交创建请求并把返回的 int64 标识转为 string。 */
  async createSchedule(request: CreateScheduleRequest): Promise<Schedule> { return toSchedule(await this.bindings.CreateSchedule(JSON.stringify(request))); }

  /** 按版本删除日程，保留后端并发保护错误。 */
  async deleteSchedule(id: string, expectedRevision: number): Promise<void> { await this.bindings.DeleteSchedule(int64Value(id), JSON.stringify({ expected_revision: expectedRevision })); }

  /** 查询调度器运行状态。 */
  async getRuntimeStatus(): Promise<RuntimeStatus> { return toRuntime(await this.bindings.GetRuntimeStatus()); }

  /** 查询窗口、生命周期及当前激活快照。 */
  async getDesktopStatus(): Promise<FrontendStatus> { return toDesktopStatus(await this.bindings.GetDesktopStatus()); }

  /** 读取当前关闭行为。 */
  async getCloseBehavior(): Promise<CloseBehavior> { return toCloseBehavior(read(asRecord(await this.bindings.GetCloseBehavior()), 'behavior', 'Behavior')); }

  /** 保存关闭行为，并只接受两个产品枚举。 */
  async saveCloseBehavior(value: CloseBehavior): Promise<CloseBehavior> { await this.bindings.SaveCloseBehavior({ behavior: value }); return value; }

  /** 读取日志级别当前生效值与保存值。 */
  async getLogLevel(): Promise<LogLevelSettings> { return toLogLevel(await this.bindings.GetLogLevel()); }

  /** 保存日志级别，并保留重启生效状态。 */
  async saveLogLevel(value: LogLevel): Promise<LogLevelSettings> { return toLogLevel(await this.bindings.SaveLogLevel({ level: value })); }

  /** 读取日志和备份目录的 effective/saved 对照。 */
  async getStorageDirectories(): Promise<StorageDirectories> { return toStorageDirectories(await this.bindings.GetStorageDirectories()); }

  /** 打开目录选择器；Wails 空字符串表示取消。 */
  async chooseDirectory(kind: DirectoryKind): Promise<string | null> { const value = await this.bindings.ChooseStorageDirectory(kind); return typeof value === 'string' && value !== '' ? value : null; }

  /** 保存单类目录，并保留后端返回的重启状态。 */
  async saveStorageDirectory(kind: DirectoryKind, path: string): Promise<StorageDirectory> { return toStorageDirectory(await this.bindings.SaveStorageDirectory({ kind, path }), kind); }

  /** 将指定配置属性恢复为默认值。 */
  async restoreConfigDefault(property: ConfigProperty): Promise<void> { await this.bindings.RestoreConfigDefault({ property }); }

  /** 注册前端事件后向后端回报最后消费的激活序号。 */
  async frontendReady(lastSequence: number): Promise<Activation | null> {
    const raw = await this.bindings.FrontendReady({ last_sequence: lastSequence });
    return toActivation(read(asRecord(raw), 'activation', 'Activation') ?? raw);
  }

  /** 请求后端重试系统通知清理。 */
  async retryCleanup(): Promise<void> { await this.bindings.RetryCleanup(); }
}

/** 使用生成方法的显式包装建立稳定端口，避免生产代码自行拼接 IPC 名称。 */
export async function createWailsAdapter(): Promise<WailsAdapter> {
  const bindings: BindingsPort = {
    ListSchedules: request => generatedBindings.ListSchedules({ limit: request.limit, cursor: request.cursor ?? '' }),
    GetSchedule: id => generatedBindings.GetSchedule(Number(id)),
    GetScheduleTimeBounds: generatedBindings.GetScheduleTimeBounds,
    ListPendingReminders: request => generatedBindings.ListPendingReminders({ limit: request.limit, cursor: request.cursor ?? '' }),
    GetReminderDetail: (scheduleID, originalDate) => generatedBindings.GetReminderDetail(Number(scheduleID), originalDate),
    ConfirmReminder: (scheduleID, originalDate) => generatedBindings.ConfirmReminder(Number(scheduleID), originalDate),
    CreateSchedule: generatedBindings.CreateSchedule,
    DeleteSchedule: generatedBindings.DeleteSchedule,
    GetRuntimeStatus: generatedBindings.GetRuntimeStatus,
    GetDesktopStatus: generatedBindings.GetDesktopStatus,
    GetCloseBehavior: generatedBindings.GetCloseBehavior,
    SaveCloseBehavior: generatedBindings.SaveCloseBehavior,
    GetLogLevel: generatedBindings.GetLogLevel,
    SaveLogLevel: generatedBindings.SaveLogLevel,
    GetStorageDirectories: generatedBindings.GetStorageDirectories,
    ChooseStorageDirectory: generatedBindings.ChooseStorageDirectory,
    SaveStorageDirectory: generatedBindings.SaveStorageDirectory,
    RestoreConfigDefault: generatedBindings.RestoreConfigDefault,
    FrontendReady: generatedBindings.FrontendReady,
    RetryCleanup: generatedBindings.RetryCleanup,
  };
  return new WailsAdapter(bindings, generatedEventsOn as EventsOn);
}

/** 将动态导入模块的取消返回值统一为可调用函数。 */
function normalizeUnsubscribe(value: void | (() => void)): (() => void) | null { return typeof value === 'function' ? value : null; }

/** 将未知值读取为对象，缺失结构由调用方给出稳定默认值。 */
function asRecord(value: unknown): Record<string, unknown> { return typeof value === 'object' && value !== null ? value as Record<string, unknown> : {}; }

/** 从生成 DTO 兼容读取 Go json tag、字段名及旧式首字母大写字段。 */
function read(value: Record<string, unknown>, ...keys: string[]): unknown { for (const key of keys) if (value[key] !== undefined) return value[key]; return undefined; }

/** 将 int64 线上的数字或字符串转换为不丢精度的 UI 字符串。 */
function int64String(value: unknown): string {
  if (typeof value === 'bigint') return value.toString();
  if (typeof value === 'string' && /^-?\d+$/.test(value)) return BigInt(value).toString();
  if (typeof value === 'number' && Number.isSafeInteger(value)) return String(value);
  throw new Error('invalid int64 value');
}

/** 将 UI string ID 转成生成绑定可接受的安全 number 或 int64 string。 */
function int64Value(value: string): number { const integer = BigInt(value); const number = Number(integer); if (!Number.isSafeInteger(number)) throw new Error('日程标识超出桌面端可安全处理的范围。'); return number; }

/** 将 DTO 转换为生产页面使用的 Schedule 模型。 */
function toSchedule(value: unknown): Schedule {
  const item = asRecord(value);
  return {
    id: int64String(read(item, 'id', 'ID', 'schedule_id', 'scheduleID')),
    title: String(read(item, 'title', 'Title') ?? ''),
    notes: String(read(item, 'notes', 'Notes', 'body', 'Body') ?? ''),
    firstStartLocal: String(read(item, 'first_start_local', 'firstStartLocal', 'FirstStartLocal') ?? ''),
    durationMinutes: asNumber(read(item, 'duration_minutes', 'durationMinutes', 'DurationMinutes')) ?? 0,
    cron: read(item, 'cron', 'Cron') == null ? null : String(read(item, 'cron', 'Cron')),
    untilDateLocal: read(item, 'until_date_local', 'untilDateLocal', 'UntilDateLocal') == null ? null : String(read(item, 'until_date_local', 'untilDateLocal', 'UntilDateLocal')),
    monthEndPolicy: read(item, 'month_end_policy', 'monthEndPolicy', 'MonthEndPolicy') == null ? null : 'clamp',
    revision: asNumber(read(item, 'revision', 'Revision')) ?? 0,
  };
}

/** 将后端时间范围 DTO 映射为表单边界。 */
function toBounds(value: unknown): ScheduleBounds {
  const item = asRecord(value);
  const min = read(item, 'min', 'Min'); const startMax = read(item, 'start_max_exclusive', 'startMaxExclusive', 'StartMaxExclusive'); const endMax = read(item, 'end_max_inclusive', 'endMaxInclusive', 'EndMaxInclusive');
  return { min: String(min ?? unixLocal(read(item, 'single_min_unix'))), startMaxExclusive: String(startMax ?? unixLocal(read(item, 'single_max_exclusive_unix'))), endMaxInclusive: String(endMax ?? unixLocal(read(item, 'single_max_exclusive_unix'))) };
}

/** 将提醒分页 DTO 转为前端列表模型，并保留不透明游标原文。 */
function toReminderPage(value: unknown, fallbackLimit: number): ReminderPage {
  if (Array.isArray(value)) return { items: value.map(toReminderDetail), nextCursor: '', limit: fallbackLimit };
  const item = asRecord(value);
  const values = read(item, 'items', 'Items', 'reminders', 'Reminders');
  return {
    items: Array.isArray(values) ? values.map(toReminderDetail) : [],
    nextCursor: String(read(item, 'next_cursor', 'nextCursor', 'NextCursor') ?? ''),
    limit: asNumber(read(item, 'limit', 'Limit')) ?? fallbackLimit,
  };
}

/** 将提醒详情 DTO 转为稳定身份、状态和可空正文模型。 */
function toReminderDetail(value: unknown): ReminderDetail {
  const item = asRecord(value);
  const identity = reminderIdentity(item);
  return {
    ...identity,
    attemptedAtUtc: nullableString(read(item, 'attempted_at_utc', 'attemptedAtUtc', 'AttemptedAtUTC')),
    startUnix: nullableUnix(read(item, 'start_unix', 'startUnix', 'StartUnix')),
    endUnix: nullableUnix(read(item, 'end_unix', 'endUnix', 'EndUnix')),
    title: nullableString(read(item, 'title', 'Title')),
    notes: nullableString(read(item, 'notes', 'Notes')),
    contentStatus: enumValue(read(item, 'content_status', 'contentStatus', 'ContentStatus'), ['available', 'unavailable'], 'unavailable'),
    status: enumValue(read(item, 'status', 'Status'), ['pending', 'confirmed', 'missing', 'not_processable'], 'missing'),
    processedAtUtc: nullableString(read(item, 'processed_at_utc', 'processedAtUtc', 'ProcessedAtUTC')),
  };
}

/** 将确认结果 DTO 转为前端确认结果，并复用同一身份转换规则。 */
function toReminderConfirmation(value: unknown): ReminderConfirmation {
  const item = asRecord(value);
  return {
    ...reminderIdentity(item),
    processedAtUtc: String(read(item, 'processed_at_utc', 'processedAtUtc', 'ProcessedAtUTC') ?? ''),
    changed: Boolean(read(item, 'changed', 'Changed')),
  };
}

/** 读取提醒的稳定身份，支持 DTO 直接字段和兼容嵌套字段。 */
function reminderIdentity(value: Record<string, unknown>): ReminderIdentity {
  const nested = asRecord(read(value, 'identity', 'Identity'));
  return {
    scheduleId: int64String(read(value, 'schedule_id', 'scheduleId', 'ScheduleID') ?? read(nested, 'schedule_id', 'scheduleId', 'ScheduleID')),
    originalDate: String(read(value, 'original_date', 'originalDate', 'OriginalDate') ?? read(nested, 'original_date', 'originalDate', 'OriginalDate') ?? ''),
  };
}

/** 将后端可空字符串字段映射为 null，避免以空正文伪造不可用内容。 */
function nullableString(value: unknown): string | null { return value === null || value === undefined ? null : String(value); }

/** 将后端可空 Unix 秒字段映射为安全数字或 null。 */
function nullableUnix(value: unknown): number | null { const number = asNumber(value); return number === undefined ? null : number; }

/** 将运行状态 DTO 映射为受控枚举。 */
function toRuntime(value: unknown): RuntimeStatus {
  const item = asRecord(value);
  const lifecycle = read(item, 'lifecycle', 'Lifecycle', 'state', 'State');
  const recovery = read(item, 'recovery_status', 'recoveryStatus', 'RecoveryStatus');
  return { visibility: enumValue(read(item, 'visibility', 'Visibility'), ['visible', 'hidden', 'minimized'], 'visible'), lifecycle: enumValue(lifecycle === 'cleanup_failed' ? 'cleanup-failed' : lifecycle, ['running', 'stopping', 'stopped', 'cleanup-failed'], 'running'), ready: Boolean(read(item, 'ready', 'Ready', 'frontend_ready', 'FrontendReady')), recoveryStatus: enumValue(recovery, ['idle', 'running', 'failed'], 'idle') } as RuntimeStatus;
}

/** 从后端枚举中选择受支持的值，并为损坏状态提供安全回退。 */
function enumValue<T extends string>(value: unknown, values: readonly T[], fallback: T): T { return typeof value === 'string' && values.includes(value as T) ? value as T : fallback; }

/** 将桌面状态 DTO 映射为统一的状态快照。 */
function toDesktopStatus(value: unknown): FrontendStatus {
  const item = asRecord(value); const runtime = read(item, 'runtime', 'Runtime');
  const activation = toActivation(read(item, 'activation', 'Activation')); return { runtime: toRuntime(runtime ?? item), closeBehavior: toCloseBehavior(read(item, 'close_behavior', 'closeBehavior', 'CloseBehavior', 'behavior', 'Behavior')), activation };
}

/** 将目录设置 DTO 从 Go snake_case 字段转换为页面模型。 */
function toStorageDirectories(value: unknown): StorageDirectories {
  const item = asRecord(value);
  return {
    logs: toStorageDirectory(read(item, 'logs', 'Logs'), 'logs'),
    backups: toStorageDirectory(read(item, 'backups', 'Backups'), 'backups'),
  };
}

/** 转换单个目录 DTO，并拒绝数据库等未开放的目录类型。 */
function toStorageDirectory(value: unknown, fallbackKind: DirectoryKind): StorageDirectory {
  const item = asRecord(value);
  const rawKind = read(item, 'kind', 'Kind');
  const kind: DirectoryKind = rawKind === 'backups' ? 'backups' : rawKind === 'logs' ? 'logs' : fallbackKind;
  return {
    kind,
    effectivePath: String(read(item, 'effective_path', 'effectivePath', 'EffectivePath') ?? ''),
    savedPath: String(read(item, 'saved_path', 'savedPath', 'SavedPath') ?? ''),
    restartRequired: Boolean(read(item, 'restart_required', 'restartRequired', 'RestartRequired')),
  };
}

/** 将日志级别 DTO 映射为受控枚举和重启状态。 */
function toLogLevel(value: unknown): LogLevelSettings {
  const item = asRecord(value);
  const level = (candidate: unknown): LogLevel => enumValue(candidate, ['debug', 'info', 'warn', 'error'], 'info');
  return {
    effectiveLevel: level(read(item, 'effective_level', 'effectiveLevel', 'EffectiveLevel')),
    savedLevel: level(read(item, 'saved_level', 'savedLevel', 'SavedLevel')),
    restartRequired: Boolean(read(item, 'restart_required', 'restartRequired', 'RestartRequired')),
  };
}

/** 只接受 tray/exit，防止平台数据污染界面状态。 */
function toCloseBehavior(value: unknown): CloseBehavior { return value === 'exit' ? 'exit' : 'tray'; }

/** 读取激活目标，忽略无效序号和空目标。 */
function toActivation(value: unknown): Activation | null {
  if (Array.isArray(value)) return value.length === 1 ? toActivation(value[0]) : toActivation({ sequence: value[0], target_id: value[1] });
  const item = asRecord(value); const sequence = asNumber(read(item, 'sequence', 'Sequence')); const targetObject = asRecord(read(item, 'target', 'Target')); const target = read(item, 'target_id', 'targetId', 'TargetID') ?? read(targetObject, 'schedule_id', 'scheduleId', 'ScheduleID'); const originalDate = String(read(item, 'original_date', 'originalDate', 'OriginalDate') ?? read(targetObject, 'original_date', 'originalDate', 'OriginalDate') ?? '');
  if (!sequence || !Number.isSafeInteger(sequence) || sequence < 1) return null;
  if (target === undefined || target === null) return { sequence, targetId: '', originalDate: '' };
  try { return { sequence, targetId: int64String(target), originalDate }; } catch { return null; }
}

/** 读取有限范围的数字字段。 */
function asNumber(value: unknown): number | undefined { const number = typeof value === 'number' ? value : typeof value === 'string' && value.trim() !== '' ? Number(value) : NaN; return Number.isFinite(number) ? number : undefined; }

/** 把后端 Unix 秒边界格式化成本机表单使用的分钟文本。 */
function unixLocal(value: unknown): string { const unix = asNumber(value); return unix === undefined ? '' : localMinute(new Date(unix * 1000)); }

/** 输出不依赖浏览器时区的本地分钟格式。 */
function localMinute(date: Date): string { const pad = (value: number) => String(value).padStart(2, '0'); return `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`; }
