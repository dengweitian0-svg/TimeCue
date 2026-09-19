import { useEffect, useState, useSyncExternalStore, type CSSProperties, type FormEvent } from 'react';
import { type AppPort, type CloseBehavior, type LogLevel, type LogLevelSettings, type ReminderDetail, type StorageDirectories, type StorageDirectory, type Theme } from './contracts';
import { Controller, groupReminders } from './controller';
import { SchedulesPage } from './features/schedules/SchedulesPage';
import { ErrorBanner } from './shared/components/ErrorBanner';
import { LoadingCard } from './shared/components/LoadingCard';
import { NoticeToast } from './shared/components/NoticeToast';
import { themeOrder, themeStyle, themes } from './themes';

/** 生产桌面页面只呈现日程、关闭偏好、运行状态和激活结果。 */
export function App({ controller }: { controller: Controller }) {
  const state = useSyncExternalStore(controller.subscribe, controller.snapshot);
  useEffect(() => { void controller.initialize(); return () => controller.dispose(); }, [controller]);
  if (state.themeLoading) return <ThemeLoadingShell theme={state.theme} />;
  return <div className="app-shell" data-theme={state.theme} style={themeStyle(state.theme) as CSSProperties}>
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
      {state.error && <ErrorBanner error={state.error} />}
      {state.notice && <NoticeToast message={state.notice} onDismiss={message => controller.clearNotice(message)} />}
      {state.page === 'settings' ? <Settings controller={controller} theme={state.theme} closeBehavior={state.closeBehavior} logLevel={state.logLevel} storageDirectories={state.storageDirectories} busy={state.busy} /> : state.page === 'reminders' ? <Reminders controller={controller} state={state} /> : <SchedulesPage actions={controller} state={state} />}
    </main>
  </div>;
}

/** 主题尚未读取完成时显示稳定的奶油桃加载壳，避免未确认的配色闪烁。 */
function ThemeLoadingShell({ theme }: { theme: Theme }) {
  return <div className="app-shell theme-loading-shell" style={themeStyle(theme) as CSSProperties} aria-busy="true"><main className="main-content"><div className="theme-loading-content"><span className="brand-mark">◷</span><p className="kicker">TIME CUE / DESKTOP</p><div className="loading-card"><span /><span /><span /></div><p>正在读取外观设置…</p></div></main></div>;
}

/** 页面导航按钮只改变前端视图，不直接接触后端状态。 */
function NavButton({ active, children, onClick }: { active: boolean; children: string; onClick: () => void }) { return <button className={`nav-button${active ? ' active' : ''}`} aria-current={active ? 'page' : undefined} onClick={onClick}>{children}<span aria-hidden="true">↗</span></button>; }

/** 展示运行状态和清理失败的恢复入口。 */
function RuntimeChip({ runtime }: { runtime: AppState['runtime'] }) {
  const label = !runtime ? '正在连接' : runtime.recoveryStatus === 'running' ? '正在恢复提醒' : runtime.recoveryStatus === 'failed' ? '提醒恢复失败' : runtime.lifecycle === 'cleanup-failed' ? '需要清理' : runtime.lifecycle === 'running' ? '后台提醒运行中' : runtime.lifecycle === 'stopping' ? '正在停止' : '已停止';
  const warning = runtime?.lifecycle === 'cleanup-failed' || runtime?.recoveryStatus === 'failed';
  return <div className={`runtime-chip ${warning ? 'warning' : ''}`}><span className="pulse" />{label}</div>;
}

/** 在页面中央显示业务结果，并在五秒后自动清除。 */
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

/** 设置页面呈现四类用户配置、显式恢复默认操作和清理重试入口。 */
function Settings({ controller, theme, closeBehavior, logLevel, storageDirectories, busy }: { controller: Controller; theme: Theme; closeBehavior: CloseBehavior; logLevel: LogLevelSettings; storageDirectories: StorageDirectories | null; busy: boolean }) {
  const [choice, setChoice] = useState(closeBehavior);
  const [level, setLevel] = useState<LogLevel>(logLevel.savedLevel);
  useEffect(() => setChoice(closeBehavior), [closeBehavior]);
  useEffect(() => setLevel(logLevel.savedLevel), [logLevel.savedLevel]);
  const state = controller.snapshot();
  return <section className="page-section"><div className="page-heading"><div><p className="kicker">PREFERENCES</p><h1>设置</h1><p>让 TimeCue 按你的习惯，安静陪伴。</p></div></div>
    <AppearanceSettings controller={controller} theme={theme} busy={busy} />
    <section className="settings-card"><p className="card-eyebrow">窗口行为</p><h2>关闭窗口时</h2><p className="card-copy">选择点击右上角关闭按钮后的行为。</p><div className="preference-options"><Preference value="tray" choice={choice} busy={busy} onChange={setChoice} title="隐藏到托盘" copy="窗口收起，后台提醒继续。" /><Preference value="exit" choice={choice} busy={busy} onChange={setChoice} title="退出应用" copy="停止提醒并清理已投递通知。" /></div><div className="form-actions"><button className="secondary-button" disabled={busy} onClick={() => void controller.restoreConfigDefault('window.close_behavior')}>恢复默认</button><button className="primary-button" disabled={busy || choice === closeBehavior} onClick={() => void controller.saveCloseBehavior(choice)}>保存偏好</button></div></section>
    <section className="settings-card settings-card-spaced"><p className="card-eyebrow">日志级别</p><h2>记录详细程度</h2><p className="card-copy">保存后重启应用生效，当前日志器不会立即切换。</p><label className="select-field" htmlFor="log-level">日志级别<select id="log-level" value={level} disabled={busy} onChange={event => setLevel(event.target.value as LogLevel)}><option value="debug">Debug · 最详细</option><option value="info">Info · 常规</option><option value="warn">Warn · 警告</option><option value="error">Error · 错误</option></select></label><div className="form-actions"><button className="secondary-button" disabled={busy} onClick={() => void controller.restoreConfigDefault('log.level')}>恢复默认</button><button className="primary-button" disabled={busy || level === logLevel.savedLevel} onClick={() => void controller.saveLogLevel(level)}>保存级别</button></div>{logLevel.restartRequired && <p className="directory-result">已保存级别与当前生效级别不同，重启后生效。</p>}</section>
    <div className="settings-subheading"><p className="card-eyebrow">存储位置</p><h2>日志与备份</h2><p>保存后重启应用生效，旧目录中的文件会保留。</p></div>{storageDirectories ? <><StorageDirectoryCard controller={controller} directory={storageDirectories.logs} busy={busy} /><StorageDirectoryCard controller={controller} directory={storageDirectories.backups} busy={busy} /></> : <LoadingCard />}{state.runtime?.lifecycle === 'cleanup-failed' && <section className="settings-card warning-card"><p className="card-eyebrow">运行状态</p><h2>通知清理未完成</h2><p className="card-copy">应用仍然可用，可以重试清理系统通知。</p><button className="secondary-button" disabled={busy} onClick={() => void controller.retryCleanup()}>重试清理</button></section>}</section>;
}

