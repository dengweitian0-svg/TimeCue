import { PrototypeError, type AdapterEvent, type Detail, type DirectoryKind, type Ports, type Reminder, type RuntimeState, type Settings } from './contracts';
import { themes } from './themes';

export const STORAGE_KEY = 'timecue:issue-28:v1';
export type Fault = 'none' | 'list' | 'confirm' | 'preferences' | 'logs-validation' | 'logs-switch' | 'backups-validation' | 'backups-switch' | 'cleanup' | 'cancel';
interface Stored { generation: number; settings: Settings; messages: Reminder[]; runtime: RuntimeState; fault: Fault }
export interface StorageLike { getItem(key: string): string | null; setItem(key: string, value: string): void }

/** 提供稳定的中文演示数据，避免真实时间改变验收路径。 */
export function seed(): Stored {
  return {
    generation: 0, fault: 'none',
    settings: { theme: 'peach', background: { kind: 'solid' }, closeBehavior: 'tray', logs: 'D:\\TimeCue 数据\\日志', backups: 'D:\\TimeCue 数据\\备份' },
    messages: [
      { id: 'water', title: '喝杯水，休息一下', body: '专注了这么久，辛苦啦。\n喝一杯温水，看看窗外，让眼睛也放个小假。', at: '2026-09-15T14:30:00', confirmed: false },
      { id: 'stretch', title: '站起来，活动活动肩颈', body: '离开座位走一走，轻轻转动肩膀。\n给身体一点舒展的时间。', at: '2026-09-15T14:30:00', confirmed: false },
      { id: 'plant', title: '给阳台的小植物浇水', body: '摸摸土壤，给绿意补一点水。\n这是一条今天 09:00 留下的提醒。', at: '2026-09-15T09:00:00', confirmed: false },
      { id: 'done', title: '已处理的提醒', body: '', at: '2026-09-15T08:00:00', confirmed: true },
    ],
    runtime: { visibility: 'visible', lifecycle: 'running', ready: true, pendingTarget: null, activation: null, notifications: ['water', 'stretch'] },
  };
}

/** 以独立命名空间模拟平台，所有持久化和生命周期变化均由此适配器拥有。 */
export class MockAdapter implements Ports {
  private listeners = new Set<(event: AdapterEvent) => void>();
  private confirmations = new Map<string, Promise<void>>();
  private stopping: Promise<void> | null = null;
  private memory = seed();
  /** 注入存储和可控延迟，供演示与测试替换。 */
  constructor(private storage: StorageLike, private delay = 400) {}

  /** 校验存储结构，损坏数据明确报错且不覆盖用户已有值。 */
  private read(): Stored {
    try {
      const raw = this.storage.getItem(STORAGE_KEY);
      if (!raw) return structuredClone(this.memory);
      const value = JSON.parse(raw) as Stored;
      if (!value.settings || !Object.hasOwn(themes, value.settings.theme) || !['tray', 'exit'].includes(value.settings.closeBehavior)
        || typeof value.settings.logs !== 'string' || typeof value.settings.backups !== 'string'
        || !value.settings.background || value.settings.background.kind !== 'solid'
        || !Array.isArray(value.messages) || value.messages.some(m => typeof m.id !== 'string' || typeof m.title !== 'string' || typeof m.body !== 'string' || typeof m.at !== 'string' || typeof m.confirmed !== 'boolean')
        || !value.runtime || !['running', 'stopping', 'stopped', 'cleanup-failed'].includes(value.runtime.lifecycle)
        || !['visible', 'hidden', 'minimized'].includes(value.runtime.visibility) || !Array.isArray(value.runtime.notifications)
        || typeof value.runtime.ready !== 'boolean' || !Number.isFinite(value.generation)) throw new Error('invalid snapshot');
      return value;
    } catch { throw new PrototypeError('本地演示数据无法读取，请在演示控制台重置场景。'); }
  }

  /** 保存完成后再发布状态，持久化失败不伪装成功。 */
  private write(value: Stored, event: AdapterEvent = 'runtime'): void {
    try { this.storage.setItem(STORAGE_KEY, JSON.stringify(value)); }
    catch { throw new PrototypeError('本地存储写入失败，请检查浏览器存储权限。'); }
    this.memory = structuredClone(value);
    this.emit(event);
  }

