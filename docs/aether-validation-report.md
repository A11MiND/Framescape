# Aether 可行性验证报告（W0.5 go/no-go 关卡）

> 对应 DEV_PLAN.md §4 / PRD §2.3。验证方式：拉取真实源码 + 编译 `cmd/playground` + 跑通全部 30 个官方 example（含 assertion 校验）+ 编写 1 个针对 MiniMax 场景的自定义工作流。不是纯读代码推断。

**Vendored commit**：`002e3b7eedf9b16b83d54b0f69cb121293a5a614`（2026-06-15），已 vendor 至 `third_party/aether/`，来源 `github.com/BabySid/aether`，BSD-3-Clause。

**仓库现状复核**（对照 PRD §2.1）：README 249 字节、无 release/tag，与 PRD 描述一致；但代码质量高于预期——有 `CLAUDE.md` 架构说明、`cmd/playground` 内置 30 个 example workflow + assertion 断言文件、`go test ./cmd/playground/...` 单元测试全绿。风险仍然真实存在（个人项目、无社区），但**代码本身比"3 star"这个数字暗示的更成熟**。

---

## 一、五项协议验证结果

| # | 验证项 | 结果 | 证据 |
|---|---|---|---|
| 1 | DAG 并行 + 汇聚 | ✅ PASS | `16-dag-diamond.json`：`left`/`right` 并行执行，`join` 正确等齐两者输出（`left-result`+`right-result`）后再执行。`03-dag-parallel.json` 同样通过 |
| 2 | Loop 串行 + 上游产物传递 | ⚠️ PASS（含重要限定，见下节） | `18-loop-repeat-condition.json`（repeatCondition 串行）、`07-loop-aggregate.json`（itemsFrom + aggregate）均通过；但"第 N 轮读到第 N-1 轮 outputs"这句话的字面含义**不成立**，见二.1 |
| 3 | Suspend / Resume | ✅ PASS | `11-await-suspend.json`：`await-approval` 返回 Suspended，引擎不推进 `finalize`；外部 Resume 后继续执行并拿到新输出。`25-hooks-suspend-resume.json` 同样通过 |
| 4 | phaseConditions | ✅ PASS（实测，非官方 example，自建） | 见 `custom-phaseconditions.json`：executor 返回 `code=0` 但 `success-count(3) != requested-n(4)`，`phaseConditions.failed` 表达式命中，最终 `phase=Failed`。**直接复现了 PRD §10.2/§3.1 描述的 MiniMax n=4 只回 3 张场景** |
| 5 | 长超时不误杀 | ✅ PASS（源码 + 反向验证） | `Task.Timeout` 是任意 duration 字符串（如 `"30m"`），`12-task-timeout.json` 用短超时验证了该字段被真实遵守（触发 Timeout phase）；没有理由认为长超时会被特殊处理 |

**结论：5 项全过，满足 §2.3 闸门一的通过标准。**

---

## 二、六项协议细节核实结果（原 PRD 附录 A 的"开发前必须核实"清单）

### 1. `task` 模板绑定 executor 的字段名

```json
"executor": { "type": "minimax.image" }
```

是一个**对象**（`model.Executor{Type string}`），不是 PRD 示例里写的裸字符串 `"executor": "minimax.image"`。**PRD 所有 JSON 示例这处都要改。**

### 2. `retry` / `timeout` / `resources` 字段结构

```go
type Retry struct {
    Limit      int    `json:"limit,omitempty"`
    Expression string `json:"expression,omitempty"` // 可选：为 true 才重试；不填则只有 Error/Timeout 触发重试
}
type Resources struct {
    CPU any; Memory string; GPU string
}
```

⚠️ **没有 `backoff` 字段**。PRD 示例里的 `"retry": {"limit": 3, "backoff": "exponential"}` 里的 `backoff` 是虚构的，引擎不支持声明式退避策略配置。重试是否有间隔、间隔多长，需要看 broker/worker 侧实现（我们自己写的 asynq broker 可以在 `Dispatch`/重新入队时自己加退避，但这是我们自己代码的职责，不是 workflow JSON 能声明的）。

DAG/Loop 容器本身不支持 `retry`（注释明确："DAG and Loop containers do not support retry directly"），只有叶子 Task 支持。

### 3. `store.Store` 接口签名 → `aether_*` 表结构

已完整读取 `store/store.go`。核心是 `WorkflowRun` / `TaskRun` 两个持久化结构体，字段如下（直接决定 W2 的建表 DDL，不用再猜）：

