# Issue #28 原型

- 本目录是独立 React + TypeScript + Vite 模拟演示，不依赖 Go 内部模块。
- `src/contracts.ts` 定义原型端口；`src/adapter.ts` 拥有持久化及模拟平台状态；`src/controller.ts` 编排应用用例；页面仅持有输入和展示状态。
- `src/schedule-model.ts` 负责日程表单转换、受限重复规则和适配器最终校验；日程定义与已投递消息分别存储，创建与删除不确认消息。
- 单次时间范围暂对齐当前 Go：开始位于 `[当天零点, 夹取后下一年零点)`，结束允许等于上界。边界由适配器提供并在写入前重新验证；不得按固定秒数计算一年。
- 所有本地存储写入经适配器完成，使用 `timecue:issue-28:v1` 命名空间。
- `src/Controls.tsx` 为独立场景控制页，不进入产品页面导航。
- `src/themes.ts` 与 Figma 语义变量一致；背景渲染通过 `Background` 容器扩展。
- 验证命令：`npm run typecheck`、`npm test`、`npm run test:e2e`、`npm run build`。
- 不得把模拟结果宣称为真实 Windows/Wails 验证；不得扩展日历、Snooze 或批量确认。
