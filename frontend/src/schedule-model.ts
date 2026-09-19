import { ScheduleError, type CreateScheduleRequest, type Schedule, type ScheduleBounds, type ScheduleDraft, type ScheduleField, type ScheduleType } from './contracts';

export const scheduleTypes: Record<ScheduleType, string> = { single: '单次', daily: '每天', weekly: '每周', monthly: '每月' };

const weekdayLabels = ['日', '一', '二', '三', '四', '五', '六'];

/** 将数字格式化为表单控件使用的两位数文本。 */
function pad(value: number): string { return String(value).padStart(2, '0'); }

/** 返回本地日期文本，作为重复日程的隐藏生效日期。 */
export function localDate(date: Date): string { return localMinute(date).slice(0, 10); }

/** 从重复字段构造本地首次时间，供类型切换和提交共用。 */
function recurringStart(draft: ScheduleDraft): Date | null {
  if (!parseLocal(draft.effectiveDate, true)) return null;
  const hour = Number(draft.hour); const minute = Number(draft.minute);
  if (!Number.isInteger(hour) || hour < 0 || hour > 23 || !Number.isInteger(minute) || minute < 0 || minute > 59) return null;
  return parseLocal(`${draft.effectiveDate}T${pad(hour)}:${pad(minute)}`);
}

/** 从已有草稿提取稳定的时间和持续时长，避免切换重复类型丢失输入。 */
function draftTiming(draft: ScheduleDraft): { start: Date | null; duration: number } {
  const recurring = recurringStart(draft);
  if (draft.type !== 'single' && recurring) {
    const duration = Number(draft.durationMinutes);
    return { start: recurring, duration: Number.isInteger(duration) && duration > 0 ? duration : 1 };
  }
  const start = parseLocal(draft.start); const end = parseLocal(draft.end);
  const duration = start && end && end > start ? (end.getTime() - start.getTime()) / 60000 : 1;
  return { start, duration: Number.isInteger(duration) && duration > 0 ? duration : 1 };
}

/** 按当前草稿推导目标重复类型的字段，保留标题、备注和用户已输入的时间。 */
export function changeDraftType(draft: ScheduleDraft, type: ScheduleType): ScheduleDraft {
  if (draft.type === type) return draft;
  const timing = draftTiming(draft);
  const start = timing.start;
  const effectiveDate = start && type !== 'single' ? localDate(start) : draft.effectiveDate || localDate(new Date());
  const next: ScheduleDraft = { ...draft, type, effectiveDate, durationMinutes: String(timing.duration), until: '' };
  if (start) {
    next.hour = pad(start.getHours()); next.minute = pad(start.getMinutes());
    next.monthDay = String(start.getDate());
    if (!next.weekdays.length) next.weekdays = [start.getDay()];
    if (type === 'single') {
      next.start = localMinute(start);
      next.end = localMinute(new Date(start.getTime() + timing.duration * 60000));
    }
  }
  return next;
}

/** 创建一份空白草稿，避免在用户未选择时间时默默提交默认值。 */
export function emptyDraft(): ScheduleDraft { return { title: '', notes: '', start: '', end: '', type: 'single', until: '', effectiveDate: localDate(new Date()), hour: '', minute: '', weekdays: [], monthDay: '', durationMinutes: '1' }; }

/** 使用本机墙钟格式化分钟，避免 ISO UTC 转换造成日期偏移。 */
export function localMinute(date: Date): string {
  const pad = (value: number) => String(value).padStart(2, '0');
  return `${String(date.getFullYear()).padStart(4, '0')}-${pad(date.getMonth() + 1)}-${pad(date.getDate())}T${pad(date.getHours())}:${pad(date.getMinutes())}`;
}

/** 严格解析本地日期，拒绝 JavaScript 自动纠正出的无效日期。 */
export function parseLocal(value: string, dateOnly = false): Date | null {
  const pattern = dateOnly ? /^\d{4}-\d{2}-\d{2}$/ : /^\d{4}-\d{2}-\d{2}T\d{2}:\d{2}$/;
  if (!pattern.test(value) || Number(value.slice(0, 4)) < 1) return null;
  const date = new Date(dateOnly ? `${value}T12:00` : value);
  return Number.isFinite(date.getTime()) && localMinute(date).slice(0, dateOnly ? 10 : 16) === value ? date : null;
}

/** 计算当前日历年的单次日程边界，结束时间允许恰好等于上界。 */
export function timeBounds(now: Date): ScheduleBounds {
  const min = new Date(now); min.setHours(0, 0, 0, 0);
  const max = new Date(min); const month = max.getMonth(); max.setFullYear(max.getFullYear() + 1);
  if (max.getMonth() !== month) max.setDate(0);
  return { min: localMinute(min), startMaxExclusive: localMinute(max), endMaxInclusive: localMinute(max) };
}