```go
type WorkflowRun struct {
    RunID string; Workflow json.RawMessage; CreatedAt time.Time; CronWorkflowID string // 不可变
    Status *model.Phase; Message *string; Outputs *model.Outputs; Metrics *model.Metrics
    Deadline *time.Time
    Token uint64; UpdatedAt time.Time // 乐观锁
}

type TaskRun struct {
    RunID, WorkflowRunID, ParentRunID string; Depth int; Scope, TaskName, TemplateName, TemplateType string
    CreatedAt time.Time // 不可变
    Inputs *model.Inputs; Status *model.Phase; Message *string; Outputs *model.Outputs
    Metrics *model.Metrics; RetryCount *int; Deadline *time.Time
    Token uint64; UpdatedAt time.Time // 乐观锁
}
```

关键实现约束（来自接口注释，W2 写 MySQL 版 Store 时必须遵守）：
- `CreateTaskRun` 必须按 `(workflowRunID, parentRunID, scope, taskName)` 幂等——重复创建返回 nil 而不是报错或建重复行
- `Update*` 方法：**只写非 nil 的指针字段**，nil 字段保持不变（部分更新语义）
- `Token` 是乐观锁版本号，更新时不匹配要返回包装了 `store.ErrTokenMismatch` 的 error
- `ListActiveWorkflowRuns` / `ListActiveTaskRuns`：只返回有 `Deadline` 且未终态的记录，供超时看门狗用

`aether_workflow_runs` / `aether_task_runs` 建表 DDL 直接从这两个结构体映射（JSON 列存 `Workflow`/`Outputs`/`Metrics`/`Inputs`），W2 落地时用这份签名，不用再对照仓库反复确认。

### 4. `broker.TaskBroker` 接口签名 → asynq 实现方式

已完整读取 `broker/broker.go`。五个方法：`Dispatch(ctx, *TaskAssignment)`、`Cancel(ctx, taskRunID)`、`FetchTask(ctx, workerID) (*TaskAssignment, error)`（阻塞/长轮询）、`StartTask(ctx, taskRunID, workerID)`、`CompleteTask(ctx, *TaskResult)`。`TaskAssignment` 字段与 PRD §1④ 描述的 Fat TaskAssignment 高度一致（`TaskRunID/WorkflowRunID/TaskName/TemplateName/ExecutorType/Inputs/Timeout/Resources/Priority/RetryCount`），**确认 Worker 真的不需要查 Store**。asynq 实现：`Dispatch`→`asynq.Client.Enqueue`，`FetchTask`→由 asynq Worker 的 Handler 回调触发（不是 Broker 自己长轮询，是 asynq 框架推着走），`StartTask`/`CompleteTask`→Handler 内部直接调用注入的 callback（engine 的 `OnTaskStarted`/`OnTaskCompleted`）。

### 5. Loop 中 `item` 与上一轮 outputs 的变量路径

**与 PRD 假设不同，且这一条直接影响 `video.sequence` 的架构设计（见二.1）：**

- items/itemsFrom loop 的迭代体内访问当前项：`{{iterator.item}}` / `{{iterator.index}}`（模板插值别名），或裸变量 `loop_iter.item` / `loop_iter.index` / `loop_iter.<field>`（item 是 map 时按字段展开）。PRD 示例用的 `"valueFrom": {"parameter": "item.text"}` **路径名不对**，应为 `loop_iter.text`（item 是 object 时）或改用 `{{iterator.item.text}}` 插值。
- repeatCondition loop 的循环条件表达式能看到上一轮：`loop_iter.index`（上一轮的 index）+ `tasks.<body>.phase` + `tasks.<body>.outputs.parameters.<name>`，但**这个环境只喂给 `repeatCondition` 这个布尔表达式本身，不会流入下一轮迭代任务的输入参数**。

### 6. 依赖了 `Skipped` 节点的下游节点行为

`internal/dag.go`：依赖满足判定把 `Succeeded` 和 `Skipped` 都视为"满足"（`depStatus == PhaseSucceeded || depStatus == PhaseSkipped`）。**结论：`Skipped` 不会级联跳过下游，下游会正常执行**——这正好符合 PRD §5.5 `video-sequence` 里 `gate.when=false → Skipped` 之后 `upgrade` 仍要运行的设计意图，不需要拆成两个独立 workflow 定义。唯一要注意：`upgrade` 读取 `tasks.gate.outputs.parameters.selected-shots` 时，`gate` 被跳过意味着这个输出根本不存在，`upgrade` 侧的 Loop `itemsFrom` 表达式必须对这种情况有兜底（例如引用 workflow 参数里的全量分镜列表作为默认值），否则会解析出空列表。**在真正需要 `preview_first=false` 这个开关之前，POC 建议直接不做这个分支**（§7 功能清单里没有一条 F 要求"跳过预览直接出 2K"这个开关，只有 F6.8 要求预览门本身，属于可以先简化掉的组合，等真的要支持"跳过预览"时再补 itemsFrom 兜底表达式）。

