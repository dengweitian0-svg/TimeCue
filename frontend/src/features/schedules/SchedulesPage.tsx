import { useState, type FormEvent } from 'react';
import { ScheduleError } from '../../shared/contracts/errors';
import { DateField } from '../../shared/fields/DateField';
import { FieldError } from '../../shared/fields/FieldError';
import { SelectField } from '../../shared/fields/SelectField';
import { LoadingCard } from '../../shared/components/LoadingCard';
import type { Schedule, ScheduleActionKind, ScheduleActionState, ScheduleBounds, ScheduleDraft, ScheduleField, SchedulePage, ScheduleType } from './contracts';
import { draftRequest, localMinute, scheduleSummary, scheduleTypes } from './model';

/** 日程页面所需的最小操作集合，Controller 通过结构类型满足该接口。 */
export interface ScheduleActions {
  beginCreate(): Promise<void>;
  refreshSchedules(): Promise<void>;
  nextPage(): Promise<void>;
  askDelete(schedule: Schedule): void;
  askScheduleAction(kind: ScheduleActionKind, schedule: Schedule): void;
  editDraft(patch: Partial<ScheduleDraft>): void;
  changeDraftType(type: ScheduleType): void;
  cancelCreate(): void;
  saveCreate(): Promise<void>;
  cancelDelete(): void;
  confirmDelete(): Promise<void>;
  recoverDelete(): Promise<void>;
  cancelScheduleAction(): void;
  confirmScheduleAction(): Promise<void>;
  recoverScheduleAction(): Promise<void>;
}

/** 日程页面所需的状态投影，不暴露提醒和设置领域状态。 */
export interface ScheduleViewState {
  schedules: SchedulePage;
  bounds: ScheduleBounds | null;
  draft: ScheduleDraft | null;
  deleting: Schedule | null;
  scheduleAction: ScheduleActionState | null;
  busy: boolean;
  loading: boolean;
  error: ScheduleError | null;
}

/** 呈现日程定义列表、创建入口和日程动作弹窗。 */
export function SchedulesPage({ actions, state }: { actions: ScheduleActions; state: ScheduleViewState }) {
  if (state.draft) return <ScheduleForm actions={actions} state={state} />;
  return <section className="page-section"><div className="page-heading"><div><p className="kicker">SCHEDULES</p><h1>我的日程</h1><p>给重要的小事，留出一段完整的时间。</p></div><button className="primary-button" onClick={() => void actions.beginCreate()}>＋ 添加日程</button></div>
    <div className="list-toolbar"><span>{state.loading ? '正在读取…' : `${state.schedules.items.length} 个日程`}</span><button className="text-button" onClick={() => void actions.refreshSchedules()} disabled={state.loading}>重新加载</button></div>
    {state.schedules.items.length ? <div className="schedule-list">{state.schedules.items.map(item => <ScheduleCard key={item.id} schedule={item} onDelete={() => actions.askDelete(item)} onScheduleAction={kind => actions.askScheduleAction(kind, item)} />)}</div> : state.loading ? <LoadingCard /> : <EmptySchedules onCreate={() => void actions.beginCreate()} />}
    {state.schedules.nextCursor && <button className="secondary-button pagination" onClick={() => void actions.nextPage()}>加载更多</button>}
    {state.deleting && <DeleteDialog actions={actions} item={state.deleting} busy={state.busy} error={state.error} />}
    {state.scheduleAction && <ScheduleActionDialog actions={actions} action={state.scheduleAction} busy={state.busy} error={state.error} />}
  </section>;
}

/** 显示一条日程定义、重复提醒状态和永久删除动作。 */
function ScheduleCard({ schedule, onDelete, onScheduleAction }: { schedule: Schedule; onDelete: () => void; onScheduleAction: (kind: ScheduleActionKind) => void }) {
  const recurring = Boolean(schedule.cron);
  const cancelled = recurring && !schedule.open;
  return <article className={`schedule-card${cancelled ? ' cancelled' : ''}`}><div className="schedule-accent" /><div className="schedule-card-body"><div className="schedule-card-head"><div><div className="schedule-type-row"><p className="schedule-type">{recurring ? '重复日程' : '单次日程'}</p>{cancelled && <span className="schedule-status">提醒已取消</span>}</div><h2>{schedule.title}</h2></div><div className="schedule-card-actions">{recurring && <button className="text-button schedule-action-button" aria-label={`${cancelled ? '恢复' : '取消'} ${schedule.title} 的提醒`} onClick={() => onScheduleAction(cancelled ? 'restore' : 'cancel')}>{cancelled ? '恢复提醒' : '取消提醒'}</button>}<button className="icon-button delete" aria-label={`删除 ${schedule.title}`} onClick={onDelete}>×</button></div></div><p className="schedule-summary">{scheduleSummary(schedule)}</p>{schedule.notes && <p className="schedule-notes">{schedule.notes}</p>}</div></article>;
}

