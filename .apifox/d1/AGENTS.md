# D1 接口验收资源

- `occurrence-schema.json`：Occurrence 的完整 Schema 更新 payload，支持 loaded 与投影空状态。
- `case-1.json` 至 `case-5.json`：loaded 查询、详情、近期创建、确认和非法筛选的测试 payload。
- `case-ids.json`：对应 Apifox 用例 ID。
- `reports/`：真实执行 JSON 报告及 summary.json 汇总。

项目 8902401，环境 49486135，AI 分支 8703058。写入前 cli-schema validate，写入后在同一分支 get 回读并运行；报告不得保存访问令牌。验收使用隔离新库，原业务库不用于测试。实现说明与执行证据见 ../../docs/phase-d1-validation.md。