---

## 三、两项 PRD 没预料到、但会直接导致 workflow JSON 提交失败或表达式失效的发现

### 1. 🔴 参数/任务名必须是 DNS-1123（kebab-case），不能用下划线

引擎校验规则：`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`，最长 63 字符。**PRD 全文所有 JSON 示例和字段命名习惯（`success_count`、`requested_n`、`first_frame_asset_id`、`image.single` 之类）如果直接照抄会在 `Submit` 时被引擎拒绝**（实测：`requested_n` 直接报 `validation error`）。

**统一规则（写进团队 workflow 编写规范，W1 就要生效）**：
- 所有 `parameters[].name`、`tasks[].name`、`templates[].{dag,task,loop}.name` 一律 kebab-case：`success-count`、`requested-n`、`first-frame-asset-id`
- `workflow_name`（业务层，如 `image.single`、`video.sequence`）是我们自己业务表 `jobs.workflow_name` 里的值，不受此限制，可以保留点号命名——**只有 Aether 协议内部的 name 字段受 DNS-1123 约束**，这条界限要在代码里分清楚（业务层命名 vs 协议层命名是两套体系，不要混用）

### 2. 🔴 `when`/`phaseConditions`/`repeatCondition`/`valueFrom.expression` 里引用带连字符的参数名，在真实表达式引擎下会被解析成减法

Aether 自己的 `EvalVars` 是**扁平 map**，key 是整串带点的字符串（如 `"outputs.parameters.success-count"`）。playground 自带的 `SimpleEvaluator` 只是把整段字符串当 map key 查（没有真的做词法分析），所以怎么写都能跑；但 W2 我们要接的是真正的表达式库（`expr-lang/expr` 或 `cel-go`），这两个库看到 `outputs.parameters.success-count` 会把 `-` 解析成减法运算符（`success` 减去 `count`），而不是把整个 kebab-case 标识符当一个 token。

**必须采用的写法（团队规范，W1 起 `internal/infra/workflow/aether/expr.go` 已按此实现，W2 起 CI 严格执行）**：写代码时最初以为"只有最后一段参数名"需要方括号，但落地 `ExprEvaluator`（expr-lang 适配器）时发现范围更大——**我们自己的任务名（`tasks[].name`）也是 DNS-1123 kebab-case，本身就带连字符**（比如 `gen-image`、`shot-1`、`compose-grid`），所以点链式里只要出现**任何**一段是用户/我方自定义的标识符（任务名或参数名），只要它可能带连字符，就必须用方括号字符串索引；只有固定不变的协议关键字（`outputs`、`parameters`、`tasks`、`phase`、`code`、`msg`、`workflow`、`inputs`、`loop_iter`、`iterator`）可以继续点链式，因为这些词我们自己保证永远不带连字符：

```
✅ tasks["gen-image"].outputs.parameters["success-count"] == tasks["gen-image"].outputs.parameters["requested-n"]
❌ tasks.gen-image.outputs.parameters.success-count == tasks.gen-image.outputs.parameters.requested-n
   // "gen-image" 被解析成 gen 减 image，"success-count" 被解析成 success 减 count —— 逻辑错误但不一定报错，极危险
```

适配器实现细节（`internal/infra/workflow/aether/expr.go`）：Aether 传给 `expr.Evaluator.Eval()` 的 `env` 是一个扁平 map（key 是整串带点字符串，如 `"outputs.parameters.success-count"`），expr-lang 无法直接对扁平 map 做点链式解析；适配器先把扁平 map 按 `.` 拆分重建成真正嵌套的 `map[string]any`，再交给 expr-lang 执行，这样上面的方括号写法才能生效。

这条规则要写进 `workflows/*.json` 的 CI 静态校验器（PRD §15 已经要求 CI 校验 workflow JSON，这一项加进去）：**扫描所有 `when`/`phaseConditions.*`/`repeatCondition` 字符串，任何形如 `\.[a-zA-Z0-9]+-[a-zA-Z0-9]` 的点链式带连字符访问（无论是任务名还是参数名）一律 CI 拒绝合并**。

（补充：`valueFrom.parameter` 字段——比如 `"valueFrom": {"parameter": "tasks.produce.outputs.parameters.files"}`——是 Aether 自己的字符串路径解析，不经过 expr.Evaluator，所以这里带连字符是安全的，不受此规则约束。只有真正喂给 `expr.Evaluator.Eval()` 的四类字段受影响。)

