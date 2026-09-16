import { test, expect } from '@playwright/test';

test('四主题、主要设置与长标题的可读布局截图', async ({ page }, testInfo) => {
  await page.emulateMedia({ reducedMotion: 'reduce' });
  await page.goto('/'); await page.evaluate(() => document.fonts.ready);
  await page.screenshot({ path: testInfo.outputPath('peach-reminders.png') });
  for (const [key, name] of [['mint', '薄荷绿'], ['sky', '晴空蓝'], ['lavender', '薰衣草紫']]) {
    await page.getByRole('button', { name: '设置', exact: true }).click(); await page.getByRole('button', { name: '外观', exact: true }).click(); await page.getByRole('button', { name: new RegExp(name) }).click(); await expect(page.locator('.app-background')).toHaveAttribute('data-theme', key);
    await page.getByRole('button', { name: '待处理提醒', exact: true }).click(); await page.evaluate(() => document.fonts.ready); await page.screenshot({ path: testInfo.outputPath(`${key}-reminders.png`) });
  }
  await page.getByRole('button', { name: '设置', exact: true }).click(); await page.screenshot({ path: testInfo.outputPath('general.png') });
  await page.getByRole('button', { name: '存储位置', exact: true }).click(); await page.screenshot({ path: testInfo.outputPath('directories.png') });
  await page.getByRole('button', { name: '外观', exact: true }).click(); await page.screenshot({ path: testInfo.outputPath('appearance.png') });
  // 长标题通过适配器的场景转换注入，页面只接收正式存储事件。
  await page.goto('/controls'); await page.getByRole('button', { name: '长标题场景' }).click(); await page.goto('/');
  await page.setViewportSize({ width: 900, height: 640 }); await expect(page.locator('.detail h2')).toContainText('一条很长');
  const widths = await page.locator('.detail h2').evaluate(element => ({ scroll: element.scrollWidth, client: element.clientWidth })); expect(widths.scroll).toBeLessThanOrEqual(widths.client);
  await page.screenshot({ path: testInfo.outputPath('long-title-compact.png') });
  const confirm = page.getByRole('button', { name: '✓ 确认', exact: true }); await confirm.scrollIntoViewIfNeeded(); await expect(confirm).toBeInViewport(); await confirm.click(); await expect(page.locator('.message-card')).toHaveCount(2);
});
