# Framescape v2 阶段 7 本地验证记录

更新时间：2026-09-27（Asia/Hong_Kong）。所有记录均在 `redesign/framescape-v2` 本地分支完成，未访问 Railway、未调用真实付费供应商。

## 已执行的退出项

- **构建与静态检查**：`go build ./...`、`go vet ./...`、前端 `npm run lint`、`npm run build`、`node scripts/security-review.mjs` 均通过。lint 仅保留既有 Toast Fast Refresh 和 comic optional-chaining 两条警告。
- **后端 race 回归**：隔离 MySQL/Redis/MinIO（13316/16386/19000）下 `go test -race -json ./...` 为 403 项通过、21 项按测试约定跳过、0 失败。
- **编排故障注入**：`internal/infra/orchestrator/orchestrator_test.go` 覆盖 worker 租约过期、异步任务不重复提交、重复投递、丢失 dispatch、发布失败重发、gate TTL、取消、供应商容量排队和并发 worker；`internal/interfaces/http/callbacks_test.go` 覆盖回调早于轮询和重复/未知回调；`cmd/fakeprovider` 单测覆盖 MiniMax 429/业务限流。
- **500 任务演练**：`FRAMESCAPE_LOAD_TEST=1 ... go test -race ./internal/infra/orchestrator -run TestPhase7FiveHundredQueuedJobs` 通过。500 个 mock 节点由 16 个 worker 并发处理，并对每条任务再次重复投递，均成功且没有重复执行。
- **SSE 扇出**：`FRAMESCAPE_LOAD_TEST=1 ... go test -race ./internal/infra/realtime -run TestPhase7ThousandSSEListeners` 通过。一个 Redis 订阅向 1,000 个监听器发送事件，未丢失。
- **读取接口负载烟测**：本地 API（隔离 DB + fakeprovider）300 请求、64 并发：`/internal/health` p95 18.9ms、`/api/v1/capabilities` p95 22.1ms，失败 0；fakeprovider 独立健康端点 p95 12.2ms。脚本为 `scripts/phase7-load.mjs`，只接受 loopback URL。
- **迁移回滚**：`scripts/phase7-migration-roundtrip.sh` 在临时数据库完整执行 1–26 升级、回滚 26、重放、回滚到 22、再次升级到 26，自动删除临时库；`migrations/migrations_test.go` 检查序号连续且每个迁移有 Up/Down。
- **前端无障碍烟审**：`web/tests/e2e/a11y-v2.spec.ts` 覆盖 23 个页面的中英文；可见按钮、链接、输入控件均有名称，图片均有 alt，dialog 均有标签，2/2 通过。完整响应式矩阵仍为 232/232，完整 Playwright 为 351/351。

## 仍需产品或运营确认的边界

- 这是本地 fakeprovider 和 mock executor 验证，不等同于真实供应商 SLA；没有提交真实生成请求。
- 500 任务和 1,000 SSE 是本机可重复演练，未声称达到生产 1,000 SSE/500 长视频持续压测的容量结论；上线前仍需在隔离环境执行更长时间的 k6/故障注入运行并保存 p95、恢复时间和资源曲线。
- P18 以及阶段 6 第二批私有评审页仍需产品团队签收；本地测试通过不能替代外部签收。
- 阶段 8 只做本地提交和合并，不部署 Railway。Railway 自动部署行为未在本轮验证。

## 重复运行

```bash
node scripts/security-review.mjs
./scripts/phase7-migration-roundtrip.sh

# 先启动隔离服务：make test-infra-up
FRAMESCAPE_LOAD_TEST=1 go test -race ./internal/infra/orchestrator \
  -run TestPhase7FiveHundredQueuedJobs -count=1
FRAMESCAPE_LOAD_TEST=1 go test -race ./internal/infra/realtime \
  -run TestPhase7ThousandSSEListeners -count=1

# LOAD_URL 必须是 localhost 或 127.0.0.1
LOAD_URL=http://127.0.0.1:8080/internal/health node scripts/phase7-load.mjs
```
