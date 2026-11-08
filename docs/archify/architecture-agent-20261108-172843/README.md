# TimeCue Agent 架构与流程图

从 [HTML 导航页](index.html) 可以打开本组全部 9 张图。先看[总体架构](overview/overview.html)了解模块边界，再看[一轮对话的主流程](run-flow/run-flow.html)。模块图展开各自的分支、事务和恢复规则。图中的「来源」按钮可打开固定版本的源码行。

## 图表索引

| 图表 | 类型 | 覆盖范围 | 可编辑源文件 | 校验回执 |
| --- | --- | --- | --- | --- |
| [总体架构](overview/overview.html) | Architecture | Flutter、本机 HTTP、chat、completion、tools、App 和 SQLite 的职责与依赖 | [JSON](overview/candidate.json) | [摘要](overview/overview.finalize-summary.json) · [浏览器](overview/overview.browser-check.json) |
| [一轮对话的主流程](run-flow/run-flow.html) | Workflow | 从消息提交和运行准入，到模型分支、工具回填与终态 | [JSON](run-flow/candidate.json) | [摘要](run-flow/run-flow.finalize-summary.json) · [浏览器](run-flow/run-flow.browser-check.json) |
| [会话与运行准入](admission/admission.html) | Workflow | 同会话原文幂等、模型门禁、占用限制和创建事务 | [JSON](admission/candidate.json) | [摘要](admission/admission.finalize-summary.json) · [浏览器](admission/admission.browser-check.json) |
| [模型流式调用与有限重试](model/model.html) | Workflow | 请求登记、流式协议、首包和空闲超时、原调用重试 | [JSON](model/candidate.json) | [摘要](model/model.finalize-summary.json) · [浏览器](model/model.browser-check.json) |
| [日程工具执行、重试与回填](tools/tools.html) | Workflow | 六个日程工具、并发与串行门禁、删除回执和全批配对 | [JSON](tools/candidate.json) | [摘要](tools/tools.finalize-summary.json) · [浏览器](tools/tools.browser-check.json) |
| [上下文预算、摘要与续写](context/context.html) | Workflow | 有效历史、创建前和工具后摘要、正常 length 续写、工具内容裁剪 | [JSON](context/candidate.json) | [摘要](context/context.finalize-summary.json) · [浏览器](context/context.browser-check.json) |
| [SQLite 事务、SSE 补读与用量](events/events.html) | Workflow | 持久化批次、事件游标、204 恢复、按需用量 | [JSON](events/candidate.json) | [摘要](events/events.finalize-summary.json) · [浏览器](events/events.browser-check.json) |
| [取消、退出与重启恢复](recovery/recovery.html) | Workflow | 取消投影、只读回执、共享退出期限和持久化故障收尾 | [JSON](recovery/candidate.json) | [摘要](recovery/recovery.finalize-summary.json) · [浏览器](recovery/recovery.browser-check.json) |
| [模型连接保存与热加载](config/config.html) | Workflow | 多模型预算、密钥保留、版本匹配和实际请求门禁 | [JSON](config/candidate.json) | [摘要](config/config.finalize-summary.json) · [浏览器](config/config.browser-check.json) |

## 版本与验证

- 源码固定在提交 `640dbcbae92785295bcd610e490ff3eef333ce45`。图表基于生产装配和该提交的实现，证据范围见各图节点。 [固定源码版本](https://github.com/dengweitian0-svg/TimeCue/tree/640dbcbae92785295bcd610e490ff3eef333ce45)
- 9 张图均通过 Archify Showcase 9/9 产物检查，零错误、零警告。真实 Chrome 自动检查通过，覆盖四种桌面尺寸、主题、可读性和查看器状态。各图摘要记录源文件和 HTML 的 SHA-256，同目录保存完整 finalize、交付和浏览器回执。
- 总体架构、对话主流程、工具执行三张图额外生成了双主题截图。逐张查看了 1440×900 浅色和 2048×1320 深色下的节点、箭头、标签和泳道，未发现遮挡或裁切。其他六张图以自动校验结果为准。 [总体架构截图](overview/visual-check/overview.visual-check.html) · [主流程截图](run-flow/visual-check/run-flow.visual-check.html) · [工具执行截图](tools/visual-check/tools.visual-check.html)

## 重新生成

在仓库根目录运行，把 `<archify>` 替换为本机技能目录。总体架构使用 `architecture`，其余图使用 `workflow`。修改候选后用新的 `--out-dir` 保存该版本的回执。

```powershell
node <archify>/bin/archify.mjs finalize architecture docs/archify/architecture-agent-20261108-172843/overview/candidate.json docs/archify/architecture-agent-20261108-172843/overview/overview.html --repo-root . --quality showcase --out-dir docs/archify/architecture-agent-20261108-172843/overview/review-2 --json
```
