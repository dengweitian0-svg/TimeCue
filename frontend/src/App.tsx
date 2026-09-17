import { useEffect, useState, useSyncExternalStore, type FormEvent } from 'react';
import { ScheduleError, type AppPort, type CloseBehavior, type ReminderDetail, type Schedule, type ScheduleField, type ScheduleType, type StorageDirectories, type StorageDirectory } from './contracts';
import { Controller, groupReminders } from './controller';
import { draftRequest, localMinute, scheduleSummary, scheduleTypes } from './schedule-model';

/** 生产桌面页面只呈现日程、关闭偏好、运行状态和激活结果。 */
export function App({ controller }: { controller: Controller }) {
  const state = useSyncExternalStore(controller.subscribe, controller.snapshot);
  useEffect(() => { void controller.initialize(); return () => controller.dispose(); }, [controller]);
  return <div className="app-shell">
    <aside className="sidebar">
      <div className="brand"><span className="brand-mark">◷</span><span>TimeCue</span></div>
      <p className="sidebar-label">工作台</p>
      <NavButton active={state.page === 'schedules'} onClick={() => controller.setPage('schedules')}>我的日程</NavButton>
      <NavButton active={state.page === 'reminders'} onClick={() => controller.setPage('reminders')}>待处理提醒</NavButton>
      <NavButton active={state.page === 'settings'} onClick={() => controller.setPage('settings')}>设置</NavButton>
      <div className="sidebar-spacer" />
      <RuntimeChip runtime={state.runtime} />
      <p className="sidebar-footer">让每件小事都有被记住的时间。</p>
    </aside>
    <main className="main-content">
      <header className="topbar"><div><p className="kicker">TIME CUE / DESKTOP</p><p className="today">今天，按自己的节奏来</p></div><span className="status-dot" aria-label="应用在线" /></header>
      {state.activation && <div className="activation-banner" role="status">已收到窗口激活请求 {state.activation.targetId && <><strong>#{state.activation.targetId}</strong>{state.activation.originalDate && <span>{state.activation.originalDate}</span>}</>}<span>序列 {state.activation.sequence}</span></div>}
      {state.error && <ErrorBanner error={state.error} />}
      {state.notice && !state.activation && <div className="notice" role="status">{state.notice}</div>}
      {state.page === 'settings' ? <Settings controller={controller} closeBehavior={state.closeBehavior} storageDirectories={state.storageDirectories} busy={state.busy} /> : state.page === 'reminders' ? <Reminders controller={controller} state={state} /> : <Schedules controller={controller} state={state} />}
    </main>
  </div>;
}

/** 页面导航按钮只改变前端视图，不直接接触后端状态。 */
function NavButton({ active, children, onClick }: { active: boolean; children: string; onClick: () => void }) { return <button className={`nav-button${active ? ' active' : ''}`} aria-current={active ? 'page' : undefined} onClick={onClick}>{children}<span aria-hidden="true">↗</span></button>; }

/** 展示运行状态和清理失败的恢复入口。 */
function RuntimeChip({ runtime }: { runtime: AppState['runtime'] }) {
  const label = !runtime ? '正在连接' : runtime.lifecycle === 'cleanup-failed' ? '需要清理' : runtime.lifecycle === 'running' ? '后台提醒运行中' : runtime.lifecycle === 'stopping' ? '正在停止' : '已停止';
  return <div className={`runtime-chip ${runtime?.lifecycle === 'cleanup-failed' ? 'warning' : ''}`}><span className="pulse" />{label}</div>;
}

/** 待处理提醒页面只读取列表和当前详情，不在查看或切换时提交确认。 */
function Reminders({ controller, state }: { controller: Controller; state: AppState }) {
  const group = groupReminders(state.reminders.items, state.activeReminder);
  return <section className="page-section reminder-page"><div className="page-heading"><div><p className="kicker">PENDING REMINDERS</p><h1>待处理提醒</h1><p>从通知回到这里，查看仍需要你处理的消息。</p></div></div>
    {state.reminderError && <ErrorBanner error={state.reminderError} />}
    <div className="list-toolbar"><span>{state.reminderLoading && !state.reminders.items.length ? '正在读取…' : `${state.reminders.items.length} 条待处理提醒`}</span><button className="text-button" onClick={() => void controller.refreshReminders()} disabled={state.reminderLoading}>重新加载</button></div>
    {state.reminderLoading && !state.reminders.items.length ? <LoadingCard /> : state.reminders.items.length ? <div className="reminder-layout"><div><div className="reminder-list">{state.reminders.items.map(item => <ReminderCard key={`${item.scheduleId}/${item.originalDate}`} item={item} active={item.scheduleId === state.activeReminder?.scheduleId && item.originalDate === state.activeReminder.originalDate} onClick={() => controller.selectReminder(item)} />)}</div>{state.reminders.nextCursor && <button className="secondary-button pagination" onClick={() => void controller.nextReminderPage()} disabled={state.reminderLoading}>加载更多</button>}</div><ReminderDetailPanel controller={controller} detail={state.reminderDetail} group={group} loading={state.reminderDetailLoading} confirming={state.reminderConfirming} active={state.activeReminder} /></div> : <EmptyReminders />}
  </section>;
}

/** 显示待处理列表项的稳定身份、状态和可用时间。 */
function ReminderCard({ item, active, onClick }: { item: ReminderDetail; active: boolean; onClick: () => void }) {
  const label = item.contentStatus === 'available' && item.title ? item.title : '提醒内容暂不可用';
  return <button className={`reminder-card${active ? ' active' : ''}`} onClick={onClick} aria-current={active ? 'true' : undefined}><span className="reminder-card-mark">◷</span><span className="reminder-card-body"><strong>{label}</strong><small>{item.startUnix === null ? item.originalDate : formatReminderTime(item.startUnix)}</small></span><span className="reminder-card-arrow">›</span></button>;
}

/** 呈现当前提醒的只读正文、状态和同刻切换入口。 */
function ReminderDetailPanel({ controller, detail, group, loading, confirming, active }: { controller: Controller; detail: ReminderDetail | null; group: ReminderDetail[]; loading: boolean; confirming: boolean; active: AppState['activeReminder'] }) {
  return <section className="reminder-detail-card" aria-label="提醒详情"><p className="card-eyebrow">CURRENT REMINDER</p>{loading ? <LoadingCard /> : !detail || !active ? <div className="detail-empty"><h2>选择一条提醒</h2><p>从左侧列表选择消息，查看它当前可重建的详情。</p></div> : <><div className="reminder-detail-heading"><div><h2>{detail.title ?? reminderStatusTitle(detail.status)}</h2><p>{detail.startUnix === null ? detail.originalDate : formatReminderTime(detail.startUnix)}</p></div><span className={`reminder-status ${detail.status}`}>{reminderStatusLabel(detail.status)}</span></div>{detail.status === 'pending' && detail.contentStatus === 'available' ? <div className="reminder-copy"><p>{detail.notes || '这条提醒没有附加正文。'}</p></div> : <div className="reminder-unavailable" role="status"><strong>{detail.status === 'confirmed' ? '这条提醒已经确认。' : detail.status === 'missing' ? '这条提醒已不存在。' : detail.status === 'not_processable' ? '这条提醒当前不可处理。' : '提醒内容暂不可用。'}</strong><p>{detail.status === 'pending' ? '系统保留了提醒身份，但无法从当前日程事实重建正文。' : '页面不会用当前或空白内容代替历史正文。'}</p></div>}{detail.status === 'pending' && <button className="primary-button reminder-confirm-button" disabled={confirming} onClick={() => void controller.confirmReminder()}>{confirming ? '正在确认…' : detail.contentStatus === 'available' ? '确认提醒' : '确认已投递提醒'}</button>}{group.length > 1 && <div className="same-time-group"><p className="card-eyebrow">SAME TIME</p><div>{group.map(item => <button key={`${item.scheduleId}/${item.originalDate}`} className={`text-button${item.scheduleId === active.scheduleId && item.originalDate === active.originalDate ? ' selected' : ''}`} onClick={() => controller.selectReminder(item)}>{item.contentStatus === 'available' && item.title ? item.title : `提醒 ${item.scheduleId}`}</button>)}</div></div>}</>}</section>;
}

/** 将提醒状态转换成页面可读标题。 */
function reminderStatusTitle(status: ReminderDetail['status']): string { return status === 'confirmed' ? '已确认的提醒' : status === 'missing' ? '已不存在的提醒' : status === 'not_processable' ? '不可处理的提醒' : '待处理提醒'; }

/** 将提醒状态转换成短标签。 */
function reminderStatusLabel(status: ReminderDetail['status']): string { return status === 'pending' ? '待处理' : status === 'confirmed' ? '已确认' : status === 'missing' ? '已不存在' : '不可处理'; }

/** 将 Unix 秒转换为本地化的提醒时间。 */
function formatReminderTime(unix: number): string { return new Date(unix * 1000).toLocaleString('zh-CN', { month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit' }); }

/** 展示结构化错误及字段级提示。 */
function ErrorBanner({ error }: { error: ScheduleError }) { return <div className="error-banner" role="alert"><span className="error-icon">!</span><div><strong>{error.message}</strong>{Object.values(error.fields)[0] && <p>{Object.values(error.fields)[0]}</p>}</div></div>; }

/** 日程定义列表与创建入口。 */
function Schedules({ controller, state }: { controller: Controller; state: AppState }) {
  if (state.draft) return <ScheduleForm controller={controller} state={state} />;
  return <section className="page-section"><div className="page-heading"><div><p className="kicker">SCHEDULES</p><h1>我的日程</h1><p>给重要的小事，留出一段完整的时间。</p></div><button className="primary-button" onClick={() => void controller.beginCreate()}>＋ 添加日程</button></div>
    <div className="list-toolbar"><span>{state.loading ? '正在读取…' : `${state.schedules.items.length} 个日程`}</span><button className="text-button" onClick={() => void controller.refreshSchedules()} disabled={state.loading}>重新加载</button></div>
    {state.schedules.items.length ? <div className="schedule-list">{state.schedules.items.map(item => <ScheduleCard key={item.id} schedule={item} onDelete={() => controller.askDelete(item)} />)}</div> : state.loading ? <LoadingCard /> : <EmptySchedules onCreate={() => void controller.beginCreate()} />}
    {state.schedules.nextCursor && <button className="secondary-button pagination" onClick={() => void controller.nextPage()}>加载更多</button>}
    {state.notice && <p className="inline-notice">{state.notice}</p>}
    {state.deleting && <DeleteDialog controller={controller} item={state.deleting} busy={state.busy} error={state.error} />}
  </section>;
}

/** 显示一条日程定义及其永久删除动作。 */
function ScheduleCard({ schedule, onDelete }: { schedule: Schedule; onDelete: () => void }) { return <article className="schedule-card"><div className="schedule-accent" /><div className="schedule-card-body"><div className="schedule-card-head"><div><p className="schedule-type">{schedule.cron ? '重复日程' : '单次日程'}</p><h2>{schedule.title}</h2></div><button className="icon-button delete" aria-label={`删除 ${schedule.title}`} onClick={onDelete}>×</button></div><p className="schedule-summary">{scheduleSummary(schedule)}</p>{schedule.notes && <p className="schedule-notes">{schedule.notes}</p>}</div></article>; }

/** 创建页面只持有输入，最终校验与提交由控制器完成。 */
function ScheduleForm({ controller, state }: { controller: Controller; state: AppState }) {
  const draft = state.draft!; const [localError, setLocalError] = useState<ScheduleError | null>(null);
  const error = localError ?? state.error;
  /** 先运行本地纯模型校验，以便表单即时显示字段错误。 */
  function submit(event: FormEvent) { event.preventDefault(); try { draftRequest(draft); setLocalError(null); void controller.saveCreate(); } catch (failure) { setLocalError(failure instanceof ScheduleError ? failure : null); } }
  /** 读取字段错误并建立控件可访问性关联。 */
  function field(name: ScheduleField) { const message = error?.fields[name]; return { 'aria-invalid': Boolean(message), 'aria-describedby': message ? `error-${name}` : undefined }; }
  return <form className="page-section create-form" onSubmit={submit} noValidate><div className="page-heading compact"><div><p className="kicker">NEW SCHEDULE</p><h1>添加日程</h1><p>把想做的事，轻轻记下来。</p></div><button type="button" className="text-button" onClick={() => controller.cancelCreate()}>取消</button></div>
    <div className="form-card"><label>标题 <span>*</span><input autoFocus value={draft.title} placeholder="例如：读完一本喜欢的书" {...field('title')} onChange={event => { setLocalError(null); controller.editDraft({ title: event.target.value }); }} /></label><FieldError error={error} name="title" />
      <label>备注 <small>选填</small><textarea value={draft.notes} placeholder="写点想提醒自己的话" onChange={event => controller.editDraft({ notes: event.target.value })} /></label>
      <fieldset><legend>重复方式</legend><div className="type-options">{(Object.keys(scheduleTypes) as ScheduleType[]).map(type => <button type="button" key={type} className={`type-option ${draft.type === type ? 'selected' : ''}`} aria-pressed={draft.type === type} onClick={() => controller.editDraft({ type })}>{scheduleTypes[type]}</button>)}</div></fieldset><FieldError error={error} name="type" />
      <div className="form-grid"><DateField label="开始时间" value={draft.start} error={error} name="start" min={state.bounds?.min} max={state.bounds ? localMinute(new Date(new Date(state.bounds.startMaxExclusive).getTime() - 60000)) : undefined} onChange={start => controller.editDraft({ start })} /><DateField label="结束时间" value={draft.end} error={error} name="end" min={draft.start || state.bounds?.min} max={state.bounds?.endMaxInclusive} onChange={end => controller.editDraft({ end })} /></div>
      {draft.type !== 'single' && <DateField label="重复截止日期" dateOnly value={draft.until} error={error} name="until" min={draft.start.slice(0, 10)} onChange={until => controller.editDraft({ until })} />}
      {state.bounds && draft.type === 'single' && <p className="field-hint">可选范围：{state.bounds.min.replace('T', ' ')} 起，结束不晚于 {state.bounds.endMaxInclusive.replace('T', ' ')}。</p>}
      <div className="form-actions"><button type="button" className="secondary-button" onClick={() => controller.cancelCreate()}>取消</button><button type="submit" className="primary-button" disabled={state.busy}>{state.busy ? '正在保存…' : '保存日程'}</button></div>
    </div></form>;
}

/** 显示单个日期时间输入并关联结构化字段错误。 */
function DateField({ label, name, value, min, max, dateOnly = false, error, onChange }: { label: string; name: ScheduleField; value: string; min?: string; max?: string; dateOnly?: boolean; error: ScheduleError | null; onChange: (value: string) => void }) { const message = error?.fields[name]; return <label>{label} <span>*</span><input type={dateOnly ? 'date' : 'datetime-local'} value={value} min={min?.slice(0, dateOnly ? 10 : 16)} max={max?.slice(0, dateOnly ? 10 : 16)} aria-invalid={Boolean(message)} aria-describedby={message ? `error-${name}` : undefined} onChange={event => onChange(event.target.value)} />{message && <span className="field-error" id={`error-${name}`}>{message}</span>}</label>; }

/** 显示标题和重复规则等非日期字段错误。 */
function FieldError({ error, name }: { error: ScheduleError | null; name: ScheduleField }) { const message = error?.fields[name]; return message ? <span className="field-error" id={`error-${name}`}>{message}</span> : null; }

/** 删除确认对话框支持并发失败后的重新读取路径。 */
function DeleteDialog({ controller, item, busy, error }: { controller: Controller; item: Schedule; busy: boolean; error: ScheduleError | null }) { const recovery = error?.code === 'STALE_REVISION' || error?.code === 'NOT_FOUND'; return <div className="dialog-backdrop" role="presentation"><section className="dialog" role="dialog" aria-modal="true" aria-labelledby="delete-title"><p className="kicker">DELETE SCHEDULE</p><h2 id="delete-title">{recovery ? '日程状态已变化' : '删除这条日程？'}</h2><p><strong>{item.title}</strong></p><p className="dialog-copy">{recovery ? error?.message : '删除后无法恢复，也不会再产生这条日程的提醒。'}</p><div className="form-actions"><button className="secondary-button" disabled={busy} onClick={() => controller.cancelDelete()}>保留日程</button>{recovery ? <button className="primary-button" disabled={busy} onClick={() => void controller.recoverDelete()}>{error?.code === 'NOT_FOUND' ? '刷新列表' : '查看最新内容'}</button> : <button className="danger-button" disabled={busy} onClick={() => void controller.confirmDelete()}>永久删除</button>}</div></section></div>; }

/** 设置页面只呈现已冻结的关闭偏好和清理重试操作。 */
function Settings({ controller, closeBehavior, storageDirectories, busy }: { controller: Controller; closeBehavior: CloseBehavior; storageDirectories: StorageDirectories | null; busy: boolean }) { const [choice, setChoice] = useState(closeBehavior); useEffect(() => setChoice(closeBehavior), [closeBehavior]); const state = controller.snapshot(); return <section className="page-section"><div className="page-heading"><div><p className="kicker">PREFERENCES</p><h1>设置</h1><p>让 TimeCue 按你的习惯，安静陪伴。</p></div></div><section className="settings-card"><p className="card-eyebrow">窗口行为</p><h2>关闭窗口时</h2><p className="card-copy">选择点击右上角关闭按钮后的行为。</p><div className="preference-options"><Preference value="tray" choice={choice} busy={busy} onChange={setChoice} title="隐藏到托盘" copy="窗口收起，后台提醒继续。" /><Preference value="exit" choice={choice} busy={busy} onChange={setChoice} title="退出应用" copy="停止提醒并清理已投递通知。" /></div><button className="primary-button" disabled={busy || choice === closeBehavior} onClick={() => void controller.saveCloseBehavior(choice)}>保存偏好</button></section><div className="settings-subheading"><p className="card-eyebrow">存储位置</p><h2>日志与备份</h2><p>保存后重启应用生效，旧目录中的文件会保留。</p></div>{storageDirectories ? <><StorageDirectoryCard controller={controller} directory={storageDirectories.logs} busy={busy} /><StorageDirectoryCard controller={controller} directory={storageDirectories.backups} busy={busy} /></> : <LoadingCard />}{state.runtime?.lifecycle === 'cleanup-failed' && <section className="settings-card warning-card"><p className="card-eyebrow">运行状态</p><h2>通知清理未完成</h2><p className="card-copy">应用仍然可用，可以重试清理系统通知。</p><button className="secondary-button" disabled={busy} onClick={() => void controller.retryCleanup()}>重试清理</button></section>}</section>; }

/** 单项关闭行为选择。 */
function Preference({ value, choice, busy, title, copy, onChange }: { value: CloseBehavior; choice: CloseBehavior; busy: boolean; title: string; copy: string; onChange: (value: CloseBehavior) => void }) { return <label className={`preference ${choice === value ? 'selected' : ''}`}><input type="radio" name="close-behavior" value={value} checked={choice === value} disabled={busy} onChange={() => onChange(value)} /><span><strong>{title}</strong><small>{copy}</small></span>{choice === value && <b>✓</b>}</label>; }

/** 独立呈现一类目录的待保存输入、当前生效值和重启提示。 */
function StorageDirectoryCard({ controller, directory, busy }: { controller: Controller; directory: StorageDirectory; busy: boolean }) {
  const label = directory.kind === 'logs' ? '日志' : '备份';
  const [input, setInput] = useState(directory.savedPath);
  const [result, setResult] = useState<{ text: string; error: boolean } | null>(null);
  useEffect(() => { setInput(directory.savedPath); }, [directory.savedPath]);

  /** 目录选择取消时保留原输入和当前生效/保存状态。 */
  async function choose() {
    if (busy) return;
    const selected = await controller.chooseDirectory(directory.kind);
    const error = controller.snapshot().error;
    if (error) setResult({ text: error.message, error: true });
    else if (selected === null) setResult({ text: '已取消选择，目录未改变。', error: false });
    else { setInput(selected); setResult(null); }
  }

  /** 保存失败时保留输入，成功时只显示后端返回的 effective/saved 结果。 */
  async function save() {
    if (busy) return;
    const saved = await controller.saveStorageDirectory(directory.kind, input);
    if (saved) {
      setInput(saved.savedPath);
      setResult({ text: saved.restartRequired ? '已保存，重启后生效。当前仍使用旧目录。' : '已保存，当前目录无需切换。', error: false });
    } else {
      const error = controller.snapshot().error;
      setResult({ text: error?.message ?? '保存失败，请重试。', error: true });
    }
  }

  return <section className="settings-card directory-card" aria-label={`${label}目录设置`}><div className="directory-heading"><div><p className="card-eyebrow">{label}目录</p><h2>{label}文件存储位置</h2></div>{directory.restartRequired && <span className="restart-badge">重启后生效</span>}</div><dl className="directory-status"><div><dt>当前生效</dt><dd data-testid={`${directory.kind}-effective`}>{directory.effectivePath}</dd></div><div><dt>已保存</dt><dd data-testid={`${directory.kind}-saved`}>{directory.savedPath}</dd></div></dl><div className="directory-row"><label className="visually-hidden" htmlFor={`${directory.kind}-path`}>{label}目录</label><input id={`${directory.kind}-path`} className="path-input" value={input} disabled={busy} onChange={event => setInput(event.target.value)} spellCheck={false} /><button className="secondary-button" disabled={busy} onClick={() => void choose()}>选择目录</button><button className="primary-button" disabled={busy || input === directory.savedPath} onClick={() => void save()}>{busy ? '正在保存…' : '保存'}</button></div>{result && <p className={result.error ? 'directory-result error' : 'directory-result'} role={result.error ? 'alert' : 'status'}>{result.text}</p>}</section>;
}

/** 空列表的友好引导。 */
function EmptySchedules({ onCreate }: { onCreate: () => void }) { return <div className="empty-card"><span className="empty-mark">✦</span><h2>还没有日程</h2><p>从一个小小的安排开始，让时间为你留出空间。</p><button className="secondary-button" onClick={onCreate}>添加第一个日程</button></div>; }

/** 待处理提醒为空时明确说明没有需要确认的消息。 */
function EmptyReminders() { return <div className="empty-card"><span className="empty-mark">✓</span><h2>没有待处理提醒</h2><p>新的系统通知到达后，会在这里保留到你确认。</p></div>; }

/** 读取期间保留页面布局，避免内容跳动。 */
function LoadingCard() { return <div className="loading-card"><span /><span /><span /></div>; }

type AppState = ReturnType<Controller['snapshot']>;

/** 由 Vite 入口延迟创建适配器，避免生产页面绑定具体生成文件。 */
export function createProductionApp(port: AppPort) { return <App controller={new Controller(port)} />; }
