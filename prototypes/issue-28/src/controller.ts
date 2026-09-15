import { ScheduleError, type Schedule, type ScheduleBounds, type ScheduleDraft, type AdapterEvent, type Detail, type Ports, type Reminder, type RuntimeState, type Settings } from './contracts';
import { draftRequest, emptyDraft } from './schedule-model';

export interface AppState { settings: Settings | null; messages: Reminder[]; detail: Detail | null; runtime: RuntimeState | null; loading: boolean; confirming: string | null; error: string; page: 'reminders' | 'settings' | 'schedules'; schedules: Schedule[]; scheduleDraft: ScheduleDraft | null; scheduleBounds: ScheduleBounds | null; scheduleBusy: boolean; schedulesLoading: boolean; scheduleError: ScheduleError | null; scheduleNotice: string; deleting: Schedule | null; discardTo: AppState['page'] | null }

/** 编排页面用例；平台适配器拥有持久状态，控制器只拥有当前选择和请求状态。 */
export class Controller {
  private state: AppState = { settings: null, messages: [], detail: null, runtime: null, loading: true, confirming: null, error: '', page: 'reminders', schedules: [], scheduleDraft: null, scheduleBounds: null, scheduleBusy: false, schedulesLoading: false, scheduleError: null, scheduleNotice: '', deleting: null, discardTo: null };
  private listeners = new Set<() => void>();
  private request = 0;
  private activation = 0;
  private disposed = false;
  private scheduleOperation = 0;
  private scheduleRead = 0;
  private unsubscribe: () => void;
  /** 通过窄端口订阅平台反馈。 */
  constructor(public readonly ports: Ports) { this.unsubscribe = ports.subscribe(this.onAdapterEvent); }

  /** 提供稳定快照以便 React 使用外部状态订阅。 */
  snapshot = (): AppState => this.state;
  /** 订阅应用状态变化。 */
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  /** 销毁控制器后，异步结果不再回填视图。 */
  dispose(): void { this.disposed = true; this.request++; this.scheduleOperation++; this.scheduleRead++; this.unsubscribe(); this.listeners.clear(); }
  /** 统一应用状态变更入口。 */
  private update(patch: Partial<AppState>): void { if (this.disposed) return; this.state = { ...this.state, ...patch }; for (const listener of this.listeners) listener(); }
  /** 系统边界最终记录一次失败，不暴露路径或消息正文。 */
  report(error: unknown, operation: string): void { console.error(JSON.stringify({ level: 'ERROR', operation, dependency: 'mock-adapter' })); this.update({ error: error instanceof Error ? error.message : '操作未完成，请重试。' }); }
  /** 清除已有错误提示。 */
  clearError(): void { this.update({ error: '' }); }
  /** 设置保存后只刷新生效设置，不影响正在查看的提醒。 */
  async refreshSettings(): Promise<void> { this.update({ settings: await this.ports.readSettings() }); }
  /** 页面切换保护未保存草稿，等待中的写操作不能被重复触发或打断。 */
  setPage(page: AppState['page']): void {
    if (this.state.scheduleBusy || this.state.deleting) return;
    if (page !== 'schedules' && this.state.page === 'schedules' && this.draftDirty()) { this.update({ discardTo: page }); return; }
    this.update({ page }); if (page === 'schedules') void this.refreshSchedules();
  }

