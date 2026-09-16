import { ScheduleError, type CreateScheduleRequest, type Schedule, type ScheduleBounds, type ScheduleDraft, type ScheduleField, type ScheduleType } from './contracts';

export const scheduleTypes: Record<ScheduleType, string> = { single: '单次', daily: '每天', weekly: '每周', monthly: '每月' };

/** 初次创建不猜测时间或重复截止，避免静默保存不符合用户意图的安排。 */
export function emptyDraft(): ScheduleDraft { return { title: '', notes: '', start: '', end: '', type: 'single', until: '' }; }

/** 输出本机墙钟分钟文本，避免 ISO UTC 转换导致日期偏移。 */
export function localMinute(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** 严格验证本地日期，不接受 JavaScript 自动纠正的 2 月 30 日或不存在的墙钟时刻。 */
export function parseLocal(value: string, dateOnly = false): Date | null {
  const pattern = dateOnly ? /^\d{4}-\d{2}-\d{2}$/ : /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/;
  if (!pattern.test(value) || Number(value.slice(0, 4)) < 1) return null;
  const date = new Date(dateOnly ? `${value}T12:00` : value);
  return Number.isFinite(date.getTime()) && localMinute(date).slice(0, dateOnly ? 10 : 16) === value ? date : null;
}

/** 复现当前 Go 的日历年夹取：开始排除上界，结束允许恰好落在上界。 */
export function timeBounds(now: Date): ScheduleBounds {
  const min = new Date(now); min.setHours(0, 0, 0, 0);
  const max = new Date(min); const month = max.getMonth(); max.setFullYear(max.getFullYear() + 1);
  if (max.getMonth() !== month) max.setDate(0);
  return { min: localMinute(min), startMaxExclusive: localMinute(max), endMaxInclusive: localMinute(max) };
}

/** 将表单转成唯一的整数秒起止事实，重复类型只在界面保留。 */
export function draftRequest(draft: ScheduleDraft): CreateScheduleRequest {
  const fields: Partial<Record<ScheduleField, string>> = {};
  if (!draft.title.trim()) fields.title = '请填写日程标题。';
  const start = parseLocal(draft.start); const end = parseLocal(draft.end);
  if (!start) fields.start = '请选择真实的开始日期和时间，精确到分钟。';
  if (!end) fields.end = '请选择真实的结束日期和时间，精确到分钟。';
  if (start && end && end <= start) fields.end = '结束时间必须晚于开始时间。';
  const until = draft.type === 'single' ? null : parseLocal(draft.until, true);
  if (draft.type !== 'single' && !until) fields.until = '请选择重复截止日期。';
  if (draft.type !== 'single' && until && draft.until < draft.start.slice(0, 10)) fields.until = '截止日期不能早于首次开始日期。';
  if (Object.keys(fields).length) throw new ScheduleError('VALIDATION', '请检查标出的字段。', fields);
  const request: CreateScheduleRequest = { title: draft.title.trim(), notes: draft.notes, start_unix: start!.getTime() / 1000, end_unix: end!.getTime() / 1000 };
  if (draft.type !== 'single') {
    request.cron = `${start!.getMinutes()} ${start!.getHours()} ${draft.type === 'monthly' ? start!.getDate() : '*'} * ${draft.type === 'weekly' ? start!.getDay() : '*'}`;
    // 截止传本地中午的整数秒，应用边界只取该时区的日期，不依赖午夜是否存在。
    request.until_unix = until!.getTime() / 1000;
  }
  return request;
}

/** 只接受后端首版支持的日、周、月五字段规则，拒绝步长、列表与混合日期。 */
export function cronType(cron: string | null): ScheduleType {
  if (cron === null) return 'single';
  const p = cron.trim().split(/\s+/);
  if (p.length !== 5 || !/^\d+$/.test(p[0]) || +p[0] > 59 || !/^\d+$/.test(p[1]) || +p[1] > 23 || p[3] !== '*'
    || (p[2] !== '*' && (!/^\d+$/.test(p[2]) || +p[2] < 1 || +p[2] > 31 || p[4] !== '*'))
    || (p[4] !== '*' && (!/^\d+$/.test(p[4]) || +p[4] > 6))) throw new ScheduleError('VALIDATION', '重复规则不受支持。', { type: '请选择单次、每天、每周或每月。' });
  return p[2] !== '*' ? 'monthly' : p[4] !== '*' ? 'weekly' : 'daily';
}

/** 在适配器写入前重新裁决字段与最新时间范围，不信任页面的预校验结果。 */
export function validateSchedule(request: CreateScheduleRequest, now: Date): Omit<Schedule, 'id' | 'revision'> {
  const fields: Partial<Record<ScheduleField, string>> = {};
  if (typeof request.title !== 'string' || !request.title.trim()) fields.title = '请填写日程标题。';
  if (typeof request.notes !== 'string') throw new ScheduleError('VALIDATION', '备注格式不正确。');
  const start = new Date(request.start_unix * 1000); const end = new Date(request.end_unix * 1000);
  for (const [key, unix, date] of [['start', request.start_unix, start], ['end', request.end_unix, end]] as const) {
    if (!Number.isSafeInteger(unix) || unix % 60 !== 0 || !Number.isFinite(date.getTime()) || date.getFullYear() < 1 || date.getFullYear() > 9999) fields[key] = '请选择真实日期，精确到分钟。';
  }
  if (end <= start) fields.end = '结束时间必须晚于开始时间。';
  if (Object.keys(fields).length) throw new ScheduleError('VALIDATION', '请检查标出的字段。', fields);
  if (request.cron !== undefined && typeof request.cron !== 'string') throw new ScheduleError('VALIDATION', '重复规则格式不正确。', { type: '单次日程应省略重复规则。' });
  const type = request.cron === undefined ? 'single' : cronType(request.cron);
  let untilDateLocal: string | null = null;
  if (type === 'single') {
    if (request.until_unix !== undefined) throw new ScheduleError('VALIDATION', '单次日程不能携带重复截止。', { until: '请移除重复截止。' });
    const bounds = timeBounds(now);
    if (start < new Date(bounds.min) || start >= new Date(bounds.startMaxExclusive)) fields.start = '超过时间限制，请按最新范围调整开始时间。';
    if (end < new Date(bounds.min) || end > new Date(bounds.endMaxInclusive)) fields.end = '超过时间限制，请按最新范围调整结束时间。';
    if (Object.keys(fields).length) throw new ScheduleError('TIME_RANGE_EXCEEDED', '超过时间限制', fields, bounds);
  } else {
    // 对齐 Go time.Duration 可承载的重复时长，避免接受真实后端无法保存的定义。
    if ((request.end_unix - request.start_unix) / 60 > Number((2n ** 63n - 1n) / 60000000000n)) throw new ScheduleError('VALIDATION', '重复日程持续时间过长。', { end: '请缩短日程持续时间。' });
    const until = new Date(Number(request.until_unix) * 1000);
    if (!Number.isSafeInteger(request.until_unix) || !Number.isFinite(until.getTime()) || until.getFullYear() < 1 || until.getFullYear() > 9999) throw new ScheduleError('VALIDATION', '请选择重复截止日期。', { until: '请选择真实的重复截止日期。' });
    untilDateLocal = localMinute(until).slice(0, 10);
    if (untilDateLocal < localMinute(start).slice(0, 10)) throw new ScheduleError('VALIDATION', '截止日期不能早于首次开始日期。', { until: '截止日期不能早于首次开始日期。' });
  }
  return { title: request.title.trim(), notes: request.notes, firstStartLocal: localMinute(start), durationMinutes: (request.end_unix - request.start_unix) / 60, cron: request.cron ?? null, untilDateLocal, monthEndPolicy: type === 'monthly' ? 'clamp' : null };
}

/** 展示保存的定义而非待处理消息，不预展开或制造发生记录。 */
export function scheduleSummary(schedule: Schedule): string {
  const type = cronType(schedule.cron); const start = new Date(schedule.firstStartLocal);
  const end = new Date(start.getTime() + schedule.durationMinutes * 60000);
  const fields = schedule.cron?.trim().split(/\s+/);
  const rule = type === 'weekly' ? `每周${'日一二三四五六'[Number(fields![4])]}` : type === 'monthly' ? `每月 ${fields![2]} 日` : scheduleTypes[type];
  return `${rule} · ${schedule.firstStartLocal.replace('T', ' ')}–${localMinute(end).slice(0, 10) === schedule.firstStartLocal.slice(0, 10) ? localMinute(end).slice(11) : localMinute(end).replace('T', ' ')}${schedule.untilDateLocal ? ` · 截止 ${schedule.untilDateLocal}` : ''}${type === 'monthly' ? ' · 短月取月末' : ''}`;
}
