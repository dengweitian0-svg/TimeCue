import { expect, test, type Page } from '@playwright/test';
import { themes, type Theme } from '../../src/themes';

/** 通过产品控件进入表单，固定时间仅用于稳定今天标记与边界。 */
async function form(page: Page, theme: Theme = 'peach') {
  await page.clock.setFixedTime(new Date('2026-09-15T12:00:00+08:00')); await page.emulateMedia({ reducedMotion: 'reduce' }); await page.goto('/');
  if (theme !== 'peach') { await page.getByRole('button', { name: '设置', exact: true }).click(); await page.getByRole('button', { name: '外观', exact: true }).click(); await page.getByRole('button', { name: new RegExp(themes[theme].name) }).click(); await expect(page.locator('.app-background')).toHaveAttribute('data-theme', theme); }
  await page.getByRole('button', { name: '我的日程', exact: true }).click(); await page.getByRole('button', { name: '＋ 添加日程' }).click();
  await page.locator('#schedule-start').fill('2027-01-31T19:00'); await page.locator('#schedule-end').fill('2027-01-31T19:30');
}
/** 将设计色值转换为浏览器计算色，检查弹层实际绘制而非仅检查类名。 */
function rgb(hex: string): string { return `rgb(${[1, 3, 5].map(i => parseInt(hex.slice(i, i + 2), 16)).join(', ')})`; }

for (const theme of Object.keys(themes) as Theme[]) for (const width of [1120, 900]) {
  test(`${themes[theme].name} ${width} 日期时间弹层和截止日期跟随主题`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: width === 900 ? 640 : 760 }); await form(page, theme);
    await page.getByRole('button', { name: '选择开始时间', exact: true }).click();
    const picker = page.getByRole('dialog', { name: '选择开始时间' }); await expect(picker).toBeVisible();
    await expect(picker).toHaveCSS('background-color', rgb(themes[theme].bg)); await expect(picker).toHaveCSS('color', rgb(themes[theme].text));
    await expect(picker.getByRole('button', { name: '2027年1月31日', exact: true })).toHaveCSS('background-color', rgb(themes[theme].accent));
    await expect(picker.getByRole('button', { name: '19小时', exact: true })).toHaveCSS('background-color', rgb(themes[theme].accent));
    const rect = await picker.boundingBox(); expect(rect!.x).toBeGreaterThanOrEqual(0); expect(rect!.y).toBeGreaterThanOrEqual(0); expect(rect!.x + rect!.width).toBeLessThanOrEqual(width); expect(rect!.y + rect!.height).toBeLessThanOrEqual(width === 900 ? 640 : 760);
    await page.screenshot({ path: testInfo.outputPath(`${theme}-${width}-date-time.png`) });
    await page.keyboard.press('Escape'); await expect(page.getByRole('dialog')).toHaveCount(0); await expect(page.getByRole('button', { name: '选择开始时间', exact: true })).toBeFocused();
    await page.getByRole('button', { name: '选择结束时间', exact: true }).click(); await expect(page.getByRole('dialog')).toHaveCSS('background-color', rgb(themes[theme].bg)); await page.keyboard.press('Escape');
    await page.getByRole('button', { name: '每月', exact: true }).click(); await page.getByRole('button', { name: '选择重复截止日期', exact: true }).click();
    const until = page.getByRole('dialog', { name: '选择重复截止日期' }); await expect(until).toHaveCSS('background-color', rgb(themes[theme].bg)); await expect(until.getByRole('group', { name: '小时' })).toHaveCount(0);
    await until.getByRole('button', { name: '下个月' }).click(); await until.getByRole('button', { name: '2027年2月28日', exact: true }).click(); await until.getByRole('button', { name: '确定', exact: true }).click(); await expect(page.locator('#schedule-until')).toHaveValue('2027-02-28');
  });
}

test('日期与时间键盘选择、确认回填、Esc 取消和清除', async ({ page }) => {
  await form(page); await page.getByRole('button', { name: '选择开始时间', exact: true }).click(); const picker = page.getByRole('dialog');
  await expect(picker.getByRole('button', { name: '2027年1月31日', exact: true })).toBeFocused(); await page.keyboard.press('PageDown'); await expect(picker.getByRole('button', { name: '2027年2月28日', exact: true })).toBeFocused();
  await picker.getByRole('button', { name: '09小时', exact: true }).click(); await page.keyboard.press('ArrowDown'); await expect(picker.getByRole('button', { name: '10小时', exact: true })).toBeFocused();
  await picker.getByRole('button', { name: '45分钟', exact: true }).click(); await picker.getByRole('button', { name: '确定', exact: true }).click(); await expect(page.locator('#schedule-start')).toHaveValue('2027-02-28T10:45');
  await page.getByRole('button', { name: '选择开始时间', exact: true }).click(); await page.keyboard.press('ArrowLeft'); await page.keyboard.press('Escape'); await expect(page.locator('#schedule-start')).toHaveValue('2027-02-28T10:45'); await expect(page.getByRole('dialog')).toHaveCount(0);
  await page.getByRole('button', { name: '选择开始时间', exact: true }).click(); await page.getByRole('button', { name: '清除', exact: true }).click(); await expect(page.locator('#schedule-start')).toHaveValue('');
});

test('弹层范围限制与手动输入最终校验共存', async ({ page }) => {
  await form(page); await page.locator('#schedule-start').fill('2026-09-15T12:00'); await page.getByRole('button', { name: '选择开始时间', exact: true }).click();
  await expect(page.getByRole('button', { name: '2026年9月14日', exact: true })).toBeDisabled(); await page.keyboard.press('Escape');
  await page.locator('#schedule-end').fill('2027-09-15T00:00'); await page.getByRole('button', { name: '选择结束时间', exact: true }).click(); await expect(page.getByRole('button', { name: '确定', exact: true })).toBeEnabled();
  await page.getByRole('button', { name: '01小时', exact: true }).click(); await expect(page.getByRole('button', { name: '确定', exact: true })).toBeDisabled(); await page.keyboard.press('Escape');
  await page.locator('#schedule-title').fill('测试日期'); await page.locator('#schedule-start').fill('2027-02-30T12:00'); await page.getByRole('button', { name: '保存日程', exact: true }).click(); await expect(page.locator('#schedule-start')).toHaveAttribute('aria-invalid', 'true'); await expect(page.locator('#schedule-start')).toHaveValue('2027-02-30T12:00');
});
