import { ErrorBanner } from '../../shared/components/ErrorBanner';
import { LoadingCard } from '../../shared/components/LoadingCard';
import type { ReminderDetail, ReminderIdentity, ReminderPage } from './contracts';
import { groupReminders } from './model';
import type { ScheduleError } from '../../shared/contracts/errors';

/** 提醒页面所需的最小操作集合，Controller 通过结构类型满足该接口。 */
export interface ReminderActions {
  refreshReminders(): Promise<ReminderPage | null>;
  nextReminderPage(): Promise<void>;
  selectReminder(identity: ReminderIdentity): void;
  confirmReminder(): Promise<void>;
}

/** 提醒页面所需的状态投影，不暴露日程和设置领域状态。 */
export interface ReminderViewState {
  reminders: ReminderPage;
  reminderLoading: boolean;
  reminderDetailLoading: boolean;
  reminderConfirming: boolean;
  reminderError: ScheduleError | null;
  activeReminder: ReminderIdentity | null;
  reminderDetail: ReminderDetail | null;
}

/** 待处理提醒页面只读取列表和当前详情，不在查看或切换时提交确认。 */
export function RemindersPage({ actions, state }: { actions: ReminderActions; state: ReminderViewState }) {
  const group = groupReminders(state.reminders.items, state.activeReminder);
  return <section className="page-section reminder-page"><div className="page-heading"><div><p className="kicker">PENDING REMINDERS</p><h1>待处理提醒</h1><p>从通知回到这里，查看仍需要你处理的消息。</p></div></div>
    {state.reminderError && <ErrorBanner error={state.reminderError} />}
    <div className="list-toolbar"><span>{state.reminderLoading && !state.reminders.items.length ? '正在读取…' : `${state.reminders.items.length} 条待处理提醒`}</span><button className="text-button" onClick={() => void actions.refreshReminders()} disabled={state.reminderLoading}>重新加载</button></div>
    {state.reminderLoading && !state.reminders.items.length ? <LoadingCard /> : state.reminders.items.length ? <div className="reminder-layout"><div><div className="reminder-list">{state.reminders.items.map(item => <ReminderCard key={`${item.scheduleId}/${item.originalDate}`} item={item} active={item.scheduleId === state.activeReminder?.scheduleId && item.originalDate === state.activeReminder.originalDate} onClick={() => actions.selectReminder(item)} />)}</div>{state.reminders.nextCursor && <button className="secondary-button pagination" onClick={() => void actions.nextReminderPage()} disabled={state.reminderLoading}>加载更多</button>}</div><ReminderDetailPanel actions={actions} detail={state.reminderDetail} group={group} loading={state.reminderDetailLoading} confirming={state.reminderConfirming} active={state.activeReminder} /></div> : <EmptyReminders />}
  </section>;
}

/** 显示待处理列表项的稳定身份、状态和可用时间。 */
function ReminderCard({ item, active, onClick }: { item: ReminderDetail; active: boolean; onClick: () => void }) {
  const label = item.contentStatus === 'available' && item.title ? item.title : '提醒内容暂不可用';
  return <button className={`reminder-card${active ? ' active' : ''}`} onClick={onClick} aria-current={active ? 'true' : undefined}><span className="reminder-card-mark">◷</span><span className="reminder-card-body"><strong>{label}</strong><small>{item.startUnix === null ? item.originalDate : formatReminderTime(item.startUnix)}</small></span><span className="reminder-card-arrow">›</span></button>;
}

/** 呈现当前提醒的只读正文、状态和同刻切换入口。 */
function ReminderDetailPanel({ actions, detail, group, loading, confirming, active }: { actions: ReminderActions; detail: ReminderDetail | null; group: ReminderDetail[]; loading: boolean; confirming: boolean; active: ReminderIdentity | null }) {
  return <section className="reminder-detail-card" aria-label="提醒详情"><p className="card-eyebrow">CURRENT REMINDER</p>{loading ? <LoadingCard /> : !detail || !active ? <div className="detail-empty"><h2>选择一条提醒</h2><p>从左侧列表选择消息，查看它当前可重建的详情。</p></div> : <><div className="reminder-detail-heading"><div><h2>{detail.title ?? reminderStatusTitle(detail.status)}</h2><p>{detail.startUnix === null ? detail.originalDate : formatReminderTime(detail.startUnix)}</p></div><span className={`reminder-status ${detail.status}`}>{reminderStatusLabel(detail.status)}</span></div>{detail.status === 'pending' && detail.contentStatus === 'available' ? <div className="reminder-copy"><p>{detail.notes || '这条提醒没有附加正文。'}</p></div> : <div className="reminder-unavailable" role="status"><strong>{detail.status === 'confirmed' ? '这条提醒已经确认。' : detail.status === 'missing' ? '这条提醒已不存在。' : detail.status === 'not_processable' ? '这条提醒当前不可处理。' : '提醒内容暂不可用。'}</strong><p>{detail.status === 'pending' ? '系统保留了提醒身份，但无法从当前日程事实重建正文。' : '页面不会用当前或空白内容代替历史正文。'}</p></div>}{detail.status === 'pending' && <button className="primary-button reminder-confirm-button" disabled={confirming} onClick={() => void actions.confirmReminder()}>{confirming ? '正在确认…' : detail.contentStatus === 'available' ? '确认提醒' : '确认已投递提醒'}</button>}{group.length > 1 && <div className="same-time-group"><p className="card-eyebrow">SAME TIME</p><div>{group.map(item => <button key={`${item.scheduleId}/${item.originalDate}`} className={`text-button${item.scheduleId === active.scheduleId && item.originalDate === active.originalDate ? ' selected' : ''}`} onClick={() => actions.selectReminder(item)}>{item.contentStatus === 'available' && item.title ? item.title : `提醒 ${item.scheduleId}`}</button>)}</div></div>}</>}</section>;
}

/** 将提醒状态转换成页面可读标题。 */
function reminderStatusTitle(status: ReminderDetail['status']): string { return status === 'confirmed' ? '已确认的提醒' : status === 'missing' ? '已不存在的提醒' : status === 'not_processable' ? '不可处理的提醒' : '待处理提醒'; }

/** 将提醒状态转换成短标签。 */
function reminderStatusLabel(status: ReminderDetail['status']): string { return status === 'pending' ? '待处理' : status === 'confirmed' ? '已确认' : status === 'missing' ? '已不存在' : '不可处理'; }

/** 将 Unix 秒转换为本地化的提醒时间。 */
function formatReminderTime(unix: number): string { return new Date(unix * 1000).toLocaleString('zh-CN', { month: 'long', day: 'numeric', hour: '2-digit', minute: '2-digit' }); }

/** 待处理提醒为空时明确说明没有需要确认的消息。 */
function EmptyReminders() { return <div className="empty-card"><span className="empty-mark">✓</span><h2>没有待处理提醒</h2><p>新的系统通知到达后，会在这里保留到你确认。</p></div>; }