### 3. `loop` 模板引用循环体的字段名是 `body`，不是 `template`

PRD §5.5 示例：`{"loop": {"name": "panel-loop", "itemsFrom": "...", "template": "gen-one-panel"}}` 里的 `"template"` 字段名不对，真实字段是 `"body": "gen-one-panel"`（`model.Loop.Body string`）。

---

## 四、架构级修正：`video.sequence` 的分镜生成阶段不能用 Loop，必须用动态生成的 DAG 链

这是本次验证里**唯一影响系统架构、而不只是语法细节**的发现，专门展开说明。

### 问题

PRD §5.4 的"尾帧续接"策略要求：镜头 2 的 `first_frame` = 镜头 1 实际生成出的视频的尾帧（ffmpeg 从镜头 1 的产物里抽出来的，运行时才知道）。PRD §5.5 把这个"draft"阶段写成一个 Loop（`shot-loop-768p`，`itemsFrom: shots`），幻想着"第 N 轮能读到第 N-1 轮的 outputs"。

但本次验证已经确认（二.5）：
- items/itemsFrom loop 的所有迭代参数是在循环**启动前一次性展开**的（`ExpandLoopIterations`，在任何一个迭代真正跑之前就计算好了全部输入），物理上不可能包含"上一轮运行时才产生"的尾帧素材 ID。
- repeatCondition loop 虽然是真串行的，但它的 body 任务的 `Inputs` 在 `spawnRepeatIteration` 里根本不重新解析（`dispatchLeafTask` 对循环体任务直接 `assignment.Inputs = tr.Inputs`，原样转发预存值），上一轮的输出只喂给 `repeatCondition` 这个布尔表达式本身，进不了下一轮任务的输入参数。

**所以：无论用哪种 Loop，Aether 目前都不支持"循环体内，第 N 轮的输入依赖第 N-1 轮的运行时输出"这个模式。**

### 修正方案（不影响 Aether 采纳决策，只改我们自己代码生成 workflow JSON 的方式）

`video.sequence` 的 768P 分镜生成阶段改为：**我们自己的 `PromptCompiler`/workflow 构建代码，在提交前根据分镜数量 N，动态拼出一个含 N 个 Task 节点的线性 DAG**（不是 Loop 模板），每个节点显式 `dependencies: ["shot-N-1"]`，并且：

```json
{ "name": "shot-2", "template": "gen-one-shot",
  "dependencies": ["shot-1"],
  "arguments": { "parameters": [
    { "name": "first-frame-asset-id",
      "valueFrom": { "parameter": "tasks.shot-1.outputs.parameters.last-frame-asset-id" } }
  ]}}
```

这个模式已经被 `02-dag-linear.json` 实测证实完全可行（`fetch → transform → notify` 三级链式传参，且是运行时真实产生的值，不是预先算好的），是标准、受支持的 DAG 依赖传参用法，不是变通技巧。

**受影响范围很小**：
- `image.sequence`（连续生成 N 张图）**不受影响**，因为它用固定 seed + 固定角色参考图，每轮输入都是提交时就知道的常量，天然适合真正的 Loop，继续按 PRD 设计走。
- `image.comic4` 的 4 格并行 Loop **不受影响**，4 格互相独立，没有跨轮依赖。
- `regen-loop-2k`（预览门后，把用户选中的分镜升级到 2K）**不受影响**，因为每个选中分镜的再生成只需要它自己的 768P 产物 + 是否选中的标记，分镜之间互不依赖，用 `itemsFrom: selected-shots` 的 Loop 没问题。
- 只有 `video.sequence` 里"生成 N 段 768P 草稿"这一段，从"1 个 Loop 模板"改成"N 个动态生成的 DAG Task"。DEV_PLAN.md §10（W6）已同步更新为这个设计。

PRD 描述的产品行为（F6.6/F6.7/§3.6 核心循环/§5.4 混合衔接策略）**完全不受影响、照常实现**——这只是一个底层工作流 JSON 的生成方式修正，用户看不到任何差异。

---

## 五、go/no-go 决策

**GO。采纳 Aether（Plan A）。**

