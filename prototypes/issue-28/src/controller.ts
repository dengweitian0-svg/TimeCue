import type { AdapterEvent, Detail, Ports, Reminder, RuntimeState, Settings } from './contracts';

export interface AppState { settings: Settings | null; messages: Reminder[]; detail: Detail | null; runtime: RuntimeState | null; loading: boolean; confirming: string | null; error: string; page: 'reminders' | 'settings' }

/** 编排页面用例；平台适配器拥有持久状态，控制器只拥有当前选择和请求状态。 */
export class Controller {
  private state: AppState = { settings: null, messages: [], detail: null, runtime: null, loading: true, confirming: null, error: '', page: 'reminders' };
  private listeners = new Set<() => void>();
  private request = 0;
  private activation = 0;
  private disposed = false;
  private unsubscribe: () => void;
  /** 通过窄端口订阅平台反馈。 */
  constructor(public readonly ports: Ports) { this.unsubscribe = ports.subscribe(this.onAdapterEvent); }

  /** 提供稳定快照以便 React 使用外部状态订阅。 */
  snapshot = (): AppState => this.state;
  /** 订阅应用状态变化。 */
  subscribe = (listener: () => void): (() => void) => { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; };
  /** 销毁控制器后，异步结果不再回填视图。 */
  dispose(): void { this.disposed = true; this.request++; this.unsubscribe(); this.listeners.clear(); }
  /** 统一应用状态变更入口。 */
  private update(patch: Partial<AppState>): void { if (this.disposed) return; this.state = { ...this.state, ...patch }; for (const listener of this.listeners) listener(); }
  /** 系统边界最终记录一次失败，不暴露路径或消息正文。 */
  report(error: unknown, operation: string): void { console.error(JSON.stringify({ level: 'ERROR', operation, dependency: 'mock-adapter' })); this.update({ error: error instanceof Error ? error.message : '操作未完成，请重试。' }); }
  /** 清除已有错误提示。 */
  clearError(): void { this.update({ error: '' }); }
  /** 设置保存后只刷新生效设置，不影响正在查看的提醒。 */
  async refreshSettings(): Promise<void> { this.update({ settings: await this.ports.readSettings() }); }
  /** 页面切换不改变任何消息处理状态。 */
  setPage(page: AppState['page']): void { this.update({ page }); }

  /** 处理外部状态与激活事件，窗口未就绪时不提前选择目标。 */
  private onAdapterEvent = (event: AdapterEvent): void => {
    try {
      const runtime = this.ports.runtime(); this.update({ runtime });
      if (event === 'reset') { this.activation = 0; this.request++; this.update({ confirming: null, detail: null }); void this.refresh(); return; }
      if (runtime.ready && runtime.activation && runtime.activation.sequence !== this.activation) {
        this.activation = runtime.activation.sequence; this.update({ page: 'reminders' }); void this.refresh(runtime.activation.target); return;
      }
      if (event === 'external') void this.refresh(this.selectedId());
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