/** 将表单草稿转换为后端使用的整数秒请求，并集中呈现字段错误。 */
export function draftRequest(draft: ScheduleDraft): CreateScheduleRequest {
  const fields: Partial<Record<ScheduleField, string>> = {};
  if (!draft.title.trim()) fields.title = '请填写日程标题。';
  const start = parseLocal(draft.start); const end = parseLocal(draft.end);
  if (draft.type === 'single' && !start) fields.start = '请选择真实的开始日期和时间，精确到分钟。';
  if (draft.type === 'single' && !end) fields.end = '请选择真实的结束日期和时间，精确到分钟。';
  if (draft.type === 'single' && start && end && end <= start) fields.end = '结束时间必须晚于开始时间。';
  let recurring: Date | null = null;
  if (draft.type !== 'single') {
    recurring = recurringStart(draft);
    if (!recurring) {
      fields.hour = '请选择有效的 24 小时时间。';
      fields.minute = '请选择有效的 24 小时时间。';
    }
    const duration = Number(draft.durationMinutes);
    if (!Number.isInteger(duration) || duration <= 0) fields.durationMinutes = '持续分钟数必须是正整数。';
    if (draft.type === 'weekly' && !draft.weekdays.length) fields.weekdays = '请至少选择一个星期。';
    if (draft.type === 'monthly' && (!/^\d+$/.test(draft.monthDay) || Number(draft.monthDay) < 1 || Number(draft.monthDay) > 31)) fields.monthDay = '请选择每月 1–31 日。';
  }
  if (Object.keys(fields).length) throw new ScheduleError('VALIDATION', '请检查标出的字段。', fields);
  const requestStart = draft.type === 'single' ? start! : recurring!;
  const requestEnd = draft.type === 'single' ? end! : new Date(requestStart.getTime() + Number(draft.durationMinutes) * 60000);
  const request: CreateScheduleRequest = { title: draft.title.trim(), notes: draft.notes, start_unix: requestStart.getTime() / 1000, end_unix: requestEnd.getTime() / 1000 };
  if (draft.type !== 'single') {
    const weekdayPart = draft.weekdays.slice().sort((a, b) => a - b).join(',');
    request.cron = `${requestStart.getMinutes()} ${requestStart.getHours()} ${draft.type === 'monthly' ? Number(draft.monthDay) : '*'} * ${draft.type === 'weekly' ? weekdayPart : '*'}`;
  }
  return request;
}

/** 只接受当前后端支持的日、周、月五字段规则。 */
export function cronType(cron: string | null): ScheduleType {
  if (cron === null) return 'single';
  const p = cron.trim().split(/\s+/);
  if (p.length !== 5 || !/^\d+$/.test(p[0]) || +p[0] > 59 || !/^\d+$/.test(p[1]) || +p[1] > 23 || p[3] !== '*'
    || (p[2] !== '*' && (!/^\d+$/.test(p[2]) || +p[2] < 1 || +p[2] > 31 || p[4] !== '*'))
    || (p[4] !== '*' && (!p[4].split(',').every(value => /^\d+$/.test(value) && +value <= 6) || new Set(p[4].split(',')).size !== p[4].split(',').length))) throw new ScheduleError('VALIDATION', '重复规则不受支持。', { type: '请选择单次、每天、每周或每月。' });
  return p[2] !== '*' ? 'monthly' : p[4] !== '*' ? 'weekly' : 'daily';
}

/** 在提交边界重新校验单次日程时间范围，不信任页面预校验。 */
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
  const type = request.cron === undefined ? 'single' : cronType(request.cron);
  let untilDateLocal: string | null = null;
  if (type === 'single') {
    if (request.until_unix !== undefined) throw new ScheduleError('VALIDATION', '单次日程不能携带重复截止。', { until: '请移除重复截止。' });
    const bounds = timeBounds(now);
    if (start < new Date(bounds.min) || start >= new Date(bounds.startMaxExclusive)) fields.start = '超过时间限制，请按最新范围调整开始时间。';
    if (end < new Date(bounds.min) || end > new Date(bounds.endMaxInclusive)) fields.end = '超过时间限制，请按最新范围调整结束时间。';
    if (Object.keys(fields).length) throw new ScheduleError('TIME_RANGE_EXCEEDED', '超过时间限制', fields, bounds);
  } else if (request.until_unix !== undefined) {
    const until = new Date(Number(request.until_unix) * 1000);
    if (!Number.isSafeInteger(request.until_unix) || !Number.isFinite(until.getTime()) || until.getFullYear() < 1 || until.getFullYear() > 9999) throw new ScheduleError('VALIDATION', '重复截止日期无效。', { until: '请选择真实的重复截止日期。' });
    untilDateLocal = localMinute(until).slice(0, 10);
    if (untilDateLocal < localMinute(start).slice(0, 10)) throw new ScheduleError('VALIDATION', '截止日期不能早于首次开始日期。', { until: '截止日期不能早于首次开始日期。' });
  }
  return { title: request.title.trim(), notes: request.notes, firstStartLocal: localMinute(start), durationMinutes: (request.end_unix - request.start_unix) / 60, cron: request.cron ?? null, untilDateLocal, monthEndPolicy: type === 'monthly' ? 'clamp' : null, open: true };
}

/** 将后端定义转换为简洁的列表摘要。 */
export function scheduleSummary(schedule: Schedule): string {
  const type = cronType(schedule.cron); const start = new Date(schedule.firstStartLocal); const end = new Date(start.getTime() + schedule.durationMinutes * 60000); const fields = schedule.cron?.trim().split(/\s+/);
  const rule = type === 'weekly' ? `每周${fields![4].split(',').map(value => weekdayLabels[Number(value)]).join('、')}` : type === 'monthly' ? `每月 ${fields![2]} 日` : scheduleTypes[type];
  const endText = localMinute(end).slice(0, 10) === schedule.firstStartLocal.slice(0, 10) ? localMinute(end).slice(11) : localMinute(end).replace('T', ' ');
  return `${rule} · ${schedule.firstStartLocal.replace('T', ' ')}–${endText}${schedule.untilDateLocal ? ` · 截止 ${schedule.untilDateLocal}` : ''}${type === 'monthly' ? ' · 短月取月末' : ''}`;
}