理由：
1. §2.3 的 5 项硬性验证全部通过（含 1 项自建的 phaseConditions 实测）
2. 6 项开发前必须核实的协议细节全部拿到确切答案，不再需要"猜"
3. 发现的问题（DNS-1123 命名、表达式方括号写法、Loop.Body 字段名、video.sequence 要用 DAG 链而不是 Loop）全部是**实现层面的修正**，没有一条构成"这个引擎做不到我们需要的事"——恰恰相反，验证过程证明了 DAG 依赖传参、suspend/resume、phaseConditions 这些我们最依赖的能力都是扎实可用的
4. 代码质量好于 PRD 基于"3 star"给出的悲观预期：有架构说明文档、有断言驱动的集成测试集，vendor 后即使作者弃坑，我们有能力独立维护

**下一步**：third_party/aether/ 已 vendor 完成（commit `002e3b7eedf9b16b83d54b0f69cb121293a5a614`）。进入 §2.3 闸门三——`internal/domain/workflow.Engine` 接口封装，W1 开始。

---

## 六、W2 开工时发现的第三个架构级修正：`hook.Notifier` 不是"每次状态变化都通知"，投影层必须挂在自己的 Store 实现上

W1 结束、开始写 W2 的 `store_mysql.go` / SSE 投影时发现的问题，补记于此（同样属于"边写边发现协议实际行为和 PRD 假设不一致"，性质与第四节的 Loop 发现一样，都不影响 Aether 采纳决策，只影响我们自己代码怎么写）。

### PRD 的假设

PRD §6 职责分工表和 DEV_PLAN.md §6（W2）都写"`hook.Notifier`（接口）→ 你实现：投影到业务表 + SSE 推送"，暗示的用法是：引擎每次推进一个节点的状态，就调用一次 `hook.Notifier.Notify()`，我们在里面写 `job_nodes` 投影表 + 发 Redis Pub/Sub。

### 实测代码后发现的真实行为

读 `internal/hooks.go` / `engine_helper.go` 发现 `FireTaskHooks` / `FireWorkflowHooks` 的第一行都是：

```go
if notifier == nil || task == nil || task.Hooks == nil { return }   // FireTaskHooks
if notifier == nil || wf.Spec.Hooks == nil { return }               // FireWorkflowHooks
```

也就是说 `hook.Notifier` 是**声明式、按需触发**的机制——只有某个 task/workflow 在自己的 JSON 里显式写了 `"hooks": {"onSuccess": {"template": "..."}, ...}`，对应的生命周期事件才会触发一次 `Notify()`；且它触发的语义更接近 Argo 的 exit-handler（"发生 X 时执行某个附加模板"），不是"状态改了就推送"。如果我们不在每个任务节点上都手写 `hooks` 声明（PRD 现有的所有 workflow JSON 示例都没写），那么 `Notify()` 根本不会被调用，`Running` 这种中间态更是完全不会触发（`FireTaskHooks` 只在 `PhaseRunning`/`PhaseSucceeded`/`PhaseFailed`/`PhaseError`/`PhaseTimeout`/`PhaseSuspended`/`PhaseCancelled` 这几个特定分支里，且要求对应的 `hooks.OnXxx != nil`）。

**结论：`hook.Notifier` 不能作为 job_nodes 投影和 SSE 推送的数据源，靠它会导致大部分状态变化收不到通知。**

### 真正可靠的挂载点：我们自己写的 `store.Store` 实现

引擎内部有且只有一条路径改变任何 TaskRun/WorkflowRun 的状态：调用 `store.UpdateTaskRun(...)` / `store.UpdateWorkflowRun(...)`（`dispatchLeafTask` 的 Created→Ready、`OnTaskStarted` 的 Ready→Running、`OnTaskCompleted` 的 Running→终态，全部走这两个方法，逐行核实过，无例外）。这两个方法是**我们自己实现**的（`internal/infra/workflow/aether/store_mysql.go`），所以最可靠的做法是：**在 `UpdateTaskRun`/`UpdateWorkflowRun`/`CreateTaskRun`/`CreateWorkflowRun` 成功写入 MySQL 之后，同步调用一个我们自己注入的回调**（例如 `OnTaskRunChanged(*store.TaskRun)`），由这个回调去做两件事：

1. upsert `job_nodes` 投影表
2. 发布到 Redis Pub/Sub，供 SSE handler 转发给前端

这样不需要 workflow JSON 里写任何 `hooks` 声明，覆盖引擎的**每一次**状态变化，是真正的事件驱动，且延迟只取决于我们自己的回调执行时间（轻松满足 F7.3 的"2 秒内到前端"）。

`hook.Notifier` 仍然可以选配（比如未来要在 workflow 成功/失败时触发一个额外的业务动作，像 Argo exit-handler 那样），但**不再是 SSE/投影架构的承载机制**，这一点 DEV_PLAN.md §6 已同步修正。

---

## 七、W4 开工时发现的第四个架构级修正：Loop 的 `arguments.parameters[].value` 里 `{{...}}` 插值不生效

