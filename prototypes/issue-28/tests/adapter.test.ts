import { describe, expect, it } from 'vitest';
import { MockAdapter, seed, STORAGE_KEY, type StorageLike } from '../src/adapter';
import { Controller } from '../src/controller';
import { themes } from '../src/themes';

/** 提供独立内存存储，验证适配器行为而不依赖浏览器。 */
function memory(): StorageLike { const data = new Map<string, string>(); return { getItem: key => data.get(key) ?? null, setItem: (key, value) => { data.set(key, value); } }; }
/** 等待异步激活用例完成，不依赖固定业务延迟。 */
async function settle() { await new Promise(resolve => setTimeout(resolve, 15)); }

describe('消息状态与应用编排', () => {
  it('查看与切换不确认，失败保留，成功优先同刻，最后为空', async () => {
    const adapter = new MockAdapter(memory(), 1); const app = new Controller(adapter);
    await app.refresh(); await app.select('plant'); await app.select('water');
    expect((await adapter.list()).map(m => m.id)).toEqual(['water', 'stretch', 'plant']);
    adapter.inject('confirm'); await app.confirm();
    expect(app.snapshot().messages).toHaveLength(3); expect(app.snapshot().error).toContain('确认未保存');
    await app.confirm(); expect(app.snapshot().detail).toMatchObject({ status: 'pending', message: { id: 'stretch' } });
    await app.confirm(); expect(app.snapshot().detail).toMatchObject({ status: 'pending', message: { id: 'plant' } });
    await app.confirm(); expect(app.snapshot().detail).toBeNull(); expect(app.snapshot().messages).toEqual([]); app.dispose();
  });
  it('并发确认与重启后的重复确认均幂等', async () => {
    const storage = memory(); const adapter = new MockAdapter(storage, 1);
    await Promise.all([adapter.confirm('water'), adapter.confirm('water')]);
    const reloaded = new MockAdapter(storage, 1); await reloaded.confirm('water');
    expect(await reloaded.list()).toHaveLength(2); expect(await reloaded.detail('water')).toEqual({ status: 'confirmed', id: 'water' });
  });
  it('已确认与不存在目标返回不同状态，加载失败可以重试', async () => {
    const adapter = new MockAdapter(memory(), 1); const app = new Controller(adapter);
    await app.refresh('done'); expect(app.snapshot().detail).toEqual({ status: 'confirmed', id: 'done' });
    await app.refresh('missing'); expect(app.snapshot().detail).toEqual({ status: 'missing', id: 'missing' });
    adapter.inject('list'); await app.refresh(); expect(app.snapshot().error).toContain('加载');
    await app.refresh(); expect(app.snapshot().messages).toHaveLength(3); expect(app.snapshot().error).toBe(''); app.dispose();
  });
  it('先确认此前消息，仍按列表顺序选中同刻组首条', async () => {
    const adapter = new MockAdapter(memory(), 1); const app = new Controller(adapter); await app.refresh('plant'); await app.confirm();
    expect(app.snapshot().detail).toMatchObject({ status: 'pending', message: { id: 'water' } }); app.dispose();
  });
  it('存储失败不移除消息', async () => {
    const storage = memory(); const adapter = new MockAdapter(storage, 1); await adapter.readSettings();
    storage.setItem = () => { throw new Error('quota'); };
    await expect(adapter.confirm('water')).rejects.toThrow('写入失败'); expect(await adapter.list()).toHaveLength(3);
  });
  it('重置期间的旧确认不得污染新场景', async () => {
    const adapter = new MockAdapter(memory(), 20); const pending = adapter.confirm('water'); adapter.reset();
    await expect(pending).rejects.toThrow('场景已重置'); expect(await adapter.list()).toHaveLength(3);
  });
});

describe.each(['logs', 'backups'] as const)('%s 目录', kind => {
  it('支持中文空格路径并在重新读取时生效，另一目录不变', async () => {
    const storage = memory(); const adapter = new MockAdapter(storage, 1); const before = await adapter.readSettings();
    await adapter.saveDirectory(kind, 'E:\\工作 资料\\新的目录');
    const after = await new MockAdapter(storage).readSettings(); expect(after[kind]).toBe('E:\\工作 资料\\新的目录');
    const other = kind === 'logs' ? 'backups' : 'logs'; expect(after[other]).toBe(before[other]);
  });
  it('选择取消不更改生效值', async () => {
    const adapter = new MockAdapter(memory(), 1); const before = await adapter.readSettings(); adapter.inject('cancel');
    expect(await adapter.chooseDirectory(kind)).toBeNull(); expect(await adapter.readSettings()).toEqual(before);
  });
  it('验证失败返回旧生效值，切换失败返回实际新值', async () => {
    const adapter = new MockAdapter(memory(), 1); const before = await adapter.readSettings();
    await expect(adapter.saveDirectory(kind, 'relative')).rejects.toMatchObject({ effectivePath: before[kind] });
    adapter.inject(`${kind}-switch`);
    await expect(adapter.saveDirectory(kind, 'E:\\有效路径')).rejects.toMatchObject({ effectivePath: `C:\\TimeCue 临时\\${kind === 'logs' ? '日志' : '备份'}` });
    expect((await adapter.readSettings())[kind]).toContain('TimeCue 临时');
  });
});

