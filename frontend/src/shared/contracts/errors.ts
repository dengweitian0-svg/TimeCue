/** 结构化字段错误，允许 shared 层不依赖具体业务字段联合类型。 */
export type FieldErrors = Partial<Record<string, string>>;

/** 错误中可选的时间边界投影。 */
export interface ErrorBounds {
  min: string;
  startMaxExclusive: string;
  endMaxInclusive: string;
}

/** 前端统一识别的业务错误码。 */
export type ScheduleErrorCode = 'VALIDATION' | 'TIME_RANGE_EXCEEDED' | 'STALE_REVISION' | 'STALE_CURSOR' | 'REMINDER_NOT_PROCESSABLE' | 'NOT_FOUND' | 'STORAGE' | 'RUNTIME_STOPPING';

/** 将端口拒绝值转换后的稳定业务错误。 */
export class ScheduleError extends Error {
  constructor(
    public readonly code: ScheduleErrorCode,
    message: string,
    public readonly fields: FieldErrors = {},
    public readonly bounds?: ErrorBounds,
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
  const message = readableErrorMessage(payload.message);
  const fields = toFieldMessages(payload.fields, message);
  const bounds = isRecord(payload.bounds) ? payload.bounds as unknown as ErrorBounds : undefined;
  return new ScheduleError(code, message, fields, bounds);
}

/** 将 Wails 的对象字符串和空文本收敛为用户可执行的稳定提示。 */
function readableErrorMessage(value: unknown): string {
  if (typeof value !== 'string') return '操作未完成，请重试。';
  const message = value.trim();
  if (!message || message.toLowerCase() === 'object' || message === '[object Object]') return '操作未完成，请重试。';
  return message;
}

/** 把后端字段名数组转为表单可显示的字段错误，同时兼容端口直接返回的映射。 */
function toFieldMessages(value: unknown, message: string): FieldErrors {
  const result: FieldErrors = {};
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
function mapBackendField(value: string): string | undefined {
  const fields: Record<string, string> = { title: 'title', start: 'start', start_unix: 'start', end: 'end', end_unix: 'end', type: 'type', cron: 'type', until: 'until', until_unix: 'until' };
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
function isScheduleCode(value: unknown): value is ScheduleErrorCode {
  return value === 'VALIDATION' || value === 'TIME_RANGE_EXCEEDED' || value === 'STALE_REVISION' || value === 'STALE_CURSOR' || value === 'REMINDER_NOT_PROCESSABLE' || value === 'NOT_FOUND' || value === 'STORAGE' || value === 'RUNTIME_STOPPING';
}