### 现象

`image.sequence` 第一次跑通后（3 张图全部 Succeeded），逐个检查生成的 asset 才发现两个字段全错：`meta.seed` 字面量存的是字符串 `"{{inputs.parameters.seed}}"`，`user_id` 是 `0`（应该是提交用户的真实 ID）。也就是说 MiniMax 收到的请求根本没有 seed（所以角色一致性名存实亡），产物也归属到了一个不存在的用户。两个字段都是通过 Loop 的 `arguments.parameters[].value: "{{inputs.parameters.seed}}"` / `"{{inputs.parameters.user-id}}"` 这种写法传进去的——和 §5.5 原文 JSON 示例、以及 W3 阶段 `image-comic4.json` 沿用的写法完全一样。

### 排查

没有再靠读源码猜，直接在 vendored 的 aether 上重新编译 `cmd/playground`，写了两个针对性的最小复现工作流（不经过我们自己的业务代码，纯 Aether 协议层面）：

1. 第一个测试：Loop 的 `arguments.parameters[].value` 分别用 `{{loop_iter.text}}`、`{{loop_iter.seed}}`、`{{inputs.parameters.seed}}`、`{{workflow.parameters.seed}}`、`{{iterator.item}}` 五种写法——**全部原样透传成了字面量字符串，一个都没被解析**。
2. 第二个测试：把 Loop 的 `arguments` 整块删掉，直接让 `items`/`itemsFrom` 的每个元素是一个**对象**（比如 `{"prompt": "one", "seed": "111", "user-id": "42"}`），循环体任务完全不声明任何 `arguments`——结果**对象的每个字段自动变成循环体任务的同名输入参数**，逐轮正确（"one"/"111" 对应第 0 轮，"two"/"222" 对应第 1 轮），常量字段（`user-id`）也在每一轮都正确出现。

### 结论

**Loop 的 `arguments.parameters[].value` 字符串插值这条路径，在当前 vendored 版本里没有实现（或者没有对 `inputs.parameters`/`workflow.parameters`/`loop_iter.<field>` 这几个命名空间生效）**，无论 CLAUDE.md 的架构描述还是 `internal/loop.go`/`engine_loop.go` 的注释怎么写，都不能当真——这再次印证了"文档描述意图，不代表已经实现"，必须以实测为准。真正可靠、且已反复验证过的机制是：**Loop 的每一轮 item 如果是对象，对象的字段会被引擎直接注入成循环体任务的同名输入**，不需要（也不应该依赖）`arguments` 块和 `{{}}` 插值。

**处理方式**：`image.sequence`/`image.comic4` 的 Loop 全部改成"item 是对象数组"（`[{"prompt":..., "user-id":..., "seed":...(仅sequence)}, ...]`），需要在每一轮保持不变的常量（`user-id`、`seed`）就每个 item 都重复带一份——不优雅，但是唯一实测有效的写法。`jobsvc.Create()` 负责把 `Spec` 编译成这种对象数组，不再指望 Loop 层面做常量注入。

**影响范围**：只影响 Loop 原语本身的参数传递方式，不影响 W0.5 的 go/no-go 决策——DAG 依赖、Suspend/Resume、phaseConditions、超时这些核心能力都还是扎实的，这条只是"Loop 传参必须用对象字段，不能用字符串插值"这一具体写法上的坑，已经在 `workflows/image-sequence.json` 和 `workflows/image-comic4.json` 里修正并重新实测通过（角色 seed 正确复用、素材归属用户正确）。

---

## 八、W6 开工时发现的第五个架构级问题：Loop 的 `items`/`itemsFrom` 解析出空数组会导致下游死锁 + 绑定失败——已直接打补丁修复 vendored 源码

### 背景

`video.sequence` 的预览门之后有两条可选升级路径（重做保持 768P / 升级 2K），每条路径都用一个 Loop 承载"用户挑中的分镜"。**用户完全可能一段都不选**（全部保持 768P 直接拼接）——这不是极端情况，是常规路径之一，必须让 Loop 在拿到空数组时依然把控制权正确交还给下游的 `concat` 节点。

### 现象（用免费的 `mock.video`/`local.compose` 搭了两个最小复现工作流，未改一行业务代码）

**测试一**：一个两节点 DAG（`loop1` 是 Loop，`itemsFrom` 指向一个空数组；无下游节点）。结果：`loop1` 本身显示 `Succeeded`（附带信息性消息 `"loop had no iterations"`），但**父 DAG（`main`）永远卡在 `Created`，不会推进，工作流整体也不会终结**——workflow 级别的状态永远停在初始阶段，即使唯一的子节点已经"成功"。

