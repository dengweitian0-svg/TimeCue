import { test, expect, type Page } from '@playwright/test';

/** 固定当前日期但保留真实定时器，验证跨刷新持久化不依赖测试运行日期。 */
async function openSchedules(page: Page) {
  await page.clock.setFixedTime(new Date('2026-09-15T12:00:00+08:00'));
  await page.goto('/'); await page.getByRole('button', { name: '我的日程', exact: true }).click();
}
/** 通过真实控件填写四种日程，不直接写浏览器存储。 */
async function fillSchedule(page: Page, type = '单次', title = '读完一本喜欢的书') {
  await page.getByRole('button', { name: '＋ 添加日程' }).click();
  await page.locator('#schedule-title').fill(title); await page.locator('#schedule-notes').fill('留半小时给自己，读到下一章。');
  await page.getByRole('button', { name: type, exact: true }).click();
  await page.locator('#schedule-start').fill('2027-01-31T19:00'); await page.locator('#schedule-end').fill('2027-01-31T19:30');
  if (type !== '单次') await page.locator('#schedule-until').fill('2027-03-31');
}
/** 在独立控制页签注入失败，避免产品刷新掩盖输入保留问题。 */
async function inject(page: Page, fault: string) {
  const controls = await page.context().newPage(); await controls.goto('/controls');
  await controls.getByLabel('下一次操作的结果').selectOption(fault); await controls.getByRole('button', { name: '应用失败注入' }).click(); await controls.close();
}

for (const type of ['单次', '每天', '每周', '每月']) {
  test(`${type} 创建、刷新、永久删除、重启后不存在，消息状态独立`, async ({ page }) => {
    await openSchedules(page); await fillSchedule(page, type);
    await page.getByRole('button', { name: '保存日程', exact: true }).click();
    await expect(page.locator('.schedule-row')).toHaveCount(1); await expect(page.locator('.schedule-card')).toContainText(type);
    await expect(page.locator('.schedule-card')).toContainText('留半小时给自己');
    await page.reload(); await page.getByRole('button', { name: '我的日程', exact: true }).click(); await expect(page.locator('.schedule-row')).toHaveCount(1);
    await page.getByRole('button', { name: '删除日程', exact: true }).click(); const dialog = page.getByRole('dialog');
    await expect(dialog.getByRole('heading', { level: 2 })).toHaveText(type === '单次' ? '永久删除这个日程？' : '永久删除整组日程？');
    await expect(dialog.getByRole('button', { name: '保留日程' })).toBeFocused();
    await dialog.getByRole('button', { name: '永久删除', exact: true }).click(); await expect(dialog).not.toBeVisible(); await expect(page.locator('.schedule-row')).toHaveCount(0);
    await page.reload(); await page.getByRole('button', { name: '我的日程', exact: true }).click(); await expect(page.getByRole('heading', { name: '你还没有日程' })).toBeVisible();
    await page.getByRole('button', { name: '待处理提醒', exact: true }).click(); await expect(page.locator('.message-card')).toHaveCount(3);
  });
}

test('校验定位、取消保留、侧栏守卫、Esc 与模态焦点循环', async ({ page }) => {
  await openSchedules(page); await page.getByRole('button', { name: '＋ 添加日程' }).click(); await expect(page.locator('#schedule-title')).toBeFocused();
  await page.getByRole('button', { name: '保存日程' }).click(); await expect(page.locator('#schedule-title')).toHaveAttribute('aria-invalid', 'true'); await expect(page.locator('#schedule-title')).toBeFocused();
  await page.locator('#schedule-title').fill('未保存的安排'); await page.locator('#schedule-start').fill('2027-01-31T19:00'); await page.locator('#schedule-end').fill('2027-01-31T18:00');
  await page.getByRole('button', { name: '重试保存' }).click(); await expect(page.locator('#schedule-end')).toBeFocused(); await expect(page.getByText('结束时间必须晚于开始时间。', { exact: true })).toBeVisible();
  await page.getByRole('button', { name: '设置', exact: true }).click(); const modal = page.getByRole('dialog'); await expect(modal).toBeVisible();
  await expect(modal.getByRole('button', { name: '继续填写' })).toBeFocused(); await page.keyboard.press('Shift+Tab'); await expect(modal.getByRole('button', { name: '放弃创建' })).toBeFocused();
  await page.keyboard.press('Escape'); await expect(modal).not.toBeVisible(); await expect(page.locator('#schedule-title')).toHaveValue('未保存的安排');
  await page.getByRole('button', { name: '取消', exact: true }).click(); await modal.getByRole('button', { name: '放弃创建' }).click(); await expect(page.locator('.schedule-form')).toHaveCount(0); await expect(page.locator('.schedule-row')).toHaveCount(0);
});