  /** 在系统边界发布必要状态，日志不输出目录或消息正文。 */
  private emit(event: AdapterEvent): void { for (const listener of this.listeners) listener(event); }

  /** 延迟期间若发生场景重置，则拒绝旧请求污染新场景。 */
  private async pause(): Promise<void> {
    const generation = this.read().generation;
    await new Promise<void>(resolve => setTimeout(resolve, this.delay));
    if (generation !== this.read().generation) throw new PrototypeError('场景已重置，本次操作已取消。');
  }

  /** 失败注入只消费一次，使重试可以展示成功路径。 */
  private consume(fault: Fault): boolean {
    const value = this.read();
    if (value.fault !== fault) return false;
    value.fault = 'none'; this.write(value); return true;
  }

  /** 订阅模拟平台事件。 */
  subscribe(listener: (event: AdapterEvent) => void): () => void { this.listeners.add(listener); return () => { this.listeners.delete(listener); }; }
  /** 将其他浏览器页签的适配器写入作为外部结果回流。 */
  externalChanged(): void { this.emit('external'); }
  /** 读取已生效设置。 */
  async readSettings(): Promise<Settings> { return structuredClone(this.read().settings); }
  /** 保存主题和关闭偏好，失败不改变生效值。 */
  async savePreferences(patch: Pick<Settings, 'theme' | 'closeBehavior'>): Promise<Settings> {
    await this.pause();
    if (this.consume('preferences')) throw new PrototypeError('偏好未能保存，仍使用之前已保存的设置。');
    const value = this.read(); value.settings = { ...value.settings, ...patch }; this.write(value); return structuredClone(value.settings);
  }
  /** 返回模拟的目录选择结果；取消不会修改设置或输入。 */
  async chooseDirectory(kind: DirectoryKind): Promise<string | null> {
    await this.pause(); if (this.consume('cancel')) return null;
    return `E:\\工作 资料\\TimeCue ${kind === 'logs' ? '日志' : '备份'}`;
  }
  /** 目录切换失败必须携带实际生效值，不假定回退至旧目录。 */
  async saveDirectory(kind: DirectoryKind, path: string): Promise<{ effectivePath: string }> {
    await this.pause();
    if (!/^(?:[A-Za-z]:\\|\\\\[^\\]+\\[^\\]+)/.test(path.trim()) || /[<>"|?*]/.test(path) || this.consume(`${kind}-validation`)) {
      throw new PrototypeError('目录验证失败，请填写可写的 Windows 绝对路径。', this.read().settings[kind]);
    }
    if (this.consume(`${kind}-switch`)) {
      const value = this.read(); value.settings[kind] = `C:\\TimeCue 临时\\${kind === 'logs' ? '日志' : '备份'}`; this.write(value);
      throw new PrototypeError('目录切换未完成，已显示当前实际生效目录。', value.settings[kind]);
    }
    const value = this.read(); value.settings[kind] = path.trim(); this.write(value); return { effectivePath: value.settings[kind] };
  }
  /** 按提醒时间倒序及原始同刻顺序返回待处理消息。 */
  async list(): Promise<Reminder[]> {
    if (this.consume('list')) throw new PrototypeError('提醒暂时没能加载，请重试。');
    return structuredClone(this.read().messages.filter(m => !m.confirmed).sort((a, b) => b.at.localeCompare(a.at)));
  }
  /** 读取详情绝不确认消息，失效目标不返回伪造正文。 */
  async detail(id: string): Promise<Detail> {
    const message = this.read().messages.find(m => m.id === id);
    return !message ? { status: 'missing', id } : message.confirmed ? { status: 'confirmed', id } : { status: 'pending', message: structuredClone(message) };
  }
  /** 合并同一消息的并发确认，并保持已确认请求幂等。 */
  confirm(id: string): Promise<void> {
    const existing = this.confirmations.get(id); if (existing) return existing;
    const operation = this.persistConfirmation(id).finally(() => { this.confirmations.delete(id); });
    this.confirmations.set(id, operation); return operation;
  }
  /** 后端保存成功才修改消息处理状态。 */
  private async persistConfirmation(id: string): Promise<void> {
    await this.pause(); const value = this.read(); const message = value.messages.find(m => m.id === id);
    if (!message) throw new PrototypeError('这条提醒已不存在。');
    if (message.confirmed) return;
    if (this.consume('confirm')) throw new PrototypeError('确认未保存，请重试。提醒仍在列表中。');
    const current = this.read(); current.messages.find(m => m.id === id)!.confirmed = true; this.write(current);
  }
  /** 返回彼此独立的窗口可见性、运行阶段与激活请求。 */
  runtime(): RuntimeState { return structuredClone(this.read().runtime); }
  /** 根据已保存偏好选择隐藏或正式停止。 */
  async close(): Promise<void> { if (this.read().settings.closeBehavior === 'exit') await this.exit(); else { const v = this.read(); v.runtime.visibility = 'hidden'; this.write(v); } }
  /** 最小化只影响窗口可见性。 */
  minimize(): void { const v = this.read(); v.runtime.visibility = 'minimized'; this.write(v); }
  /** 托盘恢复已有窗口，不改变后台运行和消息状态。 */
  open(): void { const v = this.read(); if (v.runtime.lifecycle !== 'running') return; v.runtime.visibility = 'visible'; this.write(v); }
  /** 再次启动激活唯一窗口；已停止时明确执行用户启动。 */
  launch(): void {
    const v = this.read(); if (v.runtime.lifecycle === 'stopping') return;
    v.runtime.lifecycle = 'running'; v.runtime.visibility = 'visible'; v.runtime.ready = true; this.deliver(v); this.write(v);
  }
  /** 系统通知只在运行状态下激活窗口，退出后不承诺冷启动。 */
  activate(id: string): void {
    const v = this.read(); if (v.runtime.lifecycle !== 'running') throw new PrototypeError('应用已退出。通知冷启动行为待 #14 平台验证，请显式启动 TimeCue。');
    v.runtime.visibility = 'visible'; v.runtime.pendingTarget = id; this.deliver(v); this.write(v);
  }
  /** 未就绪时只保留最近的定位请求，就绪后再消费。 */
  setReady(ready: boolean): void { const v = this.read(); v.runtime.ready = ready; this.deliver(v); this.write(v); }
  /** 将外部激活请求正式回填为可消费的应用事件。 */
  private deliver(v: Stored): void {
    if (!v.runtime.ready || !v.runtime.pendingTarget) return;
    v.runtime.activation = { sequence: (v.runtime.activation?.sequence ?? 0) + 1, target: v.runtime.pendingTarget };
    v.runtime.pendingTarget = null;
  }
  /** 复用同一次退出请求，避免并发清理相互覆盖。 */
  exit(): Promise<void> {
    if (this.stopping) return this.stopping;
    this.stopping = this.stop().finally(() => { this.stopping = null; }); return this.stopping;
  }
  /** 先停止提醒，再清理通知；失败保留残留通知与真实失败状态。 */
  private async stop(): Promise<void> {
    const v = this.read(); v.runtime.lifecycle = 'stopping'; v.runtime.visibility = 'hidden'; v.runtime.pendingTarget = null; this.write(v);
    await this.pause();
    if (this.consume('cleanup')) { const failed = this.read(); failed.runtime.lifecycle = 'cleanup-failed'; this.write(failed); throw new PrototypeError('系统通知清理失败，请检查残留通知并重试清理。'); }
    const done = this.read(); done.runtime.notifications = []; done.runtime.lifecycle = 'stopped'; done.runtime.ready = false; done.runtime.activation = null; this.write(done);
  }
  /** 场景控制台独立配置下一次失败。 */
  inject(fault: Fault): void { const v = this.read(); v.fault = fault; this.write(v); }
  /** 显示控制台当前故障配置。 */
  fault(): Fault { return this.read().fault; }
  /** 通过正式场景转换提供中文长标题的布局验收数据。 */
  longTitle(): void { const value = this.read(); const message = value.messages.find(m => m.id === 'water'); if (message) message.title = '一条很长的中文提醒标题，包含工作回顾与重要事项说明，检查换行是否完整保留'; this.write(value, 'external'); }
  /** 重置只覆盖原型命名空间，并使尚未完成的旧操作失效。 */
  reset(): void { let generation = Date.now(); try { generation = Math.max(generation, this.read().generation + 1); } catch { /* 损坏数据通过显式场景重置恢复。 */ } const v = seed(); v.generation = generation; this.write(v, 'reset'); }
}