  /** 判断草稿是否承载用户输入，空白表单取消无需二次确认。 */
  private draftDirty(): boolean { const d = this.state.scheduleDraft; return !!d && (d.title !== '' || d.notes !== '' || d.start !== '' || d.end !== '' || d.until !== '' || d.type !== 'single'); }
  /** 单独刷新定义和时间边界，读取失败不清空已有日程或表单。 */
  async refreshSchedules(): Promise<void> {
    const read = ++this.scheduleRead; this.update({ schedulesLoading: true });
    try {
      const [schedules, scheduleBounds] = await Promise.all([this.ports.listSchedules(), this.ports.scheduleBounds()]);
      if (read === this.scheduleRead && !this.disposed) this.update({ schedules, scheduleBounds, ...(!this.state.scheduleDraft && !this.state.deleting && !this.state.scheduleBusy ? { scheduleError: null } : {}) });
    } catch (error) { if (read === this.scheduleRead) this.scheduleFailure(error, 'list-schedules'); }
    finally { if (read === this.scheduleRead) this.update({ schedulesLoading: false }); }
  }
  /** 通过显式操作进入创建，保留通知激活期间尚未完成的草稿。 */
  beginSchedule(): void { if (this.state.scheduleBusy) return; this.update({ page: 'schedules', scheduleDraft: this.state.scheduleDraft ?? emptyDraft(), scheduleError: null, scheduleNotice: '' }); }
  /** 修改输入时不写存储，失败反馈在下一次提交时重新计算。 */
  editSchedule(patch: Partial<ScheduleDraft>): void { if (!this.state.scheduleBusy && this.state.scheduleDraft) this.update({ scheduleDraft: { ...this.state.scheduleDraft, ...patch } }); }
  /** 取消空草稿直接回列表，有输入时交给确认框决定。 */
  cancelSchedule(): void { if (this.state.scheduleBusy) return; if (this.draftDirty()) this.update({ discardTo: 'schedules' }); else this.update({ scheduleDraft: null, scheduleError: null }); }
  /** 关闭放弃确认，继续使用原草稿。 */
  keepDraft(): void { this.update({ discardTo: null }); }
  /** 用户确认后才丢弃草稿并完成原先请求的导航。 */
  discardDraft(): void { if (this.state.scheduleBusy || !this.state.discardTo) return; this.update({ page: this.state.discardTo, discardTo: null, scheduleDraft: null, scheduleError: null }); }
  /** 日程系统边界只记录一次失败，不输出标题、备注或其他个人内容。 */
  private scheduleFailure(error: unknown, operation: string): void {
    const failure = error instanceof ScheduleError ? error : new ScheduleError('STORAGE', error instanceof Error ? error.message : '操作未完成，请重试。');
    const entry = JSON.stringify({ level: failure.code === 'STORAGE' ? 'ERROR' : 'DEBUG', operation, code: failure.code, dependency: 'mock-adapter' });
    if (failure.code === 'STORAGE') console.error(entry); else console.debug(entry);
    this.update({ scheduleError: failure, ...(failure.bounds ? { scheduleBounds: failure.bounds } : {}) });
  }
  /** 创建提交只有一个在途请求；成功直接回填已提交结果，避免后续读取失败诱发重复创建。 */
  async saveSchedule(): Promise<void> {
    const draft = this.state.scheduleDraft; if (!draft || this.state.scheduleBusy) return;
    const operation = ++this.scheduleOperation; this.update({ scheduleBusy: true, scheduleError: null, scheduleNotice: '' });
    try {
      const saved = await this.ports.createSchedule(draftRequest(draft));
      if (operation !== this.scheduleOperation || this.disposed) return;
      // 丢弃写入前发起的慢读取，已提交结果不能被旧列表覆盖。
      this.scheduleRead++;
      this.update({ schedules: [saved, ...this.state.schedules.filter(s => s.id !== saved.id)], schedulesLoading: false, scheduleDraft: null, scheduleNotice: `已保存“${saved.title}”。到时会提醒你。` });
      console.info(JSON.stringify({ level: 'INFO', operation: 'create-schedule-complete', resource_id: saved.id }));
    } catch (error) { if (operation === this.scheduleOperation && !this.disposed) this.scheduleFailure(error, 'create-schedule'); }
    finally { if (operation === this.scheduleOperation) this.update({ scheduleBusy: false }); }
  }
  /** 锁定确认对象及其版本；外部列表刷新不会静默替换用户正在确认的内容。 */
  askDelete(schedule: Schedule): void { if (!this.state.scheduleBusy) this.update({ deleting: structuredClone(schedule), scheduleError: null, scheduleNotice: '' }); }
  /** 关闭删除确认不修改日程定义。 */
  keepSchedule(): void { if (!this.state.scheduleBusy) { const missing = this.state.scheduleError?.code === 'NOT_FOUND'; this.update({ deleting: null, scheduleError: null }); if (missing) void this.refreshSchedules(); } }
  /** 永久删除提交成功后才移出列表，缺失和旧版本必须先走恢复入口。 */
  async deleteSchedule(): Promise<void> {
    const item = this.state.deleting;
    if (!item || this.state.scheduleBusy || ['STALE_REVISION', 'NOT_FOUND'].includes(this.state.scheduleError?.code ?? '')) return;
    const operation = ++this.scheduleOperation; this.update({ scheduleBusy: true, scheduleError: null });
    try {
      await this.ports.deleteSchedule(item.id, item.revision);
      if (operation !== this.scheduleOperation || this.disposed) return;
      this.scheduleRead++;
      this.update({ schedules: this.state.schedules.filter(s => s.id !== item.id), schedulesLoading: false, deleting: null, scheduleNotice: `已永久删除“${item.title}”。` });
      console.info(JSON.stringify({ level: 'INFO', operation: 'delete-schedule-complete', resource_id: item.id }));
    } catch (error) { if (operation === this.scheduleOperation && !this.disposed) this.scheduleFailure(error, 'delete-schedule'); }
    finally { if (operation === this.scheduleOperation) this.update({ scheduleBusy: false }); }
  }
  /** 旧版本重新加载后仍需再次确认；缺失目标只刷新列表，不伪装为本次删除成功。 */
  async recoverDelete(): Promise<void> {
    const item = this.state.deleting; if (!item || this.state.scheduleBusy) return;
    const missing = this.state.scheduleError?.code === 'NOT_FOUND'; const operation = ++this.scheduleOperation;
    this.update({ scheduleBusy: true });
    try {
      const current = missing ? null : await this.ports.getSchedule(item.id);
      const schedules = await this.ports.listSchedules();
      if (operation !== this.scheduleOperation || this.disposed) return;
      this.update({ deleting: current, schedules, scheduleError: null, scheduleNotice: missing ? '已刷新列表，这条日程已不存在。' : '已读取最新内容，请重新核对后再删除。' });
    } catch (error) { if (operation === this.scheduleOperation && !this.disposed) this.scheduleFailure(error, 'refresh-delete-target'); }
    finally { if (operation === this.scheduleOperation) this.update({ scheduleBusy: false }); }
  }

