import type { Activation, ActivationListener, AppPort, CloseBehavior, CreateScheduleRequest, FrontendStatus, RuntimeStatus, Schedule, ScheduleBounds, ScheduleListRequest, SchedulePage } from './contracts';

/** 可注入的 Go bindings 端口；生成文件只在生产启动时接入，测试不依赖其存在。 */
export interface BindingsPort {
  ListSchedules(request: ScheduleListRequest): Promise<unknown>;
  GetSchedule(id: number | string): Promise<unknown>;
  GetScheduleTimeBounds(): Promise<unknown>;
  CreateSchedule(payload: string): Promise<unknown>;
  DeleteSchedule(id: number, payload: string): Promise<unknown>;
  GetRuntimeStatus(): Promise<unknown>;
  GetDesktopStatus(): Promise<unknown>;
  GetCloseBehavior(): Promise<unknown>;
  SaveCloseBehavior(request: { behavior: CloseBehavior }): Promise<unknown>;
  FrontendReady(request: { last_sequence: number }): Promise<unknown>;
  RetryCleanup(): Promise<unknown>;
}

/** Wails runtime EventsOn 的最小端口，便于在绑定文件生成前做契约测试。 */
export type EventsOn = (name: string, listener: (...args: unknown[]) => void) => void | (() => void);

export const ACTIVATION_EVENT = 'activation';
const BINDINGS_MODULE = '../wailsjs/go/runtime/WailsBindings';
const RUNTIME_MODULE = '../wailsjs/runtime/runtime';

/** 将 Wails 生成模块包成生产前端需要的类型安全端口。 */
export class WailsAdapter implements AppPort {
  private readonly activationListeners = new Set<ActivationListener>();
  private readonly unsubscribeEvents: (() => void) | null;

  constructor(private readonly bindings: BindingsPort, eventsOn?: EventsOn) {
    this.unsubscribeEvents = eventsOn ? normalizeUnsubscribe(eventsOn(ACTIVATION_EVENT, (...args) => this.onActivation(args))) : null;
  }

  /** 释放事件监听，避免热更新或窗口重建后重复接收激活。 */
  dispose(): void { this.unsubscribeEvents?.(); this.activationListeners.clear(); }

  /** 注册激活监听并返回可重复调用的取消函数。 */
  subscribeActivation(listener: ActivationListener): () => void {
    this.activationListeners.add(listener);
    return () => this.activationListeners.delete(listener);
  }

  /** 将 Wails 事件参数转换为稳定的 sequence/target 结构。 */
  private onActivation(args: unknown[]): void {
    const activation = toActivation(args.length === 1 ? args[0] : args);
    if (!activation) return;
    for (const listener of this.activationListeners) listener(activation);
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

  /** 注册前端事件后向后端回报最后消费的激活序号。 */
  async frontendReady(lastSequence: number): Promise<Activation | null> {
    const raw = await this.bindings.FrontendReady({ last_sequence: lastSequence });
    return toActivation(read(asRecord(raw), 'activation', 'Activation') ?? raw);
  }

  /** 请求后端重试系统通知清理。 */
  async retryCleanup(): Promise<void> { await this.bindings.RetryCleanup(); }
}

/** 延迟加载正式生成的 Go bindings 和 Wails runtime，避免测试或生成阶段静态依赖文件。 */
export async function createWailsAdapter(): Promise<WailsAdapter> {
  const bindings = await import(/* @vite-ignore */ BINDINGS_MODULE) as unknown as BindingsPort;
  const runtime = await import(/* @vite-ignore */ RUNTIME_MODULE) as unknown as { EventsOn?: EventsOn };
  return new WailsAdapter(bindings, runtime.EventsOn);
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

/** 将运行状态 DTO 映射为受控枚举。 */
function toRuntime(value: unknown): RuntimeStatus {
  const item = asRecord(value);
  const lifecycle = read(item, 'lifecycle', 'Lifecycle', 'state', 'State');
  return { visibility: enumValue(read(item, 'visibility', 'Visibility'), ['visible', 'hidden', 'minimized'], 'visible'), lifecycle: enumValue(lifecycle === 'cleanup_failed' ? 'cleanup-failed' : lifecycle, ['running', 'stopping', 'stopped', 'cleanup-failed'], 'running'), ready: Boolean(read(item, 'ready', 'Ready', 'frontend_ready', 'FrontendReady')) } as RuntimeStatus;
}

/** 从后端枚举中选择受支持的值，并为损坏状态提供安全回退。 */
function enumValue<T extends string>(value: unknown, values: readonly T[], fallback: T): T { return typeof value === 'string' && values.includes(value as T) ? value as T : fallback; }

/** 将桌面状态 DTO 映射为统一的状态快照。 */
function toDesktopStatus(value: unknown): FrontendStatus {
  const item = asRecord(value); const runtime = read(item, 'runtime', 'Runtime');
  const activation = toActivation(read(item, 'activation', 'Activation')); return { runtime: toRuntime(runtime ?? item), closeBehavior: toCloseBehavior(read(item, 'close_behavior', 'closeBehavior', 'CloseBehavior', 'behavior', 'Behavior')), activation };
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
