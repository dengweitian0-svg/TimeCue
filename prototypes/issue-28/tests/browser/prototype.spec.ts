import { test, expect, type Page } from '@playwright/test';

/** 从控制台设置单次失败，再返回产品界面。 */
async function inject(page: Page, fault: string) { await page.goto('/controls'); await page.getByLabel('下一次操作的结果').selectOption(fault); await page.getByRole('button', { name: '应用失败注入' }).click(); await page.goto('/'); }
/** 打开独立目录设置页。 */
async function storagePage(page: Page) { await page.getByRole('button', { name: '设置', exact: true }).click(); await page.getByRole('button', { name: '存储位置', exact: true }).click(); }

test('查看不确认、确认失败、重试与最后一条空状态', async ({ page }) => {
  await page.goto('/'); await page.getByRole('button', { name: /站起来，活动/ }).click();
  await expect(page.getByText('3 条待处理 · 查看不会自动确认')).toBeVisible();
  await inject(page, 'confirm'); await page.getByRole('button', { name: '✓ 确认', exact: true }).click();
  await expect(page.getByRole('alert')).toContainText('确认未保存'); await expect(page.locator('.message-card')).toHaveCount(3);
  await page.getByRole('button', { name: /再次确认/ }).click(); await expect(page.locator('.detail h2')).toHaveText('站起来，活动活动肩颈');
  await page.getByRole('button', { name: '✓ 确认', exact: true }).click(); await expect(page.locator('.detail h2')).toHaveText('给阳台的小植物浇水');
  await page.getByRole('button', { name: '✓ 确认', exact: true }).click(); await expect(page.getByRole('heading', { name: '都安顿好啦' })).toBeVisible();
  await page.reload(); await expect(page.getByRole('heading', { name: '都安顿好啦' })).toBeVisible();
});

for (const [key, title] of [['mint', '薄荷绿'], ['sky', '晴空蓝'], ['lavender', '薰衣草紫'], ['peach', '奶油桃']]) {
  test(`${title} 切换并刷新保留，键盘可达`, async ({ page }) => {
    await page.goto('/'); await page.getByRole('button', { name: '设置', exact: true }).click(); await page.getByRole('button', { name: '外观', exact: true }).click();
    const option = page.getByRole('button', { name: new RegExp(title) }); await option.focus(); await expect(option).toBeFocused(); await page.keyboard.press('Enter');
    await expect(page.locator('.app-background')).toHaveAttribute('data-theme', key); await page.reload(); await expect(page.locator('.app-background')).toHaveAttribute('data-theme', key);
  });
}

for (const [kind, label] of [['logs', '日志'], ['backups', '备份']]) {
  test(`${label}目录成功、取消和两类失败均显示真实结果`, async ({ page }) => {
    await page.goto('/'); await storagePage(page); let section = page.getByRole('region', { name: `${label}目录设置` });
    await section.getByRole('textbox').fill('E:\\工作 资料\\新目录'); await section.getByRole('button', { name: '保存', exact: true }).click();
    await expect(page.getByTestId(`${kind}-effective`)).toHaveText('E:\\工作 资料\\新目录'); await page.reload(); await storagePage(page); await expect(page.getByTestId(`${kind}-effective`)).toHaveText('E:\\工作 资料\\新目录');
    await inject(page, 'cancel'); await storagePage(page); section = page.getByRole('region', { name: `${label}目录设置` }); await section.getByRole('button', { name: '选择目录' }).click(); await expect(section.getByRole('status')).toContainText('已取消');
    await inject(page, `${kind}-validation`); await storagePage(page); section = page.getByRole('region', { name: `${label}目录设置` }); await section.getByRole('textbox').fill('E:\\尝试 目录'); await section.getByRole('button', { name: '保存', exact: true }).click(); await expect(section.getByRole('alert')).toContainText('验证失败'); await expect(section.getByRole('textbox')).toHaveValue('E:\\尝试 目录');
    await inject(page, `${kind}-switch`); await storagePage(page); section = page.getByRole('region', { name: `${label}目录设置` }); await section.getByRole('textbox').fill('E:\\新的 尝试'); await section.getByRole('button', { name: '保存', exact: true }).click(); await expect(section.getByRole('alert')).toContainText('切换未完成'); await expect(section.getByRole('textbox')).toHaveValue('E:\\新的 尝试'); await expect(page.getByTestId(`${kind}-effective`)).toContainText('TimeCue 临时');
  });
}

test('默认隐藏、托盘恢复、再次启动和退出偏好', async ({ page }) => {
  await page.goto('/'); await page.getByRole('button', { name: '关闭窗口', exact: true }).click(); await expect(page.getByRole('heading', { name: '已隐藏到托盘' })).toBeVisible();
  await page.getByRole('button', { name: '打开', exact: true }).click(); await expect(page.locator('.window')).toHaveCount(1);
  await page.getByRole('button', { name: '最小化', exact: true }).click(); await page.getByRole('button', { name: '再次启动 TimeCue' }).click(); await expect(page.locator('.window')).toHaveCount(1);
  await page.getByRole('button', { name: '设置', exact: true }).click(); await page.getByRole('radio', { name: /退出应用/ }).check(); await page.getByRole('button', { name: '保存偏好' }).click(); await expect(page.getByRole('status')).toContainText('偏好已保存');
  await page.getByRole('button', { name: '关闭窗口', exact: true }).click(); await expect(page.getByRole('heading', { name: 'TimeCue 已退出' })).toBeVisible(); await expect(page.locator('.system-toast')).toHaveCount(0);
});

test('退出清理失败保持残留，重试清理后消失', async ({ page }) => {
  await inject(page, 'cleanup'); await page.getByRole('button', { name: '关闭窗口', exact: true }).click(); await page.getByRole('button', { name: '退出', exact: true }).click();
  await expect(page.getByRole('heading', { name: '系统通知清理失败' })).toBeVisible(); await expect(page.locator('.system-toast')).toHaveCount(2);
  await page.getByRole('button', { name: '重试清理' }).click(); await expect(page.getByRole('heading', { name: 'TimeCue 已退出' })).toBeVisible(); await expect(page.locator('.system-toast')).toHaveCount(0);
});

test('失效通知目标与窗口就绪交错', async ({ page }) => {
  await page.goto('/controls'); await page.getByRole('button', { name: '点击已确认目标' }).click(); await page.goto('/'); await expect(page.getByRole('heading', { name: '这条提醒已经确认' })).toBeVisible();
  await page.goto('/controls'); await page.getByRole('button', { name: '点击不存在目标' }).click(); await page.goto('/'); await expect(page.getByRole('heading', { name: '这条提醒已不存在' })).toBeVisible();
  await page.goto('/controls'); await page.getByRole('button', { name: '窗口尚未就绪', exact: true }).click(); await page.getByRole('button', { name: '点击肩颈通知' }).click();
  const product = await page.context().newPage(); await product.goto('/'); await expect(product.getByRole('heading', { name: '正在准备窗口…' })).toBeVisible();
  await page.getByRole('button', { name: '窗口就绪', exact: true }).click(); await expect(product.locator('.detail h2')).toHaveText('站起来，活动活动肩颈'); await expect(product.locator('.message-card')).toHaveCount(3);
});

test('900×640 紧凑窗口无横向溢出，保存截图', async ({ page }, testInfo) => {
  await page.setViewportSize({ width: 900, height: 640 }); await page.goto('/'); await expect(page.locator('.detail h2')).toBeVisible();
  expect(await page.evaluate(() => document.documentElement.scrollWidth)).toBe(900);
  await page.screenshot({ path: testInfo.outputPath('compact.png'), fullPage: true });
});
