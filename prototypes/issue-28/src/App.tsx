import { useEffect, useState, useSyncExternalStore, type CSSProperties } from 'react';
import { Background, Button, Feedback, Mascot } from './components';
import { PrototypeError, type DirectoryKind, type Settings } from './contracts';
import { Controller, type AppState } from './controller';
import { themes, themeStyle, type Theme } from './themes';
import { ScheduleDialogs, Schedules } from './Schedules';

/** 组合产品窗口与模拟桌面，所有业务操作委托给应用控制器。 */
export function App({ controller }: { controller: Controller }) {
  const state = useSyncExternalStore(controller.subscribe, controller.snapshot);
  useEffect(() => { void controller.refresh(); }, [controller]);
  /** 在窗口系统边界处理最终失败。 */
  async function action(operation: string, execute: () => void | Promise<void>) { try { await execute(); controller.clearError(); } catch (error) { controller.report(error, operation); } }
  const settings = state.settings;
  if (!settings) return <Background theme="peach" config={{ kind: 'solid' }}><div className="empty-state"><Mascot /><h1>{state.loading ? '正在准备 TimeCue…' : '暂时无法打开'}</h1>{state.error && <Feedback kind="error">{state.error}</Feedback>}<Button secondary onClick={() => void controller.refresh()}>重新加载</Button></div></Background>;
  if (state.runtime && (state.runtime.visibility !== 'visible' || state.runtime.lifecycle !== 'running')) return <Desktop controller={controller} state={state} action={action} />;
  return <Background theme={settings.theme} config={settings.background}>
    <div className="window">
      <header className="titlebar"><span>◷ &nbsp;TimeCue</span><button className="window-button" aria-label="最小化" onClick={() => void action('minimize', () => controller.ports.minimize())}>—</button><span className="window-button" aria-hidden="true" style={{ flex: 'none', padding: 10 }}>□</span><button className="window-button close" aria-label="关闭窗口" onClick={() => void action('close', () => controller.ports.close())}>×</button></header>
      <div className="shell">
        <aside className="sidebar"><div className="brand"><Mascot size={52} />TimeCue</div>
          <button className="nav" aria-current={state.page === 'reminders' ? 'page' : undefined} onClick={() => controller.setPage('reminders')}>待处理提醒</button>
          <button className="nav" aria-current={state.page === 'schedules' ? 'page' : undefined} disabled={state.scheduleBusy} onClick={() => controller.setPage('schedules')}>我的日程</button>
          <button className="nav" aria-current={state.page === 'settings' ? 'page' : undefined} onClick={() => controller.setPage('settings')}>设置</button>
          <div className="companion"><strong>慢慢来，</strong><p>每件小事都值得<br />被好好记住。</p></div><div className="running">● &nbsp;后台提醒运行中</div><small>TimeCue · 今天也从容</small>
        </aside>
        <main className="content">
          {state.error && <div className="global-error"><Feedback kind="error">{state.error}</Feedback></div>}
          {state.page === 'settings' ? <SettingsPage controller={controller} settings={settings} /> : state.page === 'schedules' ? <Schedules controller={controller} state={state} /> : <Reminders controller={controller} state={state} />}
        </main>
      </div>
    </div>
    <ScheduleDialogs controller={controller} state={state} />
  </Background>;
}

/** 显示明确的无数据或失效目标状态，不编造消息详情。 */
function Empty({ title, body, action, onAction }: { title: string; body: string; action?: string; onAction?: () => void }) {
  return <section className="empty-state"><Mascot /><h2>{title}</h2><p>{body}</p>{action && <Button secondary onClick={onAction}>{action}</Button>}</section>;
}

