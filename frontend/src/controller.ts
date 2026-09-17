import { ScheduleError, toScheduleError, type Activation, type AppPort, type CloseBehavior, type DirectoryKind, type RuntimeStatus, type Schedule, type ScheduleBounds, type ScheduleDraft, type SchedulePage, type StorageDirectories, type StorageDirectory } from './contracts';
import { draftRequest, emptyDraft } from './schedule-model';

export type Page = 'schedules' | 'settings';
export interface AppState {
  page: Page;
  schedules: SchedulePage;
  bounds: ScheduleBounds | null;
  draft: ScheduleDraft | null;
  deleting: Schedule | null;
  busy: boolean;
  loading: boolean;
  error: ScheduleError | null;
  notice: string;
  runtime: RuntimeStatus | null;
  closeBehavior: CloseBehavior;
  storageDirectories: StorageDirectories | null;
  activation: Activation | null;
}

const initialPage: SchedulePage = { items: [], nextCursor: '', limit: 20 };

/** 编排生产前端的日程、偏好、运行状态和激活用例，不拥有后端持久状态。 */
export class Controller {
  private state: AppState = { page: 'schedules', schedules: initialPage, bounds: null, draft: null, deleting: null, busy: false, loading: true, error: null, notice: '', runtime: null, closeBehavior: 'tray', storageDirectories: null, activation: null };
  private readonly listeners = new Set<() => void>();
  private readonly unsubscribeActivation: () => void;
  private disposed = false;
  private lastSequence = 0;
  private readVersion = 0;

  /** 注入窄平台端口，并从构造时开始接收激活事件。 */
  constructor(public readonly port: AppPort) { this.unsubscribeActivation = port.subscribeActivation(this.onActivation); }

  /** 返回稳定快照供 React 的 useSyncExternalStore 使用。 */
  snapshot = (): AppState => this.state;

  /** 注册状态监听器。 */
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => this.listeners.delete(listener); };

  /** 释放事件订阅和端口资源。 */
  dispose(): void { this.disposed = true; this.unsubscribeActivation(); this.port.dispose?.(); this.listeners.clear(); }

  /** 初始化窗口状态、日程列表和前端激活握手。 */
  async initialize(): Promise<void> {
    try {
      const [desktop, schedules, bounds, closeBehavior, storageDirectories] = await Promise.all([this.port.getDesktopStatus(), this.port.listSchedules(), this.port.getScheduleTimeBounds(), this.port.getCloseBehavior(), this.port.getStorageDirectories()]);
      if (this.disposed) return;
      this.lastSequence = desktop.activation?.sequence ?? 0;
      this.update({ runtime: desktop.runtime, activation: desktop.activation, schedules, bounds, closeBehavior, storageDirectories, loading: false });
      const pending = await this.port.frontendReady(this.lastSequence);
      if (pending) this.onActivation(pending);
    } catch (error) {
      if (!this.disposed) this.fail(error);
    }
  }

  /** 切换页面并清除上一页的短暂提示。 */
  setPage(page: Page): void { if (!this.state.busy) this.update({ page, error: null, notice: '' }); }

  /** 进入创建页面并取得最新时间边界。 */
  async beginCreate(): Promise<void> { if (this.state.busy) return; this.update({ page: 'schedules', draft: emptyDraft(), error: null, notice: '' }); if (!this.state.bounds) await this.refreshBounds(); }

  /** 更新未提交草稿，不让输入直接绕过后端端口。 */
  editDraft(patch: Partial<ScheduleDraft>): void { if (!this.state.busy && this.state.draft) this.update({ draft: { ...this.state.draft, ...patch }, error: null }); }

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
      if (!this.disposed) this.update({ deleting: current, schedules, notice: current ? '已读取最新内容，请重新核对后再删除。' : '列表已刷新，这条日程已不存在。' });
    } catch (error) { if (!this.disposed) this.fail(error); }
    finally { if (!this.disposed) this.update({ busy: false }); }
  }

  /** 重新读取日程列表和边界，避免迟到响应覆盖较新的操作。 */
  async refreshSchedules(): Promise<void> {
    const version = ++this.readVersion; this.update({ loading: true });
    try { const [schedules, bounds] = await Promise.all([this.port.listSchedules({ limit: this.state.schedules.limit }), this.port.getScheduleTimeBounds()]); if (version === this.readVersion && !this.disposed) this.update({ schedules, bounds, loading: false, error: null }); }
    catch (error) { if (version === this.readVersion && !this.disposed) { this.fail(error); this.update({ loading: false }); } }
  }

  /** 加载下一页日程，保留后端返回的分页元数据。 */
  async nextPage(): Promise<void> { const current = this.state.schedules; if (this.state.busy || !current.nextCursor) return; const page = await this.port.listSchedules({ limit: current.limit, cursor: current.nextCursor }); if (!this.disposed) this.update({ schedules: { ...page, items: [...current.items, ...page.items] } }); }

  /** 重新取得动态单次日程边界。 */
  async refreshBounds(): Promise<void> { try { this.update({ bounds: await this.port.getScheduleTimeBounds() }); } catch (error) { if (!this.disposed) this.fail(error); } }

  /** 读取关闭行为；保存失败不改变本地已生效值。 */
  async saveCloseBehavior(value: CloseBehavior): Promise<void> { if (this.state.busy) return; this.update({ busy: true, error: null, notice: '' }); try { const saved = await this.port.saveCloseBehavior(value); if (!this.disposed) this.update({ closeBehavior: saved, notice: '关闭偏好已保存。' }); } catch (error) { if (!this.disposed) this.fail(error); } finally { if (!this.disposed) this.update({ busy: false }); } }

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

  /** 处理最新激活并按 sequence 去重，避免重复通知扰动当前输入。 */
  private onActivation = (activation: Activation): void => { if (this.disposed || activation.sequence <= this.lastSequence) return; this.lastSequence = activation.sequence; this.update({ activation, notice: `已收到日程 #${activation.targetId} 的激活请求。` }); };

  /** 统一状态变更通知。 */
  private update(patch: Partial<AppState>): void { if (this.disposed) return; this.state = { ...this.state, ...patch }; for (const listener of this.listeners) listener(); }

  /** 将平台拒绝值转换成可绑定字段的错误。 */
  private fail(error: unknown): void { const failure = toScheduleError(error); this.update({ error: failure, notice: '' }); }
}
