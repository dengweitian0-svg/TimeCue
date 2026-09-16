import { useEffect, useRef, type FormEvent, type KeyboardEvent as ReactKeyboardEvent, type ReactNode } from 'react';
import { Button, Feedback, Mascot } from './components';
import type { AppState, Controller } from './controller';
import type { ScheduleField, ScheduleType } from './contracts';
import { localMinute, parseLocal, scheduleSummary, scheduleTypes } from './schedule-model';
import { DateTimeField } from './DateTimeField';

/** 展示定义列表或创建表单，日程操作全部交给控制器处理。 */
export function Schedules({ controller, state }: { controller: Controller; state: AppState }) {
  if (state.scheduleDraft) return <ScheduleForm controller={controller} state={state} />;
  return <section className="schedules-page" aria-label="我的日程">
    <header className="page-header"><h1>我的日程</h1><p>给每件小事留一点时间。</p></header>
    <div className="schedule-toolbar"><p>{state.schedulesLoading ? '正在读取日程…' : `${state.schedules.length} 个日程`}</p><Button className="schedule-add" onClick={() => controller.beginSchedule()}>＋ 添加日程</Button></div>
    {state.scheduleError && !state.deleting && <Feedback kind="error">{state.scheduleError.message}<Button secondary onClick={() => void controller.refreshSchedules()}>重新加载</Button></Feedback>}
    {state.scheduleNotice && <Feedback kind="success">{state.scheduleNotice}</Feedback>}
    {!state.schedules.length && !state.schedulesLoading ? <div className="schedule-empty"><Mascot size={104} /><h2>你还没有日程</h2><p>从一个小小的安排开始吧。</p></div> : <div className="schedule-list" aria-label="日程定义列表">
      {state.schedules.map(item => <article className="schedule-row" key={item.id} aria-label={item.title}><div className="schedule-card"><h2>{item.title}</h2><p>{scheduleSummary(item)}</p>{item.notes && <p className="schedule-notes">{item.notes}</p>}</div><Button secondary className="delete-entry" onClick={() => controller.askDelete(item)}>删除日程</Button></article>)}
    </div>}
    <Feedback>创建日程后，到提醒时间才会产生消息。查看提醒不会自动确认。</Feedback>
  </section>;
}

/** 将后端字段提示关联到对应输入，便于读屏与键盘定位。 */
function FieldError({ name, state }: { name: ScheduleField; state: AppState }) {
  const message = state.scheduleError?.fields[name];
  return message ? <span className="field-error" id={`schedule-${name}-error`}>{message}</span> : null;
}

/** 使用主题日期弹层与文本控件承接填写，单次边界来自适配器。 */
function ScheduleForm({ controller, state }: { controller: Controller; state: AppState }) {
  const draft = state.scheduleDraft!; const form = useRef<HTMLFormElement>(null);
  const start = parseLocal(draft.start); const type = draft.type;
  const repeatLabel = type === 'weekly' && start ? `每周${'日一二三四五六'[start.getDay()]}` : type === 'monthly' && start ? `每月 ${start.getDate()} 日` : scheduleTypes[type];
  /** 等待最终校验返回后聚焦首个错误，错误输入始终保留。 */
  async function submit(event: FormEvent) { event.preventDefault(); await controller.saveSchedule(); requestAnimationFrame(() => form.current?.querySelector<HTMLElement>('[aria-invalid="true"]')?.focus()); }
  /** 将字段名映射为可访问性属性。 */
  function invalid(name: ScheduleField) { return { 'aria-invalid': !!state.scheduleError?.fields[name], 'aria-describedby': state.scheduleError?.fields[name] ? `schedule-${name}-error` : undefined }; }
  useEffect(() => {
    /** Esc 与取消按钮走同一确认流程，等待中不丢弃提交。 */
    function escape(event: KeyboardEvent) { if (event.key === 'Escape' && !event.defaultPrevented && !state.discardTo) { event.preventDefault(); controller.cancelSchedule(); } }
    window.addEventListener('keydown', escape); return () => window.removeEventListener('keydown', escape);
  }, [controller, state.discardTo]);
  return <form className="schedule-form" ref={form} noValidate onSubmit={event => void submit(event)} aria-label="添加日程">
    <header className="page-header"><h1>添加日程</h1><p>把想做的事，轻轻记下来。</p></header>
    <fieldset disabled={state.scheduleBusy}>
      <label className="schedule-field" htmlFor="schedule-title">日程标题 *<input id="schedule-title" autoFocus value={draft.title} placeholder="例如：读完一本喜欢的书" {...invalid('title')} onChange={event => controller.editSchedule({ title: event.target.value })} /><FieldError name="title" state={state} /></label>
      <label className="schedule-field" htmlFor="schedule-notes">备注 · 选填<textarea id="schedule-notes" rows={1} value={draft.notes} placeholder="写点想提醒自己的话" onChange={event => controller.editSchedule({ notes: event.target.value })} /></label>
      <div className="schedule-types" role="group" aria-label="日程类型">{(Object.keys(scheduleTypes) as ScheduleType[]).map(key => <Button type="button" key={key} secondary={type !== key} aria-pressed={type === key} onClick={() => controller.editSchedule({ type: key })}>{scheduleTypes[key]}</Button>)}</div><FieldError name="type" state={state} />
      <div className="schedule-endpoints">{(['start', 'end'] as const).map(name => <div className="schedule-field" key={name}><label htmlFor={`schedule-${name}`}>{name === 'start' ? '开始时间 *' : '结束时间 *'}</label><DateTimeField id={`schedule-${name}`} label={name === 'start' ? '开始时间' : '结束时间'} value={draft[name]} min={type === 'single' ? state.scheduleBounds?.min : undefined} max={type === 'single' && state.scheduleBounds ? name === 'end' ? state.scheduleBounds.endMaxInclusive : localMinute(new Date(new Date(state.scheduleBounds.startMaxExclusive).getTime() - 60000)) : undefined} {...invalid(name)} onChange={value => controller.editSchedule({ [name]: value })} /><FieldError name={name} state={state} /></div>)}</div>
      <div className="schedule-rule"><p>{repeatLabel}{start ? ` · ${draft.start.slice(11)}` : ''}{type === 'single' ? ' · 仅提醒一次' : ' · 从首次开始日期生效'}</p>
        <p className="hint">{type === 'monthly' ? '短月取月末，例如 1/31 → 2/28 → 3/31；下月恢复原日期。' : type === 'single' ? '使用本机时区，精确到分钟。保存时会重新验证可选日期范围。' : '重复截止包含当天，不套用单次日程的一年限制。'}</p>
        {type !== 'single' ? <div className="schedule-field"><label htmlFor="schedule-until">重复截止日期 *</label><DateTimeField id="schedule-until" label="重复截止日期" dateOnly value={draft.until} min={parseLocal(draft.start) ? draft.start.slice(0, 10) : undefined} {...invalid('until')} onChange={value => controller.editSchedule({ until: value })} /><FieldError name="until" state={state} /></div> : state.scheduleBounds && <p className="schedule-bounds">可选范围：{state.scheduleBounds.min.replace('T', ' ')} 起；开始早于 {state.scheduleBounds.startMaxExclusive.replace('T', ' ')}，结束不晚于此时。</p>}
      </div>
      {state.scheduleError && <Feedback kind="error">{state.scheduleError.message}</Feedback>}
      <div className="schedule-actions"><Button type="button" secondary onClick={() => controller.cancelSchedule()}>取消</Button><Button type="submit" busy={state.scheduleBusy}>{state.scheduleError ? '重试保存' : '保存日程'}</Button></div>
    </fieldset>
  </form>;
}

