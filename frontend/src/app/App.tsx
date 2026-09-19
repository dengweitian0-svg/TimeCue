import { useEffect, useSyncExternalStore, type CSSProperties } from 'react';
import type { Theme } from '../features/settings/contracts';
import { Controller } from '../controller';
import { RemindersPage } from '../features/reminders/RemindersPage';
import { SchedulesPage } from '../features/schedules/SchedulesPage';
import { SettingsPage } from '../features/settings/SettingsPage';
import { ErrorBanner } from '../shared/components/ErrorBanner';
import { NoticeToast } from '../shared/components/NoticeToast';
import type { AppPort } from '../shared/ports/app-port';
import { themeStyle } from '../themes';
import type { AppState } from './state';

/** 生产桌面页面只负责生命周期、导航、全局反馈和 feature 页面组合。 */
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
      {state.page === 'settings' ? <SettingsPage actions={controller} state={state} /> : state.page === 'reminders' ? <RemindersPage actions={controller} state={state} /> : <SchedulesPage actions={controller} state={state} />}
    </main>
  </div>;
}

/** 主题尚未读取完成时显示稳定的奶油桃加载壳，避免未确认的配色闪烁。 */
function ThemeLoadingShell({ theme }: { theme: Theme }) {
  return <div className="app-shell theme-loading-shell" style={themeStyle(theme) as CSSProperties} aria-busy="true"><main className="main-content"><div className="theme-loading-content"><span className="brand-mark">◷</span><p className="kicker">TIME CUE / DESKTOP</p><div className="loading-card"><span /><span /><span /></div><p>正在读取外观设置…</p></div></main></div>;
}

/** 页面导航按钮只改变前端视图，不直接接触后端状态。 */
function NavButton({ active, children, onClick }: { active: boolean; children: string; onClick: () => void }) {
  return <button className={`nav-button${active ? ' active' : ''}`} aria-current={active ? 'page' : undefined} onClick={onClick}>{children}<span aria-hidden="true">↗</span></button>;
}

/** 展示运行状态和清理失败的恢复入口。 */
function RuntimeChip({ runtime }: { runtime: AppState['runtime'] }) {
  const label = !runtime ? '正在连接' : runtime.recoveryStatus === 'running' ? '正在恢复提醒' : runtime.recoveryStatus === 'failed' ? '提醒恢复失败' : runtime.lifecycle === 'cleanup-failed' ? '需要清理' : runtime.lifecycle === 'running' ? '后台提醒运行中' : runtime.lifecycle === 'stopping' ? '正在停止' : '已停止';
  const warning = runtime?.lifecycle === 'cleanup-failed' || runtime?.recoveryStatus === 'failed';
  return <div className={`runtime-chip ${warning ? 'warning' : ''}`}><span className="pulse" />{label}</div>;
}

/** 由 Vite 入口延迟创建适配器，避免生产页面绑定具体生成文件。 */
export function createProductionApp(port: AppPort) { return <App controller={new Controller(port)} />; }
