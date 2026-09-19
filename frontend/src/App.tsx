import { useEffect, useState, useSyncExternalStore, type CSSProperties, type FormEvent } from 'react';
import { type AppPort, type CloseBehavior, type LogLevel, type LogLevelSettings, type StorageDirectories, type StorageDirectory, type Theme } from './contracts';
import { Controller } from './controller';
import { RemindersPage } from './features/reminders/RemindersPage';
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
      {state.page === 'settings' ? <Settings controller={controller} theme={state.theme} closeBehavior={state.closeBehavior} logLevel={state.logLevel} storageDirectories={state.storageDirectories} busy={state.busy} /> : state.page === 'reminders' ? <RemindersPage actions={controller} state={state} /> : <SchedulesPage actions={controller} state={state} />}
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