/** 展示四套主题预览，并将点击动作交给 Controller 的保存状态机。 */
function AppearanceSettings({ controller, theme, busy }: { controller: Controller; theme: Theme; busy: boolean }) {
  return <section className="settings-card appearance-card" aria-labelledby="appearance-title"><p className="card-eyebrow">外观</p><h2 id="appearance-title">选择主题</h2><p className="card-copy">点击主题卡片即可保存并立即应用。</p><div className="theme-options" role="group" aria-label="颜色主题">{themeOrder.map(value => <ThemeCard key={value} theme={value} selected={theme === value} busy={busy} onSelect={() => void controller.saveTheme(value)} />)}</div></section>;
}

/** 单个主题选项使用原生按钮保证鼠标、键盘和辅助技术行为一致。 */
function ThemeCard({ theme, selected, busy, onSelect }: { theme: Theme; selected: boolean; busy: boolean; onSelect: () => void }) {
  const palette = themes[theme];
  return <button type="button" className={`theme-card${selected ? ' selected' : ''}`} aria-label={palette.name} aria-pressed={selected} disabled={busy} onClick={onSelect}><span className="theme-preview" style={themeStyle(theme) as CSSProperties} aria-hidden="true"><span className="theme-preview-sidebar" /><span className="theme-preview-content"><span /><b /><i /></span></span><span className="theme-card-copy"><strong>{palette.name}</strong><small>{selected ? '当前使用' : '点击切换'}</small></span><span className="theme-card-check" aria-hidden="true">{selected ? '✓' : ''}</span></button>;
}

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

  /** 恢复当前目录属性的默认值，并复用控制器的统一结果反馈。 */
  async function restoreDefault() {
    if (busy) return;
    await controller.restoreConfigDefault(directory.kind === 'logs' ? 'log.path' : 'backup.dir');
    const error = controller.snapshot().error;
    setResult(error ? { text: error.message, error: true } : { text: '已恢复默认，重启后生效。', error: false });
  }

  return <section className="settings-card directory-card" aria-label={`${label}目录设置`}><div className="directory-heading"><div><p className="card-eyebrow">{label}目录</p><h2>{label}文件存储位置</h2></div>{directory.restartRequired && <span className="restart-badge">重启后生效</span>}</div><dl className="directory-status"><div><dt>当前生效</dt><dd data-testid={`${directory.kind}-effective`}>{directory.effectivePath}</dd></div><div><dt>已保存</dt><dd data-testid={`${directory.kind}-saved`}>{directory.savedPath}</dd></div></dl><div className="directory-row"><label className="visually-hidden" htmlFor={`${directory.kind}-path`}>{label}目录</label><input id={`${directory.kind}-path`} className="path-input" value={input} disabled={busy} onChange={event => setInput(event.target.value)} spellCheck={false} /><button className="secondary-button" disabled={busy} onClick={() => void choose()}>选择目录</button><button className="secondary-button" disabled={busy} onClick={() => void restoreDefault()}>恢复默认</button><button className="primary-button" disabled={busy || input === directory.savedPath} onClick={() => void save()}>{busy ? '正在保存…' : '保存'}</button></div>{result && <p className={result.error ? 'directory-result error' : 'directory-result'} role={result.error ? 'alert' : 'status'}>{result.text}</p>}</section>;
}

/** 空列表的友好引导。 */
function EmptySchedules({ onCreate }: { onCreate: () => void }) { return <div className="empty-card"><span className="empty-mark">✦</span><h2>还没有日程</h2><p>从一个小小的安排开始，让时间为你留出空间。</p><button className="secondary-button" onClick={onCreate}>添加第一个日程</button></div>; }

/** 待处理提醒为空时明确说明没有需要确认的消息。 */
function EmptyReminders() { return <div className="empty-card"><span className="empty-mark">✓</span><h2>没有待处理提醒</h2><p>新的系统通知到达后，会在这里保留到你确认。</p></div>; }

/** 读取期间保留页面布局，避免内容跳动。 */
type AppState = ReturnType<Controller['snapshot']>;

/** 由 Vite 入口延迟创建适配器，避免生产页面绑定具体生成文件。 */
export function createProductionApp(port: AppPort) { return <App controller={new Controller(port)} />; }