/** 原生模态框限制焦点范围，优先聚焦安全按钮并在关闭后恢复原入口。 */
function Modal({ title, busy, onCancel, children }: { title: string; busy: boolean; onCancel: () => void; children: ReactNode }) {
  const ref = useRef<HTMLDialogElement>(null);
  useEffect(() => {
    const previous = document.activeElement as HTMLElement | null; const dialog = ref.current!;
    dialog.showModal(); dialog.querySelector<HTMLElement>('.safe-action')?.focus();
    return () => { dialog.close(); if (previous?.isConnected) previous.focus(); else document.querySelector<HTMLElement>('.schedule-add')?.focus(); };
  }, []);
  /** 显式循环确认按钮，避免浏览器把 Shift+Tab 焦点移出模态框。 */
  function trapFocus(event: ReactKeyboardEvent<HTMLDialogElement>) {
    if (event.key !== 'Tab') return;
    const buttons = [...ref.current!.querySelectorAll<HTMLButtonElement>('button:not(:disabled)')];
    if (!buttons.length) { event.preventDefault(); return; }
    const first = buttons[0]; const last = buttons[buttons.length - 1];
    if (event.shiftKey && document.activeElement === first) { event.preventDefault(); last.focus(); }
    else if (!event.shiftKey && document.activeElement === last) { event.preventDefault(); first.focus(); }
  }
  return <dialog className="schedule-dialog" ref={ref} aria-labelledby="schedule-dialog-title" aria-busy={busy} onKeyDown={trapFocus} onCancel={event => { event.preventDefault(); if (!busy) onCancel(); }}><h2 id="schedule-dialog-title">{title}</h2>{children}</dialog>;
}

/** 全局显示确认层，侧栏导航也必须经过草稿守卫。 */
export function ScheduleDialogs({ controller, state }: { controller: Controller; state: AppState }) {
  if (state.discardTo) return <Modal title="放弃这次创建？" busy={false} onCancel={() => controller.keepDraft()}><h3>日程还没有保存</h3><p>离开后，本次填写的内容将被丢弃。</p><div className="schedule-actions"><Button secondary className="safe-action" onClick={() => controller.keepDraft()}>继续填写</Button><Button className="danger" onClick={() => controller.discardDraft()}>放弃创建</Button></div></Modal>;
  const item = state.deleting; if (!item) return null;
  const error = state.scheduleError; const stale = error?.code === 'STALE_REVISION'; const missing = error?.code === 'NOT_FOUND';
  return <Modal title={state.scheduleBusy ? '正在处理日程…' : stale ? '日程已更新' : missing ? '日程已不存在' : item.cron ? '永久删除整组日程？' : '永久删除这个日程？'} busy={state.scheduleBusy} onCancel={() => controller.keepSchedule()}>
    <h3>{item.title}</h3><p className="hint">{scheduleSummary(item)}</p><p>{item.cron ? '这会删除整组日程及其例外，之后不再提醒。删除后无法恢复。' : '删除后无法恢复，也不会再产生这个日程的提醒。'}</p>
    {error && <Feedback kind="error">{error.message}</Feedback>}{state.scheduleNotice && <Feedback>{state.scheduleNotice}</Feedback>}
    <div className="schedule-actions"><Button secondary className="safe-action" disabled={state.scheduleBusy} onClick={() => controller.keepSchedule()}>{missing ? '返回列表' : '保留日程'}</Button><Button className={stale || missing ? '' : 'danger'} disabled={state.scheduleBusy} aria-busy={state.scheduleBusy || undefined} onClick={() => void (stale || missing ? controller.recoverDelete() : controller.deleteSchedule())}>{state.scheduleBusy ? '正在处理…' : stale ? '查看最新日程' : missing ? '刷新列表' : error ? '重试删除' : '永久删除'}</Button></div>
  </Modal>;
}
