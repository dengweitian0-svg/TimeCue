# Issue #28 原型

- 本目录是独立 React + TypeScript + Vite 模拟演示，不依赖 Go 内部模块。
- `src/contracts.ts` 定义原型端口；`src/adapter.ts` 拥有持久化及模拟平台状态；`src/controller.ts` 编排应用用例；页面仅持有输入和展示状态。
- 所有本地存储写入经适配器完成，使用 `timecue:issue-28:v1` 命名空间。
- `src/Controls.tsx` 为独立场景控制页，不进入产品页面导航。
- `src/themes.ts` 与 Figma 语义变量一致；背景渲染通过 `Background` 容器扩展。
- 验证命令：`npm run typecheck`、`npm test`、`npm run test:e2e`、`npm run build`。
- 不得把模拟结果宣称为真实 Windows/Wails 验证；不得扩展日历、Snooze 或批量确认。