describe('偏好和平台生命周期', () => {
  it.each(Object.keys(themes) as (keyof typeof themes)[])('%s 主题与关闭偏好刷新后保留', async theme => {
    const storage = memory(); const adapter = new MockAdapter(storage, 1);
    await adapter.savePreferences({ theme, closeBehavior: 'exit' });
    expect(await new MockAdapter(storage).readSettings()).toMatchObject({ theme, closeBehavior: 'exit' });
  });
  it('偏好保存失败保持原设置', async () => {
    const adapter = new MockAdapter(memory(), 1); adapter.inject('preferences');
    await expect(adapter.savePreferences({ theme: 'mint', closeBehavior: 'exit' })).rejects.toThrow('偏好未能保存');
    expect(await adapter.readSettings()).toMatchObject({ theme: 'peach', closeBehavior: 'tray' });
  });
  it('默认隐藏、最小化和恢复均不影响提醒状态', async () => {
    const adapter = new MockAdapter(memory(), 1); await adapter.close(); expect(adapter.runtime()).toMatchObject({ visibility: 'hidden', lifecycle: 'running' });
    adapter.open(); expect(adapter.runtime().visibility).toBe('visible'); adapter.minimize(); expect(adapter.runtime().visibility).toBe('minimized');
    adapter.launch(); expect(adapter.runtime()).toMatchObject({ visibility: 'visible', lifecycle: 'running' }); expect(await adapter.list()).toHaveLength(3);
  });
  it('退出停止后台并清理通知，清理失败独立显示且支持重试', async () => {
    const adapter = new MockAdapter(memory(), 1); adapter.inject('cleanup');
    await expect(adapter.exit()).rejects.toThrow('清理失败'); expect(adapter.runtime()).toMatchObject({ lifecycle: 'cleanup-failed', notifications: ['water', 'stretch'] });
    expect(() => adapter.activate('water')).toThrow('冷启动');
    await adapter.exit(); expect(adapter.runtime()).toMatchObject({ lifecycle: 'stopped', notifications: [] });
    adapter.launch(); expect(adapter.runtime().lifecycle).toBe('running');
  });
  it('退出偏好走正式停止流程，重复退出不重复清理', async () => {
    const adapter = new MockAdapter(memory(), 1); await adapter.savePreferences({ theme: 'peach', closeBehavior: 'exit' });
    await Promise.all([adapter.close(), adapter.exit()]); expect(adapter.runtime()).toMatchObject({ lifecycle: 'stopped', notifications: [] });
  });
  it('就绪前缓存最后一次定位，就绪后显示目标详情且不确认', async () => {
    const adapter = new MockAdapter(memory(), 1); const app = new Controller(adapter); await app.refresh();
    adapter.setReady(false); adapter.activate('water'); adapter.activate('plant');
    expect(adapter.runtime()).toMatchObject({ pendingTarget: 'plant', activation: null });
    adapter.setReady(true); await settle(); expect(app.snapshot().detail).toMatchObject({ status: 'pending', message: { id: 'plant' } });
    expect(await adapter.list()).toHaveLength(3); app.dispose();
  });
  it('控制台先激活，产品页后载入仍定位目标', async () => {
    const adapter = new MockAdapter(memory(), 1); adapter.activate('stretch'); const app = new Controller(adapter); await app.refresh();
    expect(app.snapshot().detail).toMatchObject({ status: 'pending', message: { id: 'stretch' } }); app.dispose();
  });
  it('确认中激活其他消息，不用旧确认结果覆盖新目标', async () => {
    const adapter = new MockAdapter(memory(), 15); const app = new Controller(adapter); await app.refresh();
    const pending = app.confirm(); adapter.activate('plant'); await pending; await settle();
    expect(app.snapshot().detail).toMatchObject({ status: 'pending', message: { id: 'plant' } }); expect(await adapter.list()).toHaveLength(2); expect(app.snapshot().messages).toHaveLength(2); app.dispose();
  });
  it('损坏存储明确报错，显式重置恢复', async () => {
    const storage = memory(); storage.setItem(STORAGE_KEY, '{bad'); const adapter = new MockAdapter(storage, 1);
    await expect(adapter.list()).rejects.toThrow('无法读取'); adapter.reset(); expect(await adapter.list()).toHaveLength(3);
  });
});

/** 计算 sRGB 相对亮度，用于验证主题可读性。 */
function luminance(hex: string) { const channels = [1, 3, 5].map(start => parseInt(hex.slice(start, start + 2), 16) / 255).map(c => c <= .04045 ? c / 12.92 : ((c + .055) / 1.055) ** 2.4); return channels[0] * .2126 + channels[1] * .7152 + channels[2] * .0722; }
/** 计算文本与背景的对比度。 */
function contrast(a: string, b: string) { const values = [luminance(a), luminance(b)].sort((a, b) => b - a); return (values[0] + .05) / (values[1] + .05); }
it.each(Object.values(themes))('$name 的文字、按钮与反馈文字达到 4.5:1', theme => {
  for (const [text, bg] of [[theme.text, theme.surface], [theme.muted, theme.bg], [theme.muted, theme.soft], ['#FFFFFF', theme.accent], [theme.success, theme.successBg], [theme.error, theme.errorBg]]) expect(contrast(text, bg)).toBeGreaterThanOrEqual(4.5);
});

it('长标题和中文路径通过完整适配器保留', async () => {
  const storage = memory(); const state = seed(); state.messages[0].title = '一条很长的中文提醒标题，包含工作回顾与重要事项说明，检查换行是否完整保留'; storage.setItem(STORAGE_KEY, JSON.stringify(state));
  const adapter = new MockAdapter(storage, 1); expect(await adapter.detail('water')).toMatchObject({ status: 'pending', message: { title: state.messages[0].title } });
});