  /** 处理外部状态与激活事件，窗口未就绪时不提前选择目标。 */
  private onAdapterEvent = (event: AdapterEvent): void => {
    try {
      const runtime = this.ports.runtime(); this.update({ runtime });
      if (event === 'reset') { this.activation = 0; this.request++; this.scheduleOperation++; this.scheduleRead++; this.update({ confirming: null, detail: null, scheduleDraft: null, deleting: null, discardTo: null, scheduleBusy: false, scheduleNotice: '', scheduleError: null }); void this.refresh(); void this.refreshSchedules(); return; }
      if (runtime.ready && runtime.activation && runtime.activation.sequence !== this.activation) {
        this.activation = runtime.activation.sequence; this.update({ page: 'reminders' }); void this.refresh(runtime.activation.target); return;
      }
      if (event === 'external') { void this.refresh(this.selectedId()); void this.refreshSchedules(); }
    } catch (error) { this.report(error, 'platform-event'); }
  };
  /** 读取当前详情标识，已失效目标也保留以显示明确状态页。 */
  private selectedId(): string | undefined { const d = this.state.detail; return d?.status === 'pending' ? d.message.id : d?.id; }

  /** 载入设置、列表和详情，使用请求序号丢弃迟到响应。 */
  async refresh(target?: string): Promise<void> {
    const request = ++this.request; this.update({ loading: true, error: '' });
    try {
      const [settings, messages] = await Promise.all([this.ports.readSettings(), this.ports.list()]);
      const runtime = this.ports.runtime();
      const activationTarget = runtime.activation && runtime.activation.sequence !== this.activation ? runtime.activation.target : undefined;
      if (runtime.activation) this.activation = runtime.activation.sequence;
      const desired = runtime.ready ? target ?? activationTarget ?? messages[0]?.id : undefined;
      const detail = desired ? await this.ports.detail(desired) : null;
      if (request !== this.request || this.disposed) return;
      this.update({ settings, messages, detail, runtime, loading: false });
    } catch (error) { if (request !== this.request || this.disposed) return; this.update({ loading: false }); this.report(error, 'load'); }
  }
  /** 只加载被选中的详情，不执行确认。 */
  async select(id: string): Promise<void> {
    if (this.state.confirming) return;
    const request = ++this.request;
    try { const detail = await this.ports.detail(id); if (request === this.request) this.update({ detail, error: '' }); }
    catch (error) { if (request === this.request) this.report(error, 'detail'); }
  }
  /** 保存成功后优先选同刻消息；外部激活到来时不覆盖其新选择。 */
  async confirm(): Promise<void> {
    if (this.state.confirming || this.state.detail?.status !== 'pending') return;
    const message = this.state.detail.message;
    const request = ++this.request;
    this.update({ confirming: message.id, error: '' });
    try {
      await this.ports.confirm(message.id);
      const messages = await this.ports.list();
      if (this.disposed) return;
      // 新激活请求拥有选择权，但确认完成后的列表也必须同步，避免保留已确认条目。
      if (request !== this.request) { await this.refresh(this.selectedId()); return; }
      const next = messages.find(m => m.at === message.at) ?? messages[0];
      const detail = next ? await this.ports.detail(next.id) : null;
      if (request === this.request) this.update({ messages, detail });
      console.info(JSON.stringify({ level: 'INFO', operation: 'confirm-complete', task_id: message.id }));
    } catch (error) { if (request === this.request) this.report(error, 'confirm'); }
    finally { if (this.state.confirming === message.id) this.update({ confirming: null }); }
  }
}
