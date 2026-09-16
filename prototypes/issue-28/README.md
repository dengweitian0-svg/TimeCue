# TimeCue · 卡通桌面原型 · Issue #28

柔和卡通风格的日程创建与删除、桌面设置、提醒处理及 Windows 生命周期演示。默认奶油桃主题，另有薄荷绿、晴空蓝、薰衣草紫；支持 1120×760 与 900×640 窗口。

## 打开设计

- [可编辑 Figma 文件](https://www.figma.com/design/mmHsccec0PB3Mf8y97BbrA)
- [交互原型：从评审导航开始](https://www.figma.com/proto/mmHsccec0PB3Mf8y97BbrA?node-id=27-1236&starting-point-node-id=27%3A1236)
- [创建与删除日程原型](https://www.figma.com/proto/mmHsccec0PB3Mf8y97BbrA?node-id=49-2986&starting-point-node-id=49%3A2986)
- [主要页面](https://www.figma.com/design/mmHsccec0PB3Mf8y97BbrA?node-id=10-2)
- [主题与设计基础](https://www.figma.com/design/mmHsccec0PB3Mf8y97BbrA?node-id=4-2)
- [验收说明](https://www.figma.com/design/mmHsccec0PB3Mf8y97BbrA?node-id=31-2)

Figma 文件位于当前可用团队的草稿区。六个页面分别是设计基础、主要页面、交互流程、异常状态、验收说明、日程创建与删除。原型通过命名场景展示具体路径；跨场景重新进入使用该场景的初始数据。真实控件填写和刷新持久化以 React 演示为验收工具。

## 本地运行

在本目录执行，需 Node.js 24+：

```powershell
npm ci
npm run dev
```

打开终端输出的本地地址，默认是 `http://127.0.0.1:5173`。产品页为 `/`，独立场景控制台为 `/controls`。可在两个同源页签中分别打开，控制台触发的平台事件会同步到产品。

```powershell
npm run typecheck
npm test
npm run test:e2e
npm run build
```

浏览器测试默认使用本机 Chrome。可设置 `PLAYWRIGHT_CHANNEL=msedge` 使用 Edge；也可在配置中去掉 `channel`，运行 `npx playwright install chromium` 后使用 Playwright 浏览器。所有测试使用独立浏览器上下文，不修改用户浏览器数据。

中文字体由 `@fontsource/noto-sans-sc` 本地打包；启动后不依赖在线字体服务。`npm ci` 首次需要下载依赖。

## 演示方式

### 创建与删除日程

1. 在侧栏打开“我的日程”，点击“添加日程”，填写标题、备注和起止时间；默认单次，切换每天、每周或每月时必须填写截止日期。
2. 每周取首次日期的星期，每月取首次日期的日数；短月回退到月末，下月恢复原日期。保存后列表显示实际保存的定义和备注，刷新后再次打开“我的日程”仍可核对。
3. 点击行末“删除日程”。重复日程明确删除整组；确认后永久删除，无撤销，刷新后不再出现。创建或删除不会确认既有待处理消息。
4. 保持产品页打开，在另一页签的 `/controls` 注入“创建日程保存失败”“删除日程失败”“删除时日程已更新”或“删除时日程已不存在”，返回产品走对应操作。失败保留输入或日程；版本过期先查看最新内容再确认。
5. 表单 Tab/Shift+Tab 可遍历控件。Esc 或取消显示放弃确认；继续填写保留草稿。导航离开有输入的表单也需确认；通知激活暂时切页后返回仍保留草稿。

日程定义使用当前本机时钟，初始集合为空；旧版快照会兼容迁移为空集合，不从提醒消息推造日程。日程模拟不会实际运行计时器投递新通知。单次范围暂对齐当前 Go 实现，详见[交互规则](docs/interaction.md#日程创建与永久删除)。

### 设置与提醒

1. 查看、切换同刻或此前提醒，观察列表数量不变；确认后先显示保存中，成功才移除。
2. 在 `/controls` 注入下一次确认失败，返回产品确认；失败保留消息，再次确认可成功。
3. 在设置中选择关闭偏好并保存，再点击右上角关闭；默认隐藏，选择退出后进入正式停止流程。
4. 日志与备份分别输入中文/空格路径或选择模拟目录。注入验证、切换失败或取消；切换失败会显示实际生效的临时目录并保留输入。
5. 在控制台设置窗口尚未就绪，点击消息通知，再设置就绪；产品定位最后一次请求，保持未确认状态。
6. 注入通知清理失败并退出；桌面保留残留通知。重试清理成功后通知消失。退出后的残留通知点击明确提示冷启动待验证。

所有目录选择、系统通知、托盘和生命周期都是模拟，不访问真实目录、不发送 Windows 通知、不控制真实进程单实例。`/controls` 的重置只覆盖 `timecue:issue-28:v1` 命名空间。

## 文档

- [交互规则与架构边界](docs/interaction.md)
- [主题与媒体背景扩展](docs/themes.md)
- [Issue 验收映射与验证记录](docs/acceptance.md)
- [Figma 状态索引](design-state.json)

本目录交付 Figma 设计依据与 React 模拟原型。真实 Wails、Windows 通知与目录切换由关联 Issue 实现。