/** 渲染时间分组与单条确认；查看与切换仅调用详情用例。 */
function Reminders({ controller, state }: { controller: Controller; state: AppState }) {
  const detail = state.detail;
  if (!state.runtime?.ready) return <Empty title="正在准备窗口…" body="已记住通知定位请求，窗口准备好后会自动打开对应详情。" />;
  if (state.loading) return <Empty title="正在加载提醒…" body="请稍等片刻。" />;
  if (state.error && !detail && state.messages.length === 0) return <Empty title="提醒暂时没能加载" body="处理状态不会因此改变。" action="重新加载" onAction={() => void controller.refresh()} />;
  if (detail && detail.status !== 'pending') return <Empty title={detail.status === 'confirmed' ? '这条提醒已经确认' : '这条提醒已不存在'} body={detail.status === 'confirmed' ? '无需再次处理。你可以返回列表，继续查看其他提醒。' : '我们没有找到这条提醒，请返回列表查看当前待处理内容。'} action="返回待处理列表" onAction={() => void controller.refresh()} />;
  if (!detail) return <Empty title="都安顿好啦" body="当前没有待处理提醒，留点时间给自己吧。" />;
  const groups = [...new Set(state.messages.map(m => m.at))];
  const message = detail.message;
  return <><header className="page-header"><h1>待处理提醒</h1><p>把重要的小事，一件件安放好。</p></header>
    <div className="reminder-columns"><section className="message-list" aria-label="待处理消息列表">
      {groups.map((at, index) => <div className="message-group" key={at}><h2 className="group-title">{index === 0 ? `今天 · ${at.slice(11, 16)}` : '此前待处理'} &nbsp; / &nbsp; {state.messages.filter(m => m.at === at).length} 条</h2>
        {state.messages.filter(m => m.at === at).map(item => <button key={item.id} className="message-card" aria-pressed={message.id === item.id} disabled={!!state.confirming} onClick={() => void controller.select(item.id)}><strong>{item.title}</strong><time dateTime={item.at}>{item.at.slice(11, 16)} · 今天</time></button>)}
      </div>)}<p className="list-footnote">{state.messages.length} 条待处理 · 查看不会自动确认</p>
    </section><article className="detail" aria-label="当前提醒详情"><div className="detail-top"><div><p className="eyebrow">给自己一点关照</p><div className="big-time">{message.at.slice(11, 16)}</div><p className="date">今天 · 9 月 15 日，星期二</p></div><Mascot /></div>
      <h2>{message.title}</h2><p className="message-body">{message.body}</p><div className="detail-actions"><Feedback>看过不等于完成，准备好后再确认。</Feedback><div className="action-row"><Button className="confirm-button" busy={!!state.confirming} onClick={() => void controller.confirm()}>✓ &nbsp;{state.error ? '再次确认' : '确认'}</Button><small>只确认这一条提醒</small></div></div>
    </article></div></>;
}

/** 设置分类只管理页面状态，保存委托给设置端口。 */
function SettingsPage({ controller, settings }: { controller: Controller; settings: Settings }) {
  const [tab, setTab] = useState('常规');
  return <><header className="page-header"><h1>设置</h1><p>{tab === '存储位置' ? '把日志和备份，放在你熟悉的地方。' : tab === '外观' ? '选一种喜欢的颜色，让提醒也柔和一点。' : '让 TimeCue 按你的习惯，安静陪伴。'}</p></header>
    <nav className="tabs" aria-label="设置分类">{['常规', '存储位置', '外观'].map(name => <Button key={name} secondary={tab !== name} aria-current={tab === name ? 'page' : undefined} onClick={() => setTab(name)}>{name}</Button>)}</nav>
    {tab === '常规' && <General controller={controller} settings={settings} />}
    {tab === '存储位置' && <><Directory controller={controller} kind="logs" effective={settings.logs} /><Directory controller={controller} kind="backups" effective={settings.backups} /></>}
    {tab === '外观' && <Appearance controller={controller} settings={settings} />}
  </>;
}

