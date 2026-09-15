import { useEffect, useState } from 'react';
import { MockAdapter, type Fault } from './adapter';
import { Background, Button, Feedback } from './components';

/** 独立控制台负责重置、失败注入和平台事件模拟，不嵌入产品导航。 */
export function Controls({ adapter }: { adapter: MockAdapter }) {
  const [, render] = useState(0); const [fault, setFault] = useState<Fault>('none'); const [notice, setNotice] = useState(''); const [error, setError] = useState('');
  useEffect(() => adapter.subscribe(() => render(n => n + 1)), [adapter]);
  /** 将控制场景的最终异常反馈给评审者。 */
  async function run(label: string, action: () => void | Promise<void>) { setError(''); try { await action(); setNotice(label); } catch (value) { setError(value instanceof Error ? value.message : '操作失败'); console.error(JSON.stringify({ level: 'ERROR', operation: 'scenario-control' })); } }
  let snapshot = ''; try { snapshot = JSON.stringify({ ...adapter.runtime(), nextFailure: adapter.fault() }, null, 2); } catch { snapshot = '数据损坏，请使用重置场景恢复。'; }
  const faults: [Fault, string][] = [['none', '不注入失败'], ['schedule-create', '创建日程保存失败'], ['schedule-delete', '删除日程失败'], ['schedule-stale', '删除时日程已更新'], ['schedule-missing', '删除时日程已不存在'], ['list', '列表加载失败'], ['confirm', '下一次确认失败'], ['preferences', '偏好保存失败'], ['logs-validation', '日志目录验证失败'], ['logs-switch', '日志目录切换失败（生效到临时目录）'], ['backups-validation', '备份目录验证失败'], ['backups-switch', '备份目录切换失败（生效到临时目录）'], ['cleanup', '退出通知清理失败'], ['cancel', '下一次目录选择取消']];
  return <Background theme="peach" config={{ kind: 'solid' }}><main className="controls"><p className="eyebrow">ISSUE #28 · 模拟场景控制台</p><h1>把每一种结果，都走一遍。</h1><p className="hint">本页独立于产品界面。可在另一页签打开产品，控制事件通过模拟适配器同步。</p><p className="hint"><a href="/" target="_blank" rel="noreferrer">打开 TimeCue 演示 ↗</a> &nbsp; · &nbsp; <a href="https://www.figma.com/proto/mmHsccec0PB3Mf8y97BbrA?node-id=27-1236&starting-point-node-id=27%3A1236" target="_blank" rel="noreferrer">Figma 原型 ↗</a></p>
    {error ? <Feedback kind="error">{error}</Feedback> : notice && <Feedback kind="success">{notice}</Feedback>}
    <div className="controls-grid"><section><h2>01 · 场景与失败</h2><Button onClick={() => void run('场景已重置', () => adapter.reset())}>重置场景</Button><label htmlFor="fault">下一次操作的结果</label><select id="fault" value={fault} onChange={e => setFault(e.target.value as Fault)}>{faults.map(([key, label]) => <option value={key} key={key}>{label}</option>)}</select><Button secondary onClick={() => void run('失败注入已设置，仅消费一次', () => adapter.inject(fault))}>应用失败注入</Button></section>
    <section><h2>02 · 系统通知激活</h2>{[['water', '点击喝水通知'], ['stretch', '点击肩颈通知'], ['plant', '点击此前通知'], ['done', '点击已确认目标'], ['missing', '点击不存在目标']].map(([id, label]) => <Button secondary key={id} onClick={() => void run(label, () => adapter.activate(id))}>{label}</Button>)}<p className="hint">逐条与汇总策略待评审；汇总点击定位首条消息，不进行批量确认。</p><Button secondary onClick={() => void run('模拟汇总通知点击', () => adapter.activate('water'))}>点击汇总通知（待定）</Button></section>
    <section><h2>03 · 窗口与生命周期</h2><Button secondary onClick={() => void run('已模拟窗口尚未就绪', () => adapter.setReady(false))}>窗口尚未就绪</Button><Button secondary onClick={() => void run('窗口已就绪', () => adapter.setReady(true))}>窗口就绪</Button><Button secondary onClick={() => void run('再次启动已激活唯一窗口', () => adapter.launch())}>再次启动</Button><Button secondary onClick={() => void run('已最小化', () => adapter.minimize())}>最小化</Button><Button secondary onClick={() => void run('关闭已执行', () => adapter.close())}>关闭窗口</Button><Button secondary onClick={() => void run('正式退出完成', () => adapter.exit())}>正式退出</Button><p className="hint">退出后的残留通知点击不自动冷启动。真实 Windows 清理与冷启动由 #14 验证。</p></section>
    <section><h2>04 · 平台状态观察</h2><pre aria-label="平台状态">{snapshot}</pre><Button secondary onClick={() => void run('长标题场景已启用', () => adapter.longTitle())}>长标题场景</Button><p className="hint">模拟数据保存在 timecue:issue-28:v1；不会访问真实目录或系统通知。</p></section></div>
  </main></Background>;
}