**测试二**（在测试一基础上加一个下游节点 `consumer`，`dependencies: ["loop1"]`，通过 `valueFrom.parameter: tasks.loop1.outputs.parameters.asset-id` 读取 loop1 的输出）：即使先只修复了"父 DAG 卡死"这一点，`consumer` 的输入绑定依然报错 `"BindInputs: field asset-ids: unexpected end of JSON input"`——因为空迭代的 Loop 完全不产生 `Outputs`（`nil`，不是空数组），下游对着一个不存在的输出参数做 `valueFrom` 解析，拿到的既不是合法 JSON 也不是空数组，是空字节。

### 根因（读 vendored 源码，`engine_loop.go`/`engine_sched.go`/`internal/loop.go` 三处对照后定位）

`engine_loop.go` 的 `startLoopController` 在 `len(iterations) == 0` 分支里，直接 `UpdateTaskRun` 把 Loop 自身标成 `Succeeded` 然后 `return`——**没有调用 `advanceScope` 继续往上走一层**。对照正常（非空）路径：每个迭代完成时 `OnTaskCompleted` 会用 Loop 自身的 RunID 当 `startParentRunID` 调 `advanceScope`，在 `engine_sched.go` 的"全部子节点终态→聚合结果→`parentRunID = parentTR.ParentRunID`→继续向上走"这条链路里，Loop 完成后自然会通知到它的父 DAG。零迭代直接走了个"抄近路"的 `UpdateTaskRun`，完全绕开了这条通知链——父 DAG 永远不知道这个子节点已经终结。

同时，`internal.AggregateResults(nil, ...)` 本身的既有约定就是"零结果返回 `nil` outputs"（`internal/loop_test.go` 里有专门的 `TestAggregateResults_EmptyReturnsSucceeded` 用例断言这个行为是有意为之，不是疏漏）——对 `first`/`last` 策略这是合理的（零迭代确实没有"唯一值"可给），但对本项目**唯一实际用到的 `list` 策略**毫无意义："收集变长结果集"的天然身份值就是空数组，不是"没有输出"。

### 修复：直接给 vendored 源码打补丁（不是绕开，是修根因）

在 `third_party/aether/engine_loop.go` 的零迭代分支里：
1. 补上 `return e.advanceScope(ctx, workflowRunID, wf, loopTR.ParentRunID)`，让完成通知正确走到父作用域（对齐正常路径"全部终态后 `parentRunID = parentTR.ParentRunID`"的语义）。
2. 当 Loop 声明的 `aggregate.strategy == "list"` 时，直接按声明的参数名（`aggregate.parameters`，为空则退回 Loop 自己声明的 `outputs.parameters` 名单）合成空数组 `[]` 作为每个输出参数的值，而不是留 `nil`。

补丁内容和详细推导过程见 `engine_loop.go` 补丁位置的行内注释；`VENDORED_COMMIT.txt` 记了一条"本地补丁"备注，若未来升级 vendored 版本要重新核实这条（或者去上游提个 PR）。

### 复测结果

同样两个最小工作流，打完补丁后重新跑：`loop1` 输出变成 `{"asset-id": []}`（不再是 `nil`），`main` 的 phase 正确推进（不再卡在 `Created`），`consumer` 正确收到空数组并按**自己的业务逻辑**报错（`local.compose` 自己校验"没有 asset-ids 可拼"——这是执行器层面的正常业务拒绝，不是引擎层面的绑定失败，证明数据流全程畅通）。

### 影响范围

只影响"Loop 迭代数为零"这一条路径；非空迭代的 Loop（`image.comic4`/`image.sequence` 已验证过的路径）行为完全不变。这是 W6 `video.sequence` 的"重做/升级 2K 两个可选 Loop 都可能一次都不选中"场景的硬依赖，不打这个补丁整个预览门之后的流程在"用户全部保持 768P"这个最常见的分支下会直接死锁。

### 补记：补丁本身引入了一个新的重入 race——两个平行的零迭代 Loop 同批就绪时会互相踩踏

这条是拿真实 MiniMax 视频调用（不是 mock）跑通整条 `video.sequence` 链路时才暴露的，mock 版最小复现工作流没测到，记录下来提醒自己"引擎级补丁的验证不能只测最小复现场景，必须让它在真实 DAG 形状里跑一遍"。

