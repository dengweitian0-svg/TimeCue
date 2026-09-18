import { ScheduleError, toScheduleError, type Activation, type AppPort, type CloseBehavior, type ConfigProperty, type DirectoryKind, type LogLevel, type LogLevelSettings, type RecoveryEvent, type ReminderDetail, type ReminderIdentity, type ReminderPage, type RuntimeStatus, type Schedule, type ScheduleBounds, type ScheduleDraft, type ScheduleHidden, type SchedulePage, type StorageDirectories, type StorageDirectory, type Theme } from './contracts';
import { changeDraftType, draftRequest, emptyDraft } from './schedule-model';

export type Page = 'schedules' | 'reminders' | 'settings';
export interface AppState {
  page: Page;
  schedules: SchedulePage;
  reminders: ReminderPage;
  reminderLoading: boolean;
  reminderDetailLoading: boolean;
  reminderConfirming: boolean;
  reminderError: ScheduleError | null;
  activeReminder: ReminderIdentity | null;
  reminderDetail: ReminderDetail | null;
  bounds: ScheduleBounds | null;
  draft: ScheduleDraft | null;
  deleting: Schedule | null;
  busy: boolean;
  loading: boolean;
  error: ScheduleError | null;
  notice: string;
  runtime: RuntimeStatus | null;
  closeBehavior: CloseBehavior;
  theme: Theme;
  themeLoading: boolean;
  logLevel: LogLevelSettings;
  storageDirectories: StorageDirectories | null;
  activation: Activation | null;
}

const initialPage: SchedulePage = { items: [], nextCursor: '', limit: 20 };
const initialReminderPage: ReminderPage = { items: [], nextCursor: '', limit: 20 };

/** 以稳定身份比较两条提醒，避免依赖列表位置或可变正文。 */
export function sameReminder(left: ReminderIdentity | null | undefined, right: ReminderIdentity | null | undefined): boolean {
  return Boolean(left && right && left.scheduleId === right.scheduleId && left.originalDate === right.originalDate);
}

/** 返回当前提醒同一实际开始时刻的可切换项；无法重建时间的项不被错误合并。 */
export function groupReminders(items: ReminderDetail[], active: ReminderIdentity | null): ReminderDetail[] {
  const selected = items.find(item => sameReminder(item, active));
  if (!selected || selected.startUnix === null) return selected ? [selected] : [];
  return items.filter(item => item.startUnix === selected.startUnix);
}

/** 编排生产前端的日程、偏好、运行状态和激活用例，不拥有后端持久状态。 */
export class Controller {
  private state: AppState = { page: 'schedules', schedules: initialPage, reminders: initialReminderPage, reminderLoading: true, reminderDetailLoading: false, reminderConfirming: false, reminderError: null, activeReminder: null, reminderDetail: null, bounds: null, draft: null, deleting: null, busy: false, loading: true, error: null, notice: '', runtime: null, closeBehavior: 'tray', theme: 'peach', themeLoading: true, logLevel: { effectiveLevel: 'info', savedLevel: 'info', restartRequired: false }, storageDirectories: null, activation: null };
  private readonly listeners = new Set<() => void>();
  private readonly unsubscribeActivation: () => void;
  private readonly unsubscribeScheduleHidden: () => void;
  private readonly unsubscribeRecovery: () => void;
  private disposed = false;
  private lastSequence = 0;
  private readVersion = 0;
  private reminderReadVersion = 0;
  private reminderDetailVersion = 0;
  private reminderActionVersion = 0;
  private readonly hiddenScheduleIDs = new Set<string>();

  /** 注入窄平台端口，并从构造时开始接收激活事件。 */
  constructor(public readonly port: AppPort) { this.unsubscribeActivation = port.subscribeActivation(this.onActivation); this.unsubscribeScheduleHidden = port.subscribeScheduleHidden(this.onScheduleHidden); this.unsubscribeRecovery = port.subscribeRecovery?.(this.onRecovery) ?? (() => undefined); }

  /** 返回稳定快照供 React 的 useSyncExternalStore 使用。 */
  snapshot = (): AppState => this.state;