/** 保留未保存选择，只有保存成功才更新窗口关闭行为。 */
function General({ controller, settings }: { controller: Controller; settings: Settings }) {
  const [choice, setChoice] = useState(settings.closeBehavior); const [busy, setBusy] = useState(false); const [notice, setNotice] = useState('');
  /** 在偏好提交边界显示结果，并保留失败输入。 */
  async function save() { if (busy) return; setBusy(true); setNotice(''); try { const current = await controller.ports.readSettings(); await controller.ports.savePreferences({ theme: current.theme, closeBehavior: choice }); await controller.refreshSettings(); controller.clearError(); setNotice('偏好已保存，下次关闭窗口时生效。'); } catch (error) { controller.report(error, 'preferences'); } finally { setBusy(false); } }
  return <><section className="settings-card"><h2>关闭窗口时</h2><p>选择点击右上角关闭按钮后的行为。</p>
    <label className="radio-option"><input type="radio" name="close" value="tray" checked={choice === 'tray'} disabled={busy} onChange={() => setChoice('tray')} /><div><strong>隐藏到托盘（推荐）</strong><span>窗口收起，后台提醒继续；可从托盘重新打开。</span></div></label>
    <label className="radio-option"><input type="radio" name="close" value="exit" checked={choice === 'exit'} disabled={busy} onChange={() => setChoice('exit')} /><div><strong>退出应用</strong><span>停止后台提醒，并清理 TimeCue 已投递的系统通知。</span></div></label>
    <Button busy={busy} onClick={() => void save()}>保存偏好</Button></section>{notice ? <Feedback kind="success">{notice}</Feedback> : <p className="hint">保存后生效 · 再次启动只会恢复唯一主窗口</p>}</>;
}

/** 分开管理待保存输入和平台实际生效值，错误不能覆盖用户输入。 */
function Directory({ controller, kind, effective }: { controller: Controller; kind: DirectoryKind; effective: string }) {
  const label = kind === 'logs' ? '日志' : '备份';
  const [input, setInput] = useState(effective); const [actual, setActual] = useState(effective); const [busy, setBusy] = useState(false); const [result, setResult] = useState<{ text: string; error: boolean } | null>(null);
  useEffect(() => { setActual(effective); }, [effective]);
  /** 取消目录选择保留此前输入。 */
  async function choose() { if (busy) return; setBusy(true); setResult(null); try { const path = await controller.ports.chooseDirectory(kind); if (path !== null) setInput(path); else setResult({ text: '已取消选择，目录未改变。', error: false }); } catch (error) { controller.report(error, 'choose-directory'); } finally { setBusy(false); } }
  /** 以适配器返回值更新实际目录，失败时保持待保存输入。 */
  async function save() { if (busy) return; setBusy(true); setResult(null); try { const value = await controller.ports.saveDirectory(kind, input); setActual(value.effectivePath); await controller.refreshSettings(); setResult({ text: '已保存，重启后生效。旧文件保留。', error: false }); controller.clearError(); } catch (error) { if (error instanceof PrototypeError && error.effectivePath !== undefined) { setActual(error.effectivePath); try { await controller.refreshSettings(); } catch { /* 此次保存失败的反馈已经覆盖读取失败，保留返回的实际路径。 */ } } setResult({ text: error instanceof Error ? error.message : '保存失败，请重试。', error: true }); console.error(JSON.stringify({ level: 'ERROR', operation: 'save-directory', resource_id: kind })); } finally { setBusy(false); } }
  return <section className="settings-card directory-card" aria-label={`${label}目录设置`}><h2>{label}目录</h2><p className="effective-path">当前生效：<span data-testid={`${kind}-effective`}>{actual}</span></p><p className="effective-path">已保存（重启后使用）：<span data-testid={`${kind}-saved`}>{input}</span></p><div className="directory-row"><label className="visually-hidden" htmlFor={kind}>{label}目录</label><input className="path-input" id={kind} value={input} disabled={busy} onChange={event => setInput(event.target.value)} spellCheck={false} /><Button secondary disabled={busy} onClick={() => void choose()}>选择目录</Button><Button busy={busy} onClick={() => void save()}>保存</Button></div><p className="hint">保存后重启生效。旧文件保留，不自动迁移或删除。</p>{result && <Feedback kind={result.error ? 'error' : 'success'}>{result.text}</Feedback>}</section>;
}

