# TimeCue 生产前端

这是 Wails 桌面应用使用的 React + TypeScript 前端。生产页面只接入日程创建、列表/游标分页、删除、关闭偏好、运行状态清理重试和激活握手；原型控制台、消息确认、目录设置与主题持久化不属于此入口。

`src/wails-adapter.ts` 的 `WailsAdapter` 只依赖可注入的 `BindingsPort`。默认 `createWailsAdapter` 在启动时延迟加载 `wailsjs/go/runtime/WailsBindings` 和 `wailsjs/runtime/runtime`，因为这些文件由后续 Wails 生成步骤提供；绑定生成前的单元测试通过端口注入完成，不创建假生成文件。Commit 9 接入正式 Wails 工程时保留该桥接边界，并让生成模块实现同一组方法。

创建和删除 mutation 使用 `JSON.stringify` payload，以保留后端 JSON 字段存在性；查询、偏好和 `FrontendReady` 使用 snake_case DTO。前端内部把安全范围内的 Go `int64` 标识呈现为 string，超出 JavaScript 安全整数范围的调用会明确失败。

验证命令：

```text
npm install
npm run typecheck
npm test
npm run build
```
