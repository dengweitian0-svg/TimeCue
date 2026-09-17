import { useEffect, useState, useSyncExternalStore, type FormEvent } from 'react';
import { ScheduleError, type AppPort, type CloseBehavior, type Schedule, type ScheduleField, type ScheduleType, type StorageDirectories, type StorageDirectory } from './contracts';
import { Controller } from './controller';
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
      {state.page === 'settings' ? <Settings controller={controller} closeBehavior={state.closeBehavior} storageDirectories={state.storageDirectories} busy={state.busy} /> : <Schedules controller={controller} state={state} />}
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

/** 读取期间保留页面布局，避免内容跳动。 */
function LoadingCard() { return <div className="loading-card"><span /><span /><span /></div>; }

type AppState = ReturnType<Controller['snapshot']>;

/** 由 Vite 入口延迟创建适配器，避免生产页面绑定具体生成文件。 */
export function createProductionApp(port: AppPort) { return <App controller={new Controller(port)} />; }