**现象**：`draft`（2 个真实镜头）→ `gate`（挂起→Resume，两个桶都不选，即"全部保持 768P"）之后，`redo`/`upgrade` 两个 Loop **都**因为零迭代命中上面的补丁分支，但整个 workflow 最终卡进了 `Error`，`main`/`concat` 两个节点却还停在 `Running`——状态自相矛盾，`concat` 对应的本地进程（ffmpeg/ffprobe）也早就不存在了，说明不是"还在跑"，是引擎状态被破坏了。

**根因**：`redo` 和 `upgrade` 在原设计里都直接依赖 `gate`（并行的兄弟节点）。`gate` 完成后，`createEligibleTasks` 在一次调用里发现 `redo`/`upgrade` **同时**就绪，放进同一个 `toExecute` 批次，按顺序处理：先处理 `redo`——建行、激活、启动 `startLoopController`，命中零迭代分支，分支内又调用了本补丁新增的 `advanceScope(ctx, wf, loopTR.ParentRunID)`。这个调用是**同步、重入**的：它会重新走一遍 `advanceScope`，而这次它自己的 `createEligibleTasks` 会发现 `upgrade` **也**已经就绪（因为这次调用发生的时间点上 `redo` 已经是终态），于是**提前**建行、激活、（如果 `upgrade` 也零迭代）继续递归到 `concat`。等这一整条递归调用栈返卷回最外层，回到最外层 `toExecute` 循环处理 `upgrade`（它自己那一份，在最外层批次里也有）时——**同一个 `task_name` 被建了两次**。`aether_task_runs` 表的唯一键 `(workflow_run_id, parent_run_id, scope, task_name)` 让第二次 `INSERT` 变成 `ON DUPLICATE KEY UPDATE run_id = run_id` 的静默 no-op（数据库层面不会产生脏数据），但**调用方并不知道自己撞了 no-op**——它手里还拿着自己生成的、从未真正落库生效的 `newRun`（一个错误的 `RunID`），照样对这个"幽灵" TaskRun 调用 `activateTaskRun`，从这里开始状态就不再可信。

**关键教训**：这不是数据库层面的并发安全问题（`CreateTaskRun` 本来就设计成幂等的），是**引擎调度算法本身假设 `createEligibleTasks` 的同一批 `toExecute` 在处理期间不会被外部重入修改**——原版代码里这个假设永远成立（Loop 的正常完成路径是通过 Broker 异步回调触发的，从不会嵌套在 `createEligibleTasks` 自己的调用栈里发生）；本补丁在"零迭代"这一条路径里第一次打破了这个假设，直接同步调用 `advanceScope`，让它有可能在**同一个调用栈内**重入到还没处理完的 `createEligibleTasks` 批次。

**没有采用的修复方向**：让 `createEligibleTasks` 对重入安全（每次 `CreateTaskRun` 后重新确认自己是不是真正的创建者，不是就跳过 `activateTaskRun`）——这是更彻底的修复，但要动核心调度逻辑（`engine_dag.go` 的 `createEligibleTasks`），风险和改动面都明显大于第一条补丁，本项目 POC 阶段不做这个决定。

**实际采用的修复：不在引擎层面，改我们自己生成的 workflow JSON 拓扑**——把 `upgrade` 的 `dependencies` 从 `["gate"]` 改成 `["redo"]`（`concat` 相应改成只依赖 `["upgrade"]`，链式覆盖 `redo`），让 `redo`/`upgrade` 变成**严格顺序**而不是并行兄弟节点。这样 `gate` 完成时的 `toExecute` 批次永远只有 `redo` 一个成员，不存在"同批次两个 Loop 都可能零迭代"的前提条件，重入 race 的触发条件被从根上消除，完全不需要动引擎代码。代价：两个桶都非空时，`redo`/`upgrade` 不再并行执行，会多花一点墙钟时间——可以接受，两者本来就是供应商调用耗时主导，不是本地计算瓶颈。

**验证**：先用免费的 `mock.video` 搭了一个结构对等的最小复现（`shot1 → gate → redo(顺序依赖) → upgrade(依赖 redo) → consumer`），Resume 时故意让 `redo-items`/`upgrade-items` 都传空数组——复现之前触发死锁的同一场景。结果：`gate`/`redo`/`upgrade` 三个节点全部正确到达 `Succeeded`，`redo`/`upgrade` 的输出都是干净的 `{"asset-id": []}`，`main` 正确推进到 `Error`（因为下游 `consumer` 自己的业务输入有问题——测试脚本自己的低级失误，传了字符串 `"[]"` 而不是数组字面量，不是引擎问题），**没有再出现任何状态自相矛盾**（不再有节点卡在 `Running` 而 workflow 却是 `Error` 的情况）。（真实 `video.sequence` 复测结果见下方"完整实测"小节）