  /** 注册状态监听器。 */
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };

  /** 释放事件订阅和端口资源。 */
  dispose(): void { this.disposed = true; this.unsubscribeActivation(); this.unsubscribeScheduleHidden(); this.unsubscribeRecovery(); this.port.dispose?.(); this.listeners.clear(); }

  /** 初始化窗口状态、日程列表、提醒列表和前端激活握手。 */
  async initialize(): Promise<void> {
    try {
      let pendingError: unknown;
      let themeError: ScheduleError | null = null;
      const initialReminderVersion = this.reminderReadVersion;
      const initialPending = this.port.listPendingReminders({ limit: initialReminderPage.limit }).catch(error => { pendingError = error; return initialReminderPage; });
      const initialTheme = this.port.getTheme().catch(error => { themeError = toScheduleError(error); return 'peach' as Theme; });
      const [desktop, schedules, bounds, closeBehavior, theme, logLevel, storageDirectories, runtime, reminders] = await Promise.all([this.port.getDesktopStatus(), this.port.listSchedules(), this.port.getScheduleTimeBounds(), this.port.getCloseBehavior(), initialTheme, this.port.getLogLevel(), this.port.getStorageDirectories(), this.port.getRuntimeStatus(), initialPending]);
      if (this.disposed) return;
      const initialActivation = desktop.activation && desktop.activation.sequence > this.lastSequence ? desktop.activation : null;
      this.lastSequence = Math.max(this.lastSequence, desktop.activation?.sequence ?? 0);
      if (this.reminderReadVersion === initialReminderVersion) this.update({ reminders, reminderLoading: false });
      const visibleSchedules = this.filterHiddenSchedules(schedules);
      this.update({ runtime: runtime ?? desktop.runtime, activation: desktop.activation, schedules: visibleSchedules, bounds, closeBehavior, theme, themeLoading: false, logLevel, storageDirectories, loading: false, error: themeError });
      if (initialActivation) void this.activateReminder(initialActivation);
      if (pendingError && this.reminderReadVersion === initialReminderVersion) this.failReminder(pendingError, false);
      const pendingActivation = await this.port.frontendReady(this.lastSequence);
      if (pendingActivation) this.onActivation(pendingActivation);
    } catch (error) {
      if (!this.disposed) {
        this.update({ themeLoading: false, loading: false });
        this.fail(error);
      }
    }
  }

  /** 切换页面并清除上一页的短暂提示。 */
  setPage(page: Page): void { if (!this.state.busy) this.update({ page, error: null, notice: '' }); }

  /** 按消息内容清除中央 Toast，避免旧消息的定时器误清除新消息。 */
  clearNotice(expected?: string): void { if (expected !== undefined && this.state.notice !== expected) return; this.update({ notice: '' }); }

  /** 选中列表中的提醒并异步读取最新详情，不修改确认状态。 */
  selectReminder(identity: ReminderIdentity): void {
    if (this.state.busy) return;
    const stableIdentity: ReminderIdentity = { scheduleId: identity.scheduleId, originalDate: identity.originalDate };
    this.reminderActionVersion++;
    this.update({ page: 'reminders', activeReminder: stableIdentity, reminderDetail: null, reminderDetailLoading: true, reminderConfirming: false, reminderError: null, notice: '' });
    void this.readReminderDetail(stableIdentity);
  }

  /** 重新读取第一页待处理提醒，保留当前草稿和稳定选中身份。 */
  async refreshReminders(): Promise<ReminderPage | null> {
    const version = ++this.reminderReadVersion;
    this.update({ reminderLoading: true, reminderError: null });
    try {
      const page = await this.port.listPendingReminders({ limit: this.state.reminders.limit });
      if (!this.disposed && version === this.reminderReadVersion) this.update({ reminders: page, reminderLoading: false });
      return page;
    } catch (error) {
      if (!this.disposed && version === this.reminderReadVersion) this.failReminder(error, false);
      return null;
    }
  }

  /** 追加待处理提醒下一页；游标失效时回到第一页并重新读取。 */
  async nextReminderPage(): Promise<void> {
    const current = this.state.reminders;
    if (this.state.busy || this.state.reminderLoading || !current.nextCursor) return;
    const version = ++this.reminderReadVersion;
    this.update({ reminderLoading: true, reminderError: null });
    try {
      const page = await this.port.listPendingReminders({ limit: current.limit, cursor: current.nextCursor });
      if (!this.disposed && version === this.reminderReadVersion) this.update({ reminders: { ...page, items: [...current.items, ...page.items] }, reminderLoading: false });
    } catch (error) {
      if (this.disposed || version !== this.reminderReadVersion) return;
      if (toScheduleError(error).code === 'STALE_CURSOR') await this.refreshReminders();
      else this.failReminder(error, false);
    }
  }

  /** 确认当前提醒，成功后刷新列表并优先定位同刻的下一条提醒。 */
  async confirmReminder(): Promise<void> {
    const identity = this.state.activeReminder;
    const detail = this.state.reminderDetail;
    if (!identity || !detail || detail.status !== 'pending' || this.state.busy || this.state.reminderConfirming) return;
    const actionVersion = ++this.reminderActionVersion;
    const sameTime = groupReminders(this.state.reminders.items, identity);
    const currentIndex = sameTime.findIndex(item => sameReminder(item, identity));
    const preferredNext = currentIndex >= 0 ? sameTime[currentIndex + 1] : undefined;
    this.update({ reminderConfirming: true, reminderError: null });
    try {
      const result = await this.port.confirmReminder(identity);
      if (this.disposed || actionVersion !== this.reminderActionVersion || !sameReminder(this.state.activeReminder, identity)) return;
      const page = await this.refreshReminders();
      if (this.disposed || actionVersion !== this.reminderActionVersion || !sameReminder(this.state.activeReminder, identity)) return;
      const next = preferredNext && page?.items.some(item => sameReminder(item, preferredNext)) ? preferredNext : page?.items.find(item => item.startUnix !== null && detail.startUnix !== null && item.startUnix === detail.startUnix && !sameReminder(item, identity)) ?? page?.items[0];
      if (next) {
        this.selectReminder(next);
      } else {
        this.update({ activeReminder: null, reminderDetail: null, reminderDetailLoading: false, reminderConfirming: false, notice: result.changed ? '提醒已确认。' : '这条提醒已经确认。' });
      }
    } catch (error) {
      if (!this.disposed && actionVersion === this.reminderActionVersion && sameReminder(this.state.activeReminder, identity)) this.update({ reminderConfirming: false, reminderError: toScheduleError(error) });
    }
  }

  /** 进入创建页面并用后端当前本地日期初始化重复日程的隐藏生效日期。 */
  async beginCreate(): Promise<void> {
    if (this.state.busy) return;
    const initial = emptyDraft();
    this.update({ page: 'schedules', draft: initial, error: null, notice: '' });
    await this.refreshBounds();
    const bounds = this.state.bounds;
    if (bounds && this.state.draft === initial) this.update({ draft: { ...initial, effectiveDate: bounds.min.slice(0, 10) } });
  }

  /** 更新未提交草稿，不让输入直接绕过后端端口。 */
  editDraft(patch: Partial<ScheduleDraft>): void { if (!this.state.busy && this.state.draft) this.update({ draft: { ...this.state.draft, ...patch }, error: null }); }

  /** 切换日程类型并从现有端点或重复字段推导目标表单，保留其他草稿内容。 */
  changeDraftType(type: ScheduleDraft['type']): void { if (!this.state.busy && this.state.draft) this.update({ draft: changeDraftType(this.state.draft, type), error: null }); }

  /** 取消创建并丢弃未提交草稿。 */
  cancelCreate(): void { if (!this.state.busy) this.update({ draft: null, error: null }); }

  /** 提交日程，失败时保留草稿与字段错误。 */
  async saveCreate(): Promise<void> {
    const draft = this.state.draft; if (!draft || this.state.busy) return;
    let request; try { request = draftRequest(draft); } catch (error) { this.fail(error); return; }
    this.update({ busy: true, error: null, notice: '' });
    try {
      const saved = await this.port.createSchedule(request); if (this.disposed) return;
      this.update({ schedules: { ...this.state.schedules, items: [saved, ...this.state.schedules.items] }, draft: null, notice: `已保存“${saved.title}”。到时会提醒你。` });
    } catch (error) { if (!this.disposed) this.fail(error); }
    finally { if (!this.disposed) this.update({ busy: false }); }
  }

  /** 打开删除确认对象，锁定其版本直到用户作出决定。 */
  askDelete(schedule: Schedule): void { if (!this.state.busy) this.update({ deleting: schedule, error: null, notice: '' }); }

  /** 关闭删除确认且不修改后端。 */
  cancelDelete(): void { if (!this.state.busy) this.update({ deleting: null, error: null }); }

  /** 按确认时的版本删除，旧版本和缺失对象交给明确恢复入口。 */
  async confirmDelete(): Promise<void> {
    const item = this.state.deleting; if (!item || this.state.busy || this.state.error?.code === 'STALE_REVISION' || this.state.error?.code === 'NOT_FOUND') return;
    this.update({ busy: true, error: null });
    try {
      await this.port.deleteSchedule(item.id, item.revision); if (this.disposed) return;
      this.update({ deleting: null, schedules: { ...this.state.schedules, items: this.state.schedules.items.filter(value => value.id !== item.id) }, notice: `已删除“${item.title}”。` });
    } catch (error) { if (!this.disposed) this.fail(error); }
    finally { if (!this.disposed) this.update({ busy: false }); }
  }

  /** 读取最新删除对象和列表，让用户重新核对后再提交。 */
  async recoverDelete(): Promise<void> {
    const item = this.state.deleting; if (!item || this.state.busy) return;
    const missing = this.state.error?.code === 'NOT_FOUND';
    this.update({ busy: true, error: null });
    try {
      const current = missing ? null : await this.port.getSchedule(item.id);
      const schedules = await this.port.listSchedules({ limit: this.state.schedules.limit });
      if (!this.disposed) this.update({ deleting: current, schedules: this.filterHiddenSchedules(schedules), notice: current ? '已读取最新内容，请重新核对后再删除。' : '列表已刷新，这条日程已不存在。' });
    } catch (error) { if (!this.disposed) this.fail(error); }
    finally { if (!this.disposed) this.update({ busy: false }); }
  }

  /** 重新读取日程列表和边界，避免迟到响应覆盖较新的操作。 */
  async refreshSchedules(): Promise<void> {
    const version = ++this.readVersion; this.update({ loading: true });
    try { const [schedules, bounds] = await Promise.all([this.port.listSchedules({ limit: this.state.schedules.limit }), this.port.getScheduleTimeBounds()]); if (version === this.readVersion && !this.disposed) this.update({ schedules: this.filterHiddenSchedules(schedules), bounds, loading: false, error: null }); }
    catch (error) { if (version === this.readVersion && !this.disposed) { this.fail(error); this.update({ loading: false }); } }
  }

  /** 加载下一页日程，保留后端返回的分页元数据。 */
  async nextPage(): Promise<void> { const current = this.state.schedules; if (this.state.busy || !current.nextCursor) return; const version = ++this.readVersion; try { const page = await this.port.listSchedules({ limit: current.limit, cursor: current.nextCursor }); if (!this.disposed && version === this.readVersion) this.update({ schedules: { ...page, items: [...current.items, ...this.filterHiddenSchedules(page).items] } }); } catch (error) { if (!this.disposed && version === this.readVersion) this.fail(error); } }

  /** 重新取得动态单次日程边界。 */
  async refreshBounds(): Promise<void> { try { this.update({ bounds: await this.port.getScheduleTimeBounds() }); } catch (error) { if (!this.disposed) this.fail(error); } }

  /** 读取关闭行为；保存失败不改变本地已生效值。 */
  async saveCloseBehavior(value: CloseBehavior): Promise<void> { if (this.state.busy) return; this.update({ busy: true, error: null, notice: '' }); try { const saved = await this.port.saveCloseBehavior(value); if (!this.disposed) this.update({ closeBehavior: saved, notice: '关闭偏好已保存。' }); } catch (error) { if (!this.disposed) this.fail(error); } finally { if (!this.disposed) this.update({ busy: false }); } }

  /** 保存主题；重复选择不触发端口调用，失败时保留旧主题。 */
  async saveTheme(value: Theme): Promise<void> {
    if (this.state.busy || value === this.state.theme) return;
    this.update({ busy: true, error: null, notice: '' });
    try {
      const saved = await this.port.saveTheme(value);
      if (!this.disposed) this.update({ theme: saved, themeLoading: false, notice: '主题已保存。' });
    } catch (error) {
      if (!this.disposed) this.fail(error);
    } finally {
      if (!this.disposed) this.update({ busy: false });
    }
  }

  /** 保存日志级别；保存成功但重启前继续使用当前生效级别。 */
  async saveLogLevel(value: LogLevel): Promise<void> {
    if (this.state.busy) return;
    this.update({ busy: true, error: null, notice: '' });
    try {
      const saved = await this.port.saveLogLevel(value);
      if (!this.disposed) this.update({ logLevel: saved, notice: saved.restartRequired ? '日志级别已保存，重启后生效。' : '日志级别已保存。' });
    } catch (error) {
      if (!this.disposed) this.fail(error);
    } finally {
      if (!this.disposed) this.update({ busy: false });
    }
  }

  /** 显式恢复单个配置属性默认值，并刷新设置页的受控视图。 */
  async restoreConfigDefault(property: ConfigProperty): Promise<void> {
    if (this.state.busy) return;
    this.update({ busy: true, error: null, notice: '' });
    try {
      await this.port.restoreConfigDefault(property);
      const [closeBehavior, theme, logLevel, storageDirectories] = await Promise.all([this.port.getCloseBehavior(), this.port.getTheme(), this.port.getLogLevel(), this.port.getStorageDirectories()]);
      if (!this.disposed) {
        const notice = property === 'window.close_behavior' ? '窗口关闭行为已恢复默认。' : property === 'theme' ? '主题已恢复默认。' : '配置已恢复默认，重启后生效。';
        this.update({ closeBehavior, theme, themeLoading: false, logLevel, storageDirectories, notice });
      }
    } catch (error) {
      if (!this.disposed) this.fail(error);
    } finally {
      if (!this.disposed) this.update({ busy: false });
    }
  }

  /** 通过设置端口打开目录选择器；取消时返回 null 且不修改输入。 */
  async chooseDirectory(kind: DirectoryKind): Promise<string | null> {
    if (this.state.busy) return null;
    this.update({ busy: true, error: null, notice: '' });
    try { return await this.port.chooseDirectory(kind); }
    catch (error) { if (!this.disposed) this.fail(error); return null; }
    finally { if (!this.disposed) this.update({ busy: false }); }
  }

  /** 保存单个目录配置，成功后刷新 effective/saved 对照，失败时保留用户输入。 */
  async saveStorageDirectory(kind: DirectoryKind, path: string): Promise<StorageDirectory | null> {
    if (this.state.busy) return null;
    this.update({ busy: true, error: null, notice: '' });
    try {
      const saved = await this.port.saveStorageDirectory(kind, path);
      if (!this.disposed) {
        const current = this.state.storageDirectories;
        const storageDirectories = current ? { ...current, [kind]: saved } : null;
        this.update({ storageDirectories, notice: saved.restartRequired ? '目录配置已保存，重启后生效。' : '目录配置已保存。' });
      }
      return saved;
    } catch (error) {
      if (!this.disposed) this.fail(error);
      return null;
    } finally { if (!this.disposed) this.update({ busy: false }); }
  }

  /** 重新读取两个目录的实际/保存状态，避免设置页依赖本地猜测。 */
  async refreshStorageDirectories(): Promise<void> {
    try { const storageDirectories = await this.port.getStorageDirectories(); if (!this.disposed) this.update({ storageDirectories }); }
    catch (error) { if (!this.disposed) this.fail(error); }
  }

  /** 读取运行状态并尝试完成通知清理。 */
  async retryCleanup(): Promise<void> { if (this.state.busy) return; this.update({ busy: true, error: null }); try { await this.port.retryCleanup(); const status = await this.port.getRuntimeStatus(); if (!this.disposed) this.update({ runtime: status, notice: '通知清理已重试。' }); } catch (error) { if (!this.disposed) this.fail(error); } finally { if (!this.disposed) this.update({ busy: false }); } }

  /** 处理最新激活并按 sequence 去重，定位提醒详情且不清除日程草稿。 */
  private onActivation = (activation: Activation): void => {
    if (this.disposed || activation.sequence <= this.lastSequence) return;
    this.lastSequence = activation.sequence;
    this.reminderActionVersion++;
    this.update({ activation, page: 'reminders', activeReminder: activation.targetId ? { scheduleId: activation.targetId, originalDate: activation.originalDate } : null, reminderDetail: null, reminderDetailLoading: Boolean(activation.targetId), reminderConfirming: false, reminderError: null, notice: '' });
    if (activation.targetId) void this.activateReminder(activation);
  };

  /** 接收恢复子状态；完成事件重新读取待处理列表，失败时保留已有列表。 */
  private onRecovery = (event: RecoveryEvent): void => {
    if (this.disposed) return;
    const runtime = this.state.runtime ? { ...this.state.runtime, recoveryStatus: event.status } : { visibility: 'visible' as const, lifecycle: 'running' as const, ready: false, recoveryStatus: event.status };
    this.update({ runtime, notice: event.status === 'failed' ? '提醒恢复未完成，请检查日志。' : this.state.notice });
    if (event.status === 'idle') void this.refreshReminders();
  };

  /** 收到提交成功事件后只从本地日程投影移除目标，并使旧列表响应失效。 */
  private onScheduleHidden = (event: ScheduleHidden): void => {
    if (this.disposed) return;
    this.hiddenScheduleIDs.add(event.scheduleId);
    this.readVersion++;
    this.update({ schedules: { ...this.state.schedules, items: this.state.schedules.items.filter(item => item.id !== event.scheduleId) }, loading: false });
  };

  /** 按事件记录过滤列表响应，保护握手/刷新交错期间的本地隐藏事实。 */
  private filterHiddenSchedules(page: SchedulePage): SchedulePage { return { ...page, items: page.items.filter(item => !this.hiddenScheduleIDs.has(item.id)) }; }

  /** 处理前端未就绪后补发的激活，分别刷新列表和稳定身份详情。 */
  private async activateReminder(activation: Activation): Promise<void> {
    if (!activation.targetId || this.disposed) return;
    const identity: ReminderIdentity = { scheduleId: activation.targetId, originalDate: activation.originalDate };
    this.update({ page: 'reminders', activeReminder: identity, reminderDetail: null, reminderDetailLoading: true, reminderConfirming: false });
    void this.refreshReminders();
    await this.readReminderDetail(identity);
  }

  /** 使用请求序号保护详情，迟到响应不能覆盖更新的激活或选择。 */
  private async readReminderDetail(identity: ReminderIdentity): Promise<void> {
    const version = ++this.reminderDetailVersion;
    try {
      const detail = await this.port.getReminderDetail(identity);
      if (!this.disposed && version === this.reminderDetailVersion && sameReminder(this.state.activeReminder, identity)) this.update({ reminderDetail: detail, reminderDetailLoading: false, reminderError: null });
    } catch (error) {
      if (!this.disposed && version === this.reminderDetailVersion && sameReminder(this.state.activeReminder, identity)) this.failReminder(error, true);
    }
  };

  /** 统一状态变更通知。 */
  private update(patch: Partial<AppState>): void { if (this.disposed) return; this.state = { ...this.state, ...patch }; for (const listener of this.listeners) listener(); }

  /** 将平台拒绝值转换成可绑定字段的错误。 */
  private fail(error: unknown): void { const failure = toScheduleError(error); this.update({ error: failure, notice: '' }); }

  /** 将提醒端口错误隔离到提醒页面，并保留当前详情以便显示稳定状态。 */
  private failReminder(error: unknown, detail: boolean): void { const failure = toScheduleError(error); this.update({ reminderError: failure, reminderLoading: detail ? this.state.reminderLoading : false, reminderDetailLoading: detail ? false : this.state.reminderDetailLoading }); }
}
