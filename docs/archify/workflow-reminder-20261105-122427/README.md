# TimeCue 提醒全流程

[打开交互图表](reminder.html) · [可编辑源文件](candidate.json) · [最终校验摘要](review-2/reminder.finalize-summary.json) · [最终截图预览](review-2/visual-check/reminder.visual-check.html)

类型：Workflow v2；语言：简体中文；静态展示，支持主题切换、查看源码和图片/SVG 导出。

## 覆盖范围

- 日程规则正式写入后刷新近期提醒，30 秒扫描与完整分钟核验，先持久化 loaded 再入内存队列。
- 重启、时钟前跳和未开始旧分钟的历史恢复；按可证明版本、固定上界分批补齐，每批最多 500 条，完成后开放汇总投递。
- Flutter 运行身份核验与串行轮询；Go 完整任务领取、开始前资格核验、冻结成员及文案，有效开始才消费预算。
- Windows 即时通知、已知结果回传、失败后 5/30/120 秒退避及最多四次有效开始；图中“返回 claim / start”指向上方同一套协议，不是额外展示步骤。
- 平台结果不明时保留未知，不伪造 failed、不自行重复展示；明确执行者退出与可信迟到 sent 单独说明。
- 点击通知的只读定位、任务/attempt 成员分页、恢复进度，以及独立的今日/历史待确认入口与原子确认。
- 窗口隐藏继续投递、退出停止新任务并在共享预算内尽力回填。

Go 持有规则、资格、重试及业务状态，Flutter 调用平台并回报结果。投递状态与用户确认分别保存；sent 仅证明平台接受展示。确认不要求曾经投递成功，点击或查看不确认。页面未展开 AI、模型调用或打包发行流程。

## 代码依据

仓库：`https://github.com/dengweitian0-svg/TimeCue.git`

固定版本：`6a0afbcdbfca5659d87c3b4137ab788fc8b36058`

分支：`docs/reminder-workflow`，从本地 dev 创建独立 worktree。

节点均附已核对的源码路径及行号。图表只反映该提交的实际实现；生成时原工作区的未提交修改未作为依据。

## 验证与证据

最终候选及 HTML 的哈希见 [review-2 校验摘要](review-2/reminder.finalize-summary.json)。源码引用、showcase 结构、交付来源、严格产物检查及真实 Chrome 浏览器检查均通过，最终诊断为零。最终截图检查覆盖浅色/深色、1440×900 与 2048×1320；自动检查包含边界、可读性、主题与查看器控件。

截图捕获不等于人工观感判断；人工查看情况单独记在 [视觉检查说明](VISUAL-REVIEW.md)。`review-2/` 为当前 HTML 的最终验证证据；目录根部较早的 finalize/browser-check 回执及 `visual-check/` 保留前一版的过程证据，应按各自 SHA-256 区分，不能用于证明当前 HTML。

本次仅新增文档，不变更业务代码、HTTP 或数据库；未以文档校验代替真实系统通知的业务测试。

## 重新生成

在 worktree 根目录运行，将 `<archify>` 替换为本机技能包目录：

```powershell
node <archify>/bin/archify.mjs finalize workflow docs/archify/workflow-reminder-20261105-122427/candidate.json docs/archify/workflow-reminder-20261105-122427/reminder.html --repo-root . --quality showcase --out-dir docs/archify/workflow-reminder-20261105-122427/review-3 --json
```

更改候选后使用新的证据目录；不要手工修改生成的 HTML 或覆盖不属于本次产物的回执。图表源文件、HTML 和配套文档/校验/截图证据一起交付；临时目录不纳入提交。

本目录的 `.gitattributes` 保留 JSON/HTML 原始换行字节，避免 Git 自动转换后破坏 SHA-256 校验；生成 HTML 中的模板行末空白按原样保留。