/** 主题保存成功后应用；没有不可用的媒体上传入口。 */
function Appearance({ controller, settings }: { controller: Controller; settings: Settings }) {
  const [busy, setBusy] = useState(false);
  /** 原子保存主题，读取最新关闭偏好避免覆盖另一项设置。 */
  async function choose(theme: Theme) { if (busy) return; setBusy(true); try { const current = await controller.ports.readSettings(); await controller.ports.savePreferences({ theme, closeBehavior: current.closeBehavior }); await controller.refreshSettings(); controller.clearError(); } catch (error) { controller.report(error, 'theme'); } finally { setBusy(false); } }
  return <><section className="settings-card"><h2>配色</h2><div className="theme-grid">{Object.entries(themes).map(([key, theme]) => <button key={key} className="theme-option" aria-pressed={settings.theme === key} disabled={busy} onClick={() => void choose(key as Theme)} style={themeStyle(key as Theme) as CSSProperties}><div className="theme-mini" aria-hidden="true"><i /><i /></div>{theme.name}{settings.theme === key && ' ✓'}</button>)}</div></section><section className="settings-card"><h2>背景</h2><p>● &nbsp;纯色 &nbsp; 跟随当前配色，让内容清楚、舒适。</p></section><p className="hint" role="status">{busy ? '正在保存配色…' : '配色即时保存，下次打开依然是你喜欢的样子。'}</p></>;
}

/** Windows 桌面模拟使用系统风格，并呈现退出清理的真实结果。 */
function Desktop({ controller, state, action }: { controller: Controller; state: AppState; action: (operation: string, execute: () => void | Promise<void>) => Promise<void> }) {
  const runtime = state.runtime!;
  const titles = { running: runtime.visibility === 'minimized' ? '已最小化' : '已隐藏到托盘', stopping: '正在退出 TimeCue…', stopped: 'TimeCue 已退出', 'cleanup-failed': '系统通知清理失败' };
  return <div className="desktop"><p className="desktop-header">WINDOWS 桌面模拟</p><h1>{titles[runtime.lifecycle]}</h1><p>{runtime.lifecycle === 'running' ? '后台提醒继续运行，你可以从托盘重新打开窗口。' : runtime.lifecycle === 'stopped' ? '后台提醒已停止，TimeCue 投递的系统通知已清理。' : runtime.lifecycle === 'stopping' ? '停止后台提醒 → 清理已投递通知 → 释放资源。' : '后台提醒已停止，但系统通知仍有残留。请重试清理。'}</p>
    {state.error && <Feedback kind="error">{state.error}</Feedback>}
    <div className="system-notifications">{runtime.notifications.map(id => <button className="system-toast" key={id} onClick={() => void action('notification-activate', () => controller.ports.activate(id))}><small>TimeCue &nbsp; ···</small><strong>{id === 'water' ? '喝杯水，休息一下' : '站起来，活动活动肩颈'}</strong><span>点击打开 TimeCue 查看详情</span></button>)}</div>
    <div className="desktop-bottom">{runtime.lifecycle === 'running' && <div className="tray-menu" aria-label="TimeCue 托盘菜单"><Button secondary onClick={() => void action('tray-open', () => controller.ports.open())}>打开</Button><Button secondary onClick={() => void action('exit', () => controller.ports.exit())}>退出</Button></div>}
    <div className="taskbar"><Button secondary disabled={runtime.lifecycle === 'stopping'} onClick={() => void action('launch', () => controller.ports.launch())}>{runtime.lifecycle === 'stopped' ? '启动 TimeCue' : '再次启动 TimeCue'}</Button>{runtime.lifecycle === 'cleanup-failed' && <Button secondary onClick={() => void action('cleanup-retry', () => controller.ports.exit())}>重试清理</Button>}{runtime.visibility === 'minimized' && runtime.lifecycle === 'running' && <Button secondary onClick={() => void action('restore', () => controller.ports.open())}>恢复窗口</Button>}<time>14:30 &nbsp; 2026/9/15</time></div></div>
  </div>;
}