/** 创建页面只持有输入，最终校验与提交由 Controller 完成。 */
function ScheduleForm({ actions, state }: { actions: ScheduleActions; state: ScheduleViewState }) {
  const draft = state.draft!;
  const [localError, setLocalError] = useState<ScheduleError | null>(null);
  const error = localError ?? state.error;

  /** 先运行本地纯模型校验，以便表单即时显示字段错误。 */
  function submit(event: FormEvent) {
    event.preventDefault();
    try {
      draftRequest(draft);
      setLocalError(null);
      void actions.saveCreate();
    } catch (failure) {
      setLocalError(failure instanceof ScheduleError ? failure : null);
    }
  }

  /** 读取字段错误并建立控件可访问性关联。 */
  function field(name: ScheduleField) {
    const message = error?.fields[name];
    return { 'aria-invalid': Boolean(message), 'aria-describedby': message ? `error-${name}` : undefined };
  }

  return <form className="page-section create-form" onSubmit={submit} noValidate><div className="page-heading compact"><div><p className="kicker">NEW SCHEDULE</p><h1>添加日程</h1><p>把想做的事，轻轻记下来。</p></div><button type="button" className="text-button" onClick={() => actions.cancelCreate()}>取消</button></div>
    <div className="form-card"><label>标题 <span className="required-mark">*</span><input autoFocus value={draft.title} placeholder="例如：读完一本喜欢的书" {...field('title')} onChange={event => { setLocalError(null); actions.editDraft({ title: event.target.value }); }} /></label><FieldError error={error} name="title" />
      <label>备注 <small>选填</small><textarea value={draft.notes} placeholder="写点想提醒自己的话" onChange={event => actions.editDraft({ notes: event.target.value })} /></label>
      <fieldset><legend>重复方式</legend><div className="type-options">{(Object.keys(scheduleTypes) as ScheduleType[]).map(type => <button type="button" key={type} className={`type-option ${draft.type === type ? 'selected' : ''}`} aria-pressed={draft.type === type} onClick={() => actions.changeDraftType(type)}>{scheduleTypes[type]}</button>)}</div></fieldset><FieldError error={error} name="type" />
      {draft.type === 'single' ? <><div className="form-grid"><DateField label="开始时间" value={draft.start} error={error} name="start" min={state.bounds?.min} max={state.bounds ? localMinute(new Date(new Date(state.bounds.startMaxExclusive).getTime() - 60000)) : undefined} onChange={start => actions.editDraft({ start })} /><DateField label="结束时间" value={draft.end} error={error} name="end" min={draft.start || state.bounds?.min} max={state.bounds?.endMaxInclusive} onChange={end => actions.editDraft({ end })} /></div>{state.bounds && <p className="field-hint">可选范围：{state.bounds.min.replace('T', ' ')} 起，结束不晚于 {state.bounds.endMaxInclusive.replace('T', ' ')}。</p>}</> : <RecurringFields actions={actions} draft={draft} error={error} />}
      <div className="form-actions"><button type="button" className="secondary-button" onClick={() => actions.cancelCreate()}>取消</button><button type="submit" className="primary-button" disabled={state.busy}>{state.busy ? '正在保存…' : '保存日程'}</button></div>
    </div></form>;
}

/** 按重复类型展示时间、持续时长和日期规则，不暴露单次端点或截止日期。 */
function RecurringFields({ actions, draft, error }: { actions: ScheduleActions; draft: ScheduleDraft; error: ScheduleError | null }) {
  const hours = Array.from({ length: 24 }, (_, index) => padOption(index));
  const minutes = Array.from({ length: 60 }, (_, index) => padOption(index));
  return <div className="recurring-fields"><input type="hidden" value={draft.effectiveDate} readOnly aria-label="重复生效日期" />
    <div className="form-grid"><SelectField label="小时" name="hour" value={draft.hour} error={error} options={hours} onChange={hour => actions.editDraft({ hour })} /><SelectField label="分钟" name="minute" value={draft.minute} error={error} options={minutes} onChange={minute => actions.editDraft({ minute })} /></div>
    {draft.type === 'weekly' && <WeekdayField actions={actions} draft={draft} error={error} />}
    {draft.type === 'monthly' && <SelectField label="每月日期" name="monthDay" value={draft.monthDay} error={error} options={Array.from({ length: 31 }, (_, index) => String(index + 1))} onChange={monthDay => actions.editDraft({ monthDay })} />}
    <label>持续分钟数 <span className="required-mark">*</span><input type="number" min="1" step="1" value={draft.durationMinutes} aria-invalid={Boolean(error?.fields.durationMinutes)} onChange={event => actions.editDraft({ durationMinutes: event.target.value })} />{error?.fields.durationMinutes && <span className="field-error">{error.fields.durationMinutes}</span>}</label>
    <p className="field-hint">按当前本地日期（{draft.effectiveDate}）开始计算；如果今天的时间已过去，将从下一次匹配开始，不补发历史提醒。{draft.type === 'monthly' ? '短月没有该日期时取当月最后一天。' : ''}</p>
  </div>;
}