test('保存失败保留重复字段，等待中禁用操作，重试只创建一条', async ({ page }) => {
  await openSchedules(page); await fillSchedule(page, '每月'); await inject(page, 'schedule-create');
  await page.getByRole('button', { name: '保存日程' }).click(); await expect(page.locator('#schedule-title')).toBeDisabled(); await expect(page.getByRole('button', { name: '取消', exact: true })).toBeDisabled();
  await expect(page.getByRole('alert')).toContainText('填写内容已保留'); await expect(page.locator('#schedule-until')).toHaveValue('2027-03-31'); await expect(page.locator('#schedule-title')).toHaveValue('读完一本喜欢的书');
  await page.getByRole('button', { name: '重试保存' }).click(); await expect(page.locator('.schedule-row')).toHaveCount(1);
  await inject(page, 'schedule-delete'); await page.getByRole('button', { name: '删除日程' }).click(); await page.getByRole('button', { name: '永久删除', exact: true }).click();
  await expect(page.getByRole('dialog').getByRole('alert')).toContainText('日程仍保留'); await expect(page.locator('.schedule-row')).toHaveCount(1);
  await page.getByRole('button', { name: '重试删除' }).click(); await expect(page.getByRole('dialog')).not.toBeVisible(); await expect(page.locator('.schedule-row')).toHaveCount(0);
});

test('版本过期必须重新核对，不存在目标可刷新恢复', async ({ page }) => {
  await openSchedules(page); await fillSchedule(page); await page.getByRole('button', { name: '保存日程' }).click(); await expect(page.locator('.schedule-row')).toHaveCount(1);
  await inject(page, 'schedule-stale'); await page.getByRole('button', { name: '删除日程' }).click(); await page.getByRole('button', { name: '永久删除', exact: true }).click();
  await expect(page.getByRole('dialog').getByRole('heading', { level: 2 })).toHaveText('日程已更新'); await page.getByRole('button', { name: '查看最新日程' }).click();
  await expect(page.getByRole('dialog').getByRole('heading', { level: 3 })).toContainText('已更新'); await expect(page.locator('.schedule-row')).toHaveCount(1);
  await page.getByRole('button', { name: '保留日程' }).click(); await inject(page, 'schedule-missing'); await page.getByRole('button', { name: '删除日程' }).click(); await page.getByRole('button', { name: '永久删除', exact: true }).click();
  await expect(page.getByRole('dialog').getByRole('heading', { level: 2 })).toHaveText('日程已不存在'); await page.getByRole('button', { name: '刷新列表', exact: true }).click(); await expect(page.getByRole('dialog')).not.toBeVisible(); await expect(page.locator('.schedule-row')).toHaveCount(0);
});

test('通知激活后返回创建，草稿仍在且消息未确认', async ({ page }) => {
  await openSchedules(page); await fillSchedule(page, '每周'); const controls = await page.context().newPage(); await controls.goto('/controls'); await controls.getByRole('button', { name: '点击肩颈通知' }).click();
  await expect(page.locator('.detail h2')).toContainText('站起来'); await page.getByRole('button', { name: '我的日程', exact: true }).click(); await expect(page.locator('#schedule-title')).toHaveValue('读完一本喜欢的书'); await expect(page.locator('#schedule-until')).toHaveValue('2027-03-31'); await controls.close();
});

for (const [key, name] of [['peach', '奶油桃'], ['mint', '薄荷绿'], ['sky', '晴空蓝'], ['lavender', '薰衣草紫']]) {
  for (const width of [1120, 900]) test(`${name} ${width} 创建与删除无溢出并截图`, async ({ page }, testInfo) => {
    await page.setViewportSize({ width, height: width === 900 ? 640 : 760 }); await openSchedules(page);
    await page.getByRole('button', { name: '设置', exact: true }).click(); await page.getByRole('button', { name: '外观', exact: true }).click(); await page.getByRole('button', { name: new RegExp(name) }).click(); await expect(page.locator('.app-background')).toHaveAttribute('data-theme', key);
    await page.getByRole('button', { name: '我的日程', exact: true }).click(); await fillSchedule(page, '每月'); await page.evaluate(() => document.fonts.ready);
    await expect(page.getByRole('button', { name: '保存日程' })).toBeInViewport(); expect(await page.locator('.content').evaluate(e => e.scrollWidth <= e.clientWidth)).toBe(true);
    await page.screenshot({ path: testInfo.outputPath(`${key}-${width}-create.png`) });
    await page.getByRole('button', { name: '保存日程' }).click(); await expect(page.locator('.schedule-row')).toHaveCount(1); await page.getByRole('button', { name: '删除日程' }).click();
    await expect(page.getByRole('button', { name: '永久删除', exact: true })).toBeInViewport(); await page.screenshot({ path: testInfo.outputPath(`${key}-${width}-delete.png`) });
    expect(await page.getByRole('dialog').evaluate(e => e.scrollWidth <= e.clientWidth)).toBe(true);
  });
}