/** 以按钮组呈现周一至周日多选，星期值直接对应 cron 的 0–6。 */
function WeekdayField({ actions, draft, error }: { actions: ScheduleActions; draft: ScheduleDraft; error: ScheduleError | null }) {
  const weekdays = [{ label: '一', value: 1 }, { label: '二', value: 2 }, { label: '三', value: 3 }, { label: '四', value: 4 }, { label: '五', value: 5 }, { label: '六', value: 6 }, { label: '日', value: 0 }];
  return <fieldset><legend>星期 <span className="required-mark">*</span></legend><div className="weekday-options">{weekdays.map(({ label, value }) => { const selected = draft.weekdays.includes(value); return <button type="button" key={value} className={`weekday-option${selected ? ' selected' : ''}`} aria-pressed={selected} onClick={() => actions.editDraft({ weekdays: selected ? draft.weekdays.filter(day => day !== value) : [...draft.weekdays, value].sort((a, b) => a - b) })}>周{label}</button>; })}</div><FieldError error={error} name="weekdays" /></fieldset>;
}

/** 输出带两位数字的原生下拉选项。 */
function padOption(value: number): string { return String(value).padStart(2, '0'); }

/** 删除确认对话框支持并发失败后的重新读取路径。 */
function DeleteDialog({ actions, item, busy, error }: { actions: ScheduleActions; item: Schedule; busy: boolean; error: ScheduleError | null }) {
  const recovery = error?.code === 'STALE_REVISION' || error?.code === 'NOT_FOUND';
  return <div className="dialog-backdrop" role="presentation"><section className="dialog" role="dialog" aria-modal="true" aria-labelledby="delete-title"><p className="kicker">DELETE SCHEDULE</p><h2 id="delete-title">{recovery ? '日程状态已变化' : '删除这条日程？'}</h2><p><strong>{item.title}</strong></p><p className="dialog-copy">{recovery ? error?.message : '删除后无法恢复，也不会再产生这条日程的提醒。'}</p><div className="form-actions"><button className="secondary-button" disabled={busy} onClick={() => actions.cancelDelete()}>保留日程</button>{recovery ? <button className="primary-button" disabled={busy} onClick={() => void actions.recoverDelete()}>{error?.code === 'NOT_FOUND' ? '刷新列表' : '查看最新内容'}</button> : <button className="danger-button" disabled={busy} onClick={() => void actions.confirmDelete()}>永久删除</button>}</div></section></div>;
}

/** 为重复日程提供取消或恢复提醒的确认和并发恢复入口。 */
function ScheduleActionDialog({ actions, action, busy, error }: { actions: ScheduleActions; action: ScheduleActionState; busy: boolean; error: ScheduleError | null }) {
  const restoring = action.kind === 'restore';
  const recovery = error?.code === 'STALE_REVISION' || error?.code === 'NOT_FOUND';
  return <div className="dialog-backdrop" role="presentation"><section className="dialog" role="dialog" aria-modal="true" aria-labelledby="schedule-action-title"><p className="kicker">{restoring ? 'RESTORE REMINDER' : 'CANCEL REMINDER'}</p><h2 id="schedule-action-title">{recovery ? '日程状态已变化' : restoring ? '恢复这条日程的提醒？' : '取消这条日程的提醒？'}</h2><p><strong>{action.item.title}</strong></p><p className="dialog-copy">{recovery ? error?.message : restoring ? '恢复后将继续按原重复规则提醒。' : '取消后会保留日程、重复规则和历史数据，但不会再产生后续提醒。'}</p><div className="form-actions"><button className="secondary-button" disabled={busy} onClick={() => actions.cancelScheduleAction()}>保留日程</button>{recovery ? <button className="primary-button" disabled={busy} onClick={() => void actions.recoverScheduleAction()}>{error?.code === 'NOT_FOUND' ? '刷新列表' : '查看最新内容'}</button> : <button className="primary-button" disabled={busy} onClick={() => void actions.confirmScheduleAction()}>{busy ? '正在保存…' : restoring ? '恢复提醒' : '取消提醒'}</button>}</div></section></div>;
}

/** 空列表的友好引导。 */
function EmptySchedules({ onCreate }: { onCreate: () => void }) { return <div className="empty-card"><span className="empty-mark">✦</span><h2>还没有日程</h2><p>从一个小小的安排开始，让时间为你留出空间。</p><button className="secondary-button" onClick={onCreate}>添加第一个日程</button></div>; }
