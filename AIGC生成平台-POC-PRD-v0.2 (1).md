# AIGC 内容生成平台 · POC 产品与技术方案（PRD v0.2）

> **v0.2 相对 v0.1 的重大变更**
> 1. 编排层从「自研 Recipe + DAG 编排器」改为 **采用 Aether 协议（aether/v1）**，并附第 1 周 go/no-go 决策关卡
> 2. 模型供应商锁定 **MiniMax**（图片 image-01 / image-01-live，视频 MiniMax-H3）
> 3. **图片接口是同步的、视频接口是异步的** —— 两套完全不同的执行形态，v0.1 的统一三段式作废
> 4. 「批量出图」由 N 个并行 Task 改为 **1 个 Task（n=1..9）**
> 5. 新增核心产品循环：**768P 预览 → 用户挑选 → 2K 定稿**（基于 Aether suspend/resume + MiniMax 再生成）
> 6. 「连续影片」的 provider 原生 extend 路径**删除**（MiniMax 无此能力），只保留尾帧续接
> 7. 积分体系改为基于 MiniMax 实际刊例价推导，附完整成本表
> 8. **新增第 19、20 章**：UI 设计规范与详细页面规格（参考即梦/剪映的设计语言与交互模式）、用户画像与端到端用户案例

---

## 目录

1. [竞品与技术选型反推](#1-竞品与技术选型反推)
2. [Aether 采纳决策与风险控制](#2-aether-采纳决策与风险控制)
3. [MiniMax 能力与约束基线](#3-minimax-能力与约束基线)
4. [产品定义与范围](#4-产品定义与范围)
5. [核心领域模型](#5-核心领域模型)
6. [职责分工：Aether 做什么，你做什么](#6-职责分工)
7. [功能需求清单](#7-功能需求清单)
8. [技术架构](#8-技术架构)
9. [数据库设计](#9-数据库设计)
10. [执行器（Executor Plugin）设计](#10-执行器设计)
11. [调度、并发与可靠性](#11-调度并发与可靠性)
12. [积分与成本模型](#12-积分与成本模型)
13. [API 设计](#13-api-设计)
14. [前端方案](#14-前端方案)
15. [工程落地与目录结构](#15-工程落地与目录结构)
16. [里程碑排期（7 周）](#16-里程碑排期)
17. [演进路径](#17-演进路径)
18. [风险清单](#18-风险清单)
19. [UI 设计规范与页面详细规格](#19-ui-设计规范与页面详细规格)
20. [用户画像与用户案例](#20-用户画像与用户案例)

---

## 1. 竞品与技术选型反推

### 1.1 PolyBuzz —— 交互层的可抄之处

| 观察 | 反推设计 | 落地物 |
|---|---|---|
| "Ship Them" 拆成 Character A / Character B / Integration 三个输入框 | prompt 是**结构化对象**，不是一个字符串 | `PromptSpec` JSON |
| Style / Pose 是带封面图的横向卡片网格 | 预设是**数据表**，每个预设 = 封面 + prompt 片段 + negative 片段 | `presets` 表 |
| Visual Components 再分 Composition / Lighting 两个 tab | 预设分层可叠加 | 预设组合器（按 priority 拼接与裁剪） |
| 生成产物带 tag、作者、对话数，可被检索复用 | 产物是**资产**不是一次性输出 | `assets` 表 + 溯源字段 |
| 会员分层：模型切换、每日 30 次头像生成 | 配额是产品一等公民 | `credit_accounts` + `credit_ledger` |
| 未登录可浏览，发消息才弹登录 | 先给价值再要注册 | 匿名试用 1 次 |

### 1.2 即梦 / Seedance —— 产品形态的可抄之处

即梦不再是技术参考（我们用 MiniMax），但产品形态仍然值得抄：

- **素材引用 DSL**：`@图片1` `@视频1` 标签化后注入 prompt，素材有「功能角色」分类（角色锚定 / 场景定调 / 运镜参考 / 节奏氛围）→ 我们的 `refs[].role`
- **提交前实时积分预估**（"✦ 48 ~~72~~" 会员折扣）→ `POST /jobs/estimate`
- **结果页即新任务入口**（超清 / 延长 / 编辑 / 补帧 / 对口型）→ 产物是下一个 Job 的输入
- **参数条由模型能力驱动**（不同模型支持的比例/时长不同）→ Capability Matrix 驱动前端禁用态

### 1.3 Aether —— 编排层的直接采纳

vese.ai 的 Aether 是一个**图式任务调度引擎**，核心是一套声明式工作流协议（`apiVersion: aether/v1`）+ 一个 Go 参考实现。仓库：`github.com/BabySid/aether`，BSD-3-Clause。

**它解决的问题正好是你的问题。** 文章开篇描述的困境几乎是你需求的逐字复述：

> 一开始只是「prompt 调模型出图」。然后产品说 prompt 太糙要先 LLM 润色、润色完要过审、过审才出图。然后又要基于图出视频、叠字幕、合成音频、再过审。步骤从 3 个涨到 7 个，有的有依赖有的能并行，线性调用链开始长分支，代码里塞满 WaitGroup、channel 和状态判断。

这就是你的「单图 → 四格 → 连续图 → 单段视频 → 连续视频」的演化路径。

**协议的三个原语正好覆盖你的六种形态：**

| Aether 原语 | 对应你的需求 |
|---|---|
| **DAG**（`dependencies` 声明依赖，引擎自动最大化并行） | 四格漫画的 4 格并行 + 合成节点 |
| **Task**（叶子节点，绑定可插拔 Executor） | 一次 MiniMax 调用 / 一次 ffmpeg 处理 |
| **Loop**（`items` 静态列表 / `itemsFrom` 上游动态列表 / `repeatCondition` 条件循环） | 连续生成 N 段；「重试直到质量达标」 |
| **递归组合**（DAG 里的步骤可引用 Loop，Loop 每轮可引用内层 DAG，任意深度嵌套，引擎用同一套调度逻辑处理所有层级） | 连续影片 = Loop(每个分镜) → DAG(单段视频生产线) |

文章里给的批量视频生成示例，结构和你的「连续影片」几乎一模一样：

```
DAG(main-pipeline)
├── Task(fetch-tasks)
├── Loop(batch-process)
│   └── DAG(single-video)
│       ├── Task(polish-prompt)
│       ├── Task(gen-image)
│       ├── Task(gen-video)
│       └── Task(add-subtitle)
└── Task(notify)
```

**四个对你特别关键的设计：**

**① Suspend / Resume 是协议级原语。**
执行器返回 `ExecCode 1 (Suspended)`，引擎保存中间输出、把任务标记为挂起、**不推进下游**；外部系统带新数据调用 `Resume`，引擎合并输入后重新派发。可以反复挂起-恢复多次。

这一条 v0.1 完全没有，但它是你产品的刚需：四格生成完让用户确认再去出视频；审核不通过挂起等人工；**尤其是下面 §3.5 的「768P 预览 → 用户挑选 → 2K 定稿」核心循环，没有它就得在业务层自己写状态机。**

**② ExecCode 与 Phase 分离，且 phaseConditions 可覆盖默认映射。**
执行器只返回一个数字，引擎负责把数字映射成状态：

```
ExecCode 0 → Succeeded    执行成功
ExecCode 1 → Suspended    挂起等待输入
ExecCode 2 → Failed       业务失败（如审核不通过）
ExecCode 3 → Error        系统错误（如 GPU OOM）
ExecCode 4 → Timeout      超时
```

完整状态机 `Created → Ready → Running → Succeeded/Failed/Error/Timeout/Skipped/Cancelled` 也在协议里，其中 `Skipped`（条件不满足）和 `Cancelled`（用户取消）只能由引擎写入，执行器无权设置。

`phaseConditions` 允许在工作流定义里覆盖默认规则：

```json
{
  "phaseConditions": {
    "succeeded": "code == 0 && outputs.parameters.score > 0.8",
    "failed":    "code == 0 && outputs.parameters.score <= 0.8"
  }
}
```

**这一条在 MiniMax 上有立刻可用的场景**：图片接口 `n=4` 时，被内容安全拦截的图会被静默丢弃，响应里 `metadata.failed_count > 0` 而 HTTP 仍是 200。用 phaseConditions 可以直接把「只回了 3 张」判成失败，不用在执行器里写业务判断：

```json
"phaseConditions": {
  "succeeded": "code == 0 && outputs.parameters.success_count == outputs.parameters.requested_n",
  "failed":    "code == 0 && outputs.parameters.success_count < outputs.parameters.requested_n"
}
```

**③ 引擎只调度，绝不执行。** 引擎职责严格限定三件事：判定哪些任务就绪、通过 TaskBroker 派发给 Worker、响应完成事件推进工作流状态。所有实际执行交给可插拔执行器，引擎不知道也不关心任务跑在本地进程、Docker、K8s Pod 还是远程 API。

这让引擎**拓扑无关**：同一份代码可以嵌在单体里做内存调度，也可以驱动跨机房的分布式任务集群。对你的意义：POC 单机跑，将来拆 GPU 机器池，引擎代码一行不改。

**④ Fat TaskAssignment —— Worker 自给自足。**
引擎派发时不是给一个任务 ID 让 Worker 回查，而是把执行所需的一切打包进单个 payload：

```go
type TaskAssignment struct {
    TaskRunID     string
    WorkflowRunID string
    TaskName      string
    ExecutorType  string           // "minimax.image" / "minimax.video" / "local.ffmpeg"
    Inputs        *model.Inputs    // 已完全解析的输入参数
    Timeout       string           // "30m"
    Resources     *model.Resources // CPU / 内存 / GPU 需求
    Priority      int
    RetryCount    int
}
```

Worker 收到后完全自给自足：不需要知道引擎地址、不需要持有数据库连接、甚至不需要在同一个网络。这消灭了一整类分布式耦合问题。

**执行器接口只有三个方法**（这是你实现 MiniMax 适配的全部工作量）：

```go
type Plugin interface {
    Type() string                    // "minimax.image"
    Schema() model.ExecutorSchema    // 声明输入输出契约，Submit 时引擎据此校验
    Execute(ctx context.Context, req *ExecuteRequest) (*model.ExecOutputs, error)
}
```

**六边形架构，全部依赖以函数式选项注入：**

```go
eng, _ := aether.New(
    aether.WithStore(...),            // 状态存储        → 你实现 MySQL 版
    aether.WithIDGenerator(...),      // ID 生成          → ULID
    aether.WithExprEvaluator(...),    // 表达式引擎       → expr-lang 或 cel-go
    aether.WithTaskBroker(...),       // 任务派发桥       → 你实现 asynq/Redis 版
    aether.WithExecutorRegistry(...), // 执行器注册表
    aether.WithTimeoutWatcher(...),   // 超时监控
    aether.WithVarsSource(...),       // 变量注入
)
```

可选扩展点（不配也不影响核心调度）：`hook.Notifier`（生命周期回调）、`secret.Provider`（密钥）、`artifact.Repo`（产物存储）、`errsink.ErrorSink`（错误观测）、`cron.Scheduler`（定时触发）。

**Playground**：仓库带 `cmd/playground`，内置全部接口的内存实现，零依赖本地跑完整工作流，还能出 HTML 报告。**这是你第 1 周验证可行性的工具。**

---

## 2. Aether 采纳决策与风险控制

### 2.1 必须先说清楚的事实

我抓了仓库，实际状态是：

| 项 | 实际值 |
|---|---|
| Stars / Forks / Watchers | **3 / 0 / 0** |
| Commits | 55 |
| Releases / Tags | **无** |
| README | **2 行，249 字节**（只有一句项目描述） |
| 文档 | 只有那篇博客文章 |
| License | BSD-3-Clause（宽松，可自由 fork / 商用 / 修改） |
| 包结构 | `artifact` `broker` `cron` `errsink` `executor` `expr` `hook` `idgen` `internal` `model` `secret` `specs` `store` `timeout` `vars` `worker` + `engine*.go` `option.go` `types.go` |

把一个「将来要长成完整系统」的产品的编排核心，压在一个 3 star、无 release、无文档的个人项目上，**这是整个方案里最大的单点风险**，必须明确讲出来而不是含糊过去。

### 2.2 但也要说清楚另一面

反过来看，几个因素显著降低了这个风险：

1. **协议才是真资产，引擎只是参考实现。** 文章自己就是这么定位的：协议稳定、引擎可替换。即使有一天你扔掉这个引擎，`aether/v1` 的工作流 JSON 定义是可移植的——同样的声明式结构可以驱动 Argo Workflows 或 Temporal。**前提是你的工作流定义里不塞引擎特有的 hack。**
2. **代码量小到可以整个读完。** 一个 55 commit 的 Go 库，一下午能读完全部源码。这和 Temporal 那种你根本读不动的系统相比，出问题时的排障能力是完全不同的。对你这种「不熟 Go」的情况，读一个小而完整的调度器源码，其实是很好的学习材料。
3. **BSD-3-Clause 允许你 fork。** 最坏情况下作者弃坑，你已经 vendor 了一份代码，继续自己维护。这不是理论选项——55 commit 的代码你养得起。
4. **架构上它已经把接口边界画好了。** 即使你完全自研，你也会需要设计 Store / Broker / Executor / Expr 这几个边界。抄一个已经画好的边界，比自己从零想省很多。

### 2.3 决策：采纳，但设三道闸门

**闸门一：第 1 周的 PoC 验证（go / no-go）**

W1 用 Playground 跑三个工作流，**跑通才继续，跑不通立刻退回 v0.1 的自研编排器**：

| 验证项 | 通过标准 |
|---|---|
| DAG 并行 + 汇聚 | 4 个并行 Task → 1 个 compose Task，能正确等齐 |
| Loop 串行 + 上游产物传递 | 3 轮循环，第 N 轮能读到第 N-1 轮的 outputs |
| Suspend / Resume | 任务挂起后引擎不推进下游；Resume 带新参数后能继续 |
| phaseConditions | 执行器返回 code=0 但 outputs 不满足条件时，能判成 failed |
| 长超时 | Task 配 30m 超时不被误杀（视频任务真的要跑几分钟） |

这五项是你六种形态的全部依赖。任何一项跑不通，当场知道，而不是第 4 周才发现。

**闸门二：vendor + 版本锁定**

不要 `go get` 追 main 分支。做法二选一：
- `git submodule` 锁到具体 commit，或
- 直接 `cp -r` 进 `third_party/aether/`，在 `go.mod` 里 `replace github.com/BabySid/aether => ./third_party/aether`

理由：无 release 无 tag 的项目，main 分支随时可能 break 你。锁死之后升级是你主动选择的动作，不是某天 CI 突然红了。

**闸门三：包一层你自己的接口**

业务代码**绝不直接 import `aether`**，只认你自己的接口：

```go
// internal/domain/workflow/engine.go —— 业务代码只认这个
type Engine interface {
    Submit(ctx context.Context, def *Definition, args map[string]any) (RunID, error)
    Get(ctx context.Context, id RunID) (*Run, error)
    Cancel(ctx context.Context, id RunID) error
    Resume(ctx context.Context, id RunID, taskName string, inputs map[string]any) error
}

// internal/infra/workflow/aether/engine.go —— 唯一 import aether 的文件
type aetherEngine struct{ eng *aether.Engine }
```

这和 v0.1 对 `TaskQueue` 的处理是同一个手法。加起来大概 200 行胶水代码，换来的是「换编排引擎只改一个文件」。

### 2.4 明确的 Plan B

如果闸门一没过：回退到 v0.1 §7.8 的自研 Orchestrator（DB 状态机 + `depends_on` JSON + CAS 推进），大约 400 行。工作流定义仍然用 aether/v1 的 JSON 结构书写——**协议照抄，引擎自写**。这样将来 Aether 成熟了还能换回去。

---

## 3. MiniMax 能力与约束基线

**这一节是硬约束清单，所有设计都要满足它。**

### 3.1 图片生成：`POST /v1/image_generation`

⚠️ **同步接口**。直接返回图片 URL，没有 task_id，没有轮询。

| 项 | 值 |
|---|---|
| 模型 | `image-01`、`image-01-live` |
| prompt | ≤ **1500 字符** |
| `n` | **1 ~ 9**，单次调用出多张 |
| `aspect_ratio` | `1:1`(1024×1024) `16:9`(1280×720) `4:3`(1152×864) `3:2`(1248×832) `2:3`(832×1248) `3:4`(864×1152) `9:16`(720×1280) `21:9`(1344×576，仅 image-01) |
| `width`/`height` | 仅 image-01；须同时设置；[512, 2048]；必须是 8 的倍数；与 aspect_ratio 同时给时 aspect_ratio 优先 |
| `seed` | 支持。相同 seed + 相同参数可复现相近结果 → **角色一致性的主要抓手** |
| `style` | 仅 `image-01-live` 生效。`style_type` 可选 `漫画`/`元气`/`中世纪`/`水彩`，`style_weight` (0,1] 默认 0.8 |
| `prompt_optimizer` | 布尔，默认 false。**自带 prompt 优化**，省掉一个 LLM 润色节点 |
| `response_format` | `url`（默认，**有效期 24 小时**）或 `base64` |
| `aigc_watermark` | 布尔，默认 false |

**响应的坑（必须处理）：**

```jsonc
{
  "id": "03ff3cd0...",
  "data": { "image_urls": ["...", "...", "..."] },
  "metadata": { "success_count": "3", "failed_count": "0" },  // ← 被内容安全拦截的图会被丢弃
  "base_resp": { "status_code": 0, "status_msg": "success" }
}
```

- **错误不走 HTTP 状态码**，HTTP 恒 200，错误在 `base_resp.status_code`：
  `0` 成功 / `1002` 限流 / `1004` 鉴权失败 / `1008` 余额不足 / `1026` 描述涉敏 / `2013` 参数异常 / `2049` 无效 api key
- **`n=4` 可能只回 3 张**（`failed_count=1`），这是部分成功，必须显式处理

### 3.2 视频生成：`POST /v2/video_generation`

⚠️ **异步接口**。提交拿 `task_id`，轮询 `GET /v2/query/video_generation/{task_id}`，**官方建议轮询间隔 10 秒**。

| 项 | 值 |
|---|---|
| 模型 | `MiniMax-H3` |
| 分辨率 | `768P` / `2K` |
| 时长 | **4 ~ 15 秒整数** |
| prompt | ≤ **7000 字符**，且**每次请求必须含一个非空 text 项** |
| `ratio` | `adaptive` `21:9` `16:9` `4:3` `1:1` `3:4` `9:16` |
| `callback_url` | **支持回调**（见下） |
| `aigc_watermark` | 布尔 |
| 请求体总大小 | ≤ **64 MB**，大文件用公网 URL，别用 base64 |

**三种模式与 `role` 取值：**

```
文生视频 t2va   : 仅 text                  → ratio 必填且不能为 adaptive
图生视频 i2va   : text + first_frame / last_frame 图片
                                            → ratio 恒为 adaptive（传别的会被忽略）
多模态参考 r2va : text + reference_image / reference_video / reference_audio
                                            → ratio 可选，默认 adaptive
```

🔴 **最重要的一条硬约束——两种模式互斥：**

> content 中出现 `reference_image` / `reference_video` / `reference_audio` 任一 role，就**不能**再出现 `first_frame` / `last_frame`，反之亦然。

**这条直接决定了「连续影片」的设计**，见 §5.4。

**输入素材限制：**

| 类型 | 限制 |
|---|---|
| 图片 | JPG/JPEG/PNG/WEBP/HEIC/HEIF；单个 ≤30MB；宽高 [256, 5760]；宽高比 [0.4, 2.5]；首帧 ≤1、尾帧 ≤1、参考图 **≤9** |
| 视频 | MP4/MOV；H.264 或 H.265；音轨 AAC/MP3；单个 ≤50MB；**≤3 段**；单段 [2,15]s；**总时长 ≤15s**；帧率 [23.976, 60] |
| 音频 | WAV/MP3；单个 ≤15MB；**≤3 段**；单段 [2,15]s；总 ≤15s |
| 混合 | **总上限 12 个文件** |

**素材可以用 `mm_file://{file_id}` 引用平台已上传文件** → 同一个角色参考图在 N 段视频里复用时，**只上传一次**，后续传 file_id。省带宽、避开 64MB 限制、降延迟。

**任务状态**：`queued` / `running` / `succeeded` / `failed` / `cancelled`

**查询响应含 `usage` 字段**（`total_seconds` / `input_seconds` / `output_seconds` / `input_image_count`）→ **可以按实际用量精确结算，不用估算**。

**其他可用接口**：
- `GET` 查询任务列表（最近 7 天，按状态/模型/类型过滤）→ **对账兜底**
- 取消排队中的任务 / 删除任务记录 → **取消功能可以真的取消到上游**
- prompt 里可用 `[运镜]` 指令引导镜头调度

### 3.3 回调机制（替代纯轮询）

配置 `callback_url` 后：
1. MiniMax 先发一个含 `challenge` 字段的验证请求，**你必须在 3 秒内原样返回 challenge**
2. 验证通过后，每次任务状态变更就 POST 推送，推送体结构与查询接口响应一致

**设计**：回调为主 + 轮询兜底。回调会丢（网络、部署重启、验证过期），所以 `next_poll_at` 兜底扫描必须保留，只是间隔可以放宽到 60 秒。

### 3.4 H3-Context-IR：厂商提供的 Prompt 编译器

异步任务，深度理解文本/图像/音频/视频多模态上下文及其关系，做逻辑推理后生成结构化的增强提示词，**只返回 prompt 不生成视频**。用 `task_type=h3_context_ir` 识别，结果在 `content.prompt`。

按 token 计费：输入 ¥5.80/百万 tokens，输出 ¥23.00/百万 tokens。

**用法**：作为 DAG 里一个可选的 `prompt_enhance` 节点，接在生成节点之前。对应文章开头那个「让 LLM 先润色 prompt」的诉求——**厂商已经帮你做了，不用自己接 LLM**。图片侧的等价物是 `prompt_optimizer: true`（免费）。

### 3.5 视频再生成：768P → 2K（⭐ 核心成本策略）

已有符合 H3 768P 规格的成片，可以调「视频再生成」接口输出 2K。**请求时需原样提交生成 768P 时用的全部 content，并额外加入且仅加入一个 `type=video_url`、`role=base_video` 的源视频项。** 用 `task_type=regeneration` 识别，与其他 H3 任务共用查询/列表/取消接口。

**算一下账：**

```
直接出 2K，5 秒：           5 × 0.80 = ¥4.00
先 768P 再升 2K，5 秒：     5 × 0.50 + 5 × 0.30 = 2.50 + 1.50 = ¥4.00
```

**完全相等。** 这是厂商刻意设计的定价（0.50 + 0.30 = 0.80）。

于是三段 5 秒的连续视频：

| 策略 | 成本 |
|---|---|
| 全部直接 2K | 3 × ¥4.00 = **¥12.00** |
| 全 768P，三段全升 2K | ¥7.50 + ¥4.50 = **¥12.00**（持平） |
| 全 768P，只升满意的 1 段 | ¥7.50 + ¥1.50 = **¥9.00**（省 25%） |
| 全 768P，一段都不满意重做 | ¥7.50 + 重做成本（比重做 2K 便宜 37.5%） |

⚠️ 注意：再生成会**重新对输入素材计费**（图片超 5 张的部分 ¥0.15/张，输入视频 ¥0.30/秒）。素材少时成本中性成立，素材多时略有溢价。

**结论：默认策略永远是「先 768P，后定稿」。成本上限等于直接出 2K，下限低得多，且用户体验更好（先看再决定）。**

### 3.6 这一条 + Aether 的 Suspend/Resume = 产品核心循环

```
Workflow: video.sequence
│
├─ Loop(每个分镜)
│   └─ Task(gen_video_768p)      ← 便宜，快，用来看效果
│
├─ Task(preview_gate)             ← 返回 ExecCode 1 (Suspended)
│      引擎保存产物、挂起、不推进下游
│      前端展示 N 段预览，用户勾选要定稿的段 + 标记要重做的段
│      用户提交 → Resume(选中的分镜索引)
│
├─ Loop(选中的分镜)
│   └─ Task(regenerate_2k)        ← 只对选中的段付 2K 的钱
│
└─ Task(concat_video)             ← ffmpeg 拼接成片
```

**没有 suspend/resume，这个流程就要在业务层自己写一套「等待用户输入」的状态机——把本该属于工作流语义的东西泄漏到应用代码里。** 这正是文章批评的传统做法。

---

## 4. 产品定义与范围

### 4.1 一句话定义

> 用户用**结构化 prompt + 参考素材**，通过**声明式工作流**编排 MiniMax 的图片与视频能力，从单张图生产到连续多段影片，产物沉淀为**可复用资产**，并以「预览-定稿」两阶段控制成本。

### 4.2 六种生成形态

| # | 形态 | Workflow 名 | 拓扑 | MiniMax 调用 |
|---|---|---|---|---|
| 1 | 单图 | `image.single` | 1 Task | 1 次 `n=1` |
| 2 | 批量出图 | `image.batch` | **1 Task**（不是 N 个！） | **1 次 `n=2..9`** |
| 3 | 四格漫画 | `image.comic4` | DAG: 4 并行 + 1 合成 | 4 次 `n=1`（各自 prompt 不同） |
| 4 | 连续生成（图） | `image.sequence` | Loop 串行 | N 次，靠同 seed + 参考图保一致 |
| 5 | 单段影片 | `video.single` | 1 Task（+ 可选预览门） | 1 次异步任务 |
| 6 | 连续影片 | `video.sequence` | Loop(768P) → 预览门 → Loop(2K) → concat | N×2 次异步任务 |

**形态 2 的变更是 v0.1 的实质性修正**：MiniMax 图片接口单次就能出 9 张，拆成 N 个并行 Task 纯属自找麻烦（多 N 倍 HTTP 开销、N 倍限流压力、seed 管理更复杂）。**同 prompt 多变体 = 1 Task；不同 prompt = N Task。** 这是划分原则。

### 4.3 POC 不做（但留口）

多租户 / 支付订阅（积分做，充值走 CLI）/ 社区分发 / 音频生成与对口型 / 可视化工作流编辑器 / 移动端 / 分库分表

### 4.4 POC 成功标准

1. 六种形态端到端跑通，产物在资产库可见可下载
2. 杀掉 Worker 重启，进行中的任务自动恢复，不丢不重
3. `video.sequence` 能走完「768P 预览 → 挂起 → 用户勾选 → 2K 定稿 → 拼接」全流程
4. 把 MiniMax 调成必失败，积分正确退回，`SUM(ledger) == balance` 对账通过
5. 提交后 SSE 实时可见每个节点状态
6. 新增一个执行器（比如 mock）只需新增一个文件 + 一行注册

---

## 5. 核心领域模型

### 5.1 三层结构

```
Job（业务层：用户点一次"生成"）
 └── WorkflowRun（Aether 层：一次工作流执行）
      └── TaskRun（Aether 层：DAG/Loop 中的一个节点）
           └── Asset（业务层：产物文件）
```

`Job` 和 `WorkflowRun` 是 1:1，但**必须分开**：`Job` 承载业务语义（谁的、哪个项目、扣了多少积分、给前端看的标题），`WorkflowRun` 承载编排语义（节点状态、依赖、循环轮次）。前者是你的表，后者是 Aether 的 Store。

### 5.2 PromptSpec：平台中立的结构化 prompt

```jsonc
{
  "version": 1,
  "text": "两人在天台对峙，黄昏逆光，风很大",
  "characters": [
    { "slot": "A", "character_id": "chr_01", "weight": 1.0 },
    { "slot": "B", "character_id": "chr_02" }
  ],
  "refs": [
    { "tag": "图片1", "asset_id": "ast_x", "role": "character_anchor" },
    { "tag": "图片2", "asset_id": "ast_y", "role": "scene_tone" },
    { "tag": "视频1", "asset_id": "ast_z", "role": "camera_motion" }
  ],
  "presets": {
    "style": "cyberpunk", "pose": "back_to_back",
    "lighting": "rim_light", "camera": "low_angle"
  },
  "params": {
    "model": "MiniMax-H3",
    "aspect_ratio": "3:4", "resolution": "768P",
    "n": 4, "seed": 12345, "duration_s": 8,
    "prompt_optimizer": true, "aigc_watermark": false
  },
  "negative": "低画质, 多手指, 水印",
  "panels": [ { "index": 1, "text": "..." } ],
  "sequence": {
    "shots": [ { "text": "近景，A 转身", "duration_s": 5 } ],
    "continuity": "last_frame",
    "preview_first": true
  }
}
```

`refs[].role` 枚举（业务语义，由编译器映射到 MiniMax 的 role）：
`character_anchor` / `scene_tone` / `camera_motion` / `rhythm` / `first_frame` / `last_frame` / `style_ref`

### 5.3 PromptCompiler：PromptSpec → MiniMax 请求

```
PromptSpec  ──[PromptCompiler]──→  MiniMax 请求体
```

编译步骤：

1. **展开 characters** → 取角色的参考图 asset + 描述文本；图片场景注入 `seed`，视频场景注入 `reference_image`
2. **展开 presets** → 从 `presets` 表取 `prompt_fragment`，按 `priority` 拼接
3. **展开 refs** → 映射到 MiniMax `role`：
   `character_anchor`→`reference_image`，`camera_motion`→`reference_video`，
   `first_frame`→`first_frame`，`style_ref`→`reference_image`
4. **模式互斥检查**（🔴 关键）：若同时存在 `first_frame`/`last_frame` 和任一 `reference_*`，按策略降级并**在响应里告知用户**
5. **长度裁剪**：图片 1500 字符 / 视频 7000 字符，按 `priority` 从低到高丢弃片段
6. **能力校验**：查 Capability Matrix，比例/时长/分辨率不合法直接报错，不发请求
7. **素材去重**：同一 asset 已上传过 MiniMax 的，复用 `mm_file://{file_id}`

### 5.4 连续影片：两种衔接策略的取舍（受互斥约束支配）

因为 `first_frame` 和 `reference_image` **不能共存**，连续影片有两条互斥路径：

| 策略 | 做法 | 优点 | 缺点 |
|---|---|---|---|
| **A. 尾帧续接**（i2va） | 上一段 ffmpeg 抽尾帧 → 作为下一段 `first_frame` | 画面**无缝衔接** | 不能同时挂角色参考图 |
| **B. 参考续接**（r2va） | 每段都用 `reference_image` = 角色图（+ 上段尾帧） | 角色**强一致** | 段与段之间**有明显跳切** |

**推荐默认策略（混合）：**

```
镜头 1  : r2va  reference_image = 角色参考图      ← 建立角色形象
镜头 2..N: i2va  first_frame  = 上一段的尾帧      ← 无缝衔接
```

**关键洞察：从镜头 2 开始不需要再挂角色参考图，因为上一段的尾帧本身就包含了这个角色。** 角色一致性通过帧链条传递，而不是通过 reference。这样两个目标同时达成，绕开了互斥约束。

风险：帧链条会逐段漂移（每段都基于上段的最后一帧，误差累积）。缓解：镜头数 >4 时，每 3 段插入一次 r2va 用原始角色图「校准」，接受该处的一次跳切。这个策略参数化成 `sequence.recalibrate_every`。

### 5.5 Workflow 定义示例（aether/v1）

> ⚠️ **以下 JSON 依据博客文章披露的协议片段编写。`task` 模板的执行器绑定方式、`retry` / `timeout` / `resources` 的确切字段名需要对照仓库 `specs/` 目录核实后修正。DAG / Loop / when / dependencies / valueFrom / phaseConditions 这几处是文章明确给出的，可信度高。**

**四格漫画：**

```json
{
  "apiVersion": "aether/v1",
  "kind": "Workflow",
  "metadata": { "name": "image-comic4" },
  "spec": {
    "entrypoint": "main",
    "arguments": {
      "parameters": [
        { "name": "panels",     "type": "string" },
        { "name": "seed",       "type": "string" },
        { "name": "style_frag", "type": "string" },
        { "name": "aspect_ratio", "type": "string", "default": "\"1:1\"" }
      ]
    },
    "templates": [
      {
        "dag": {
          "name": "main",
          "tasks": [
            { "name": "moderate", "template": "text-moderation" },
            { "name": "panels",   "template": "panel-loop",
              "dependencies": ["moderate"],
              "when": "tasks.moderate.outputs.parameters.passed == \"true\"" },
            { "name": "compose",  "template": "compose-grid",
              "dependencies": ["panels"] }
          ]
        }
      },
      {
        "loop": {
          "name": "panel-loop",
          "itemsFrom": "workflow.parameters.panels",
          "template": "gen-one-panel"
        }
      },
      {
        "task": {
          "name": "gen-one-panel",
          "executor": "minimax.image",
          "arguments": {
            "parameters": [
              { "name": "prompt", "valueFrom": { "parameter": "item.text" } },
              { "name": "seed",   "valueFrom": { "parameter": "workflow.parameters.seed" } },
              { "name": "n",      "value": "1" }
            ]
          },
          "phaseConditions": {
            "succeeded": "code == 0 && outputs.parameters.success_count == \"1\"",
            "failed":    "code == 0 && outputs.parameters.success_count == \"0\""
          },
          "retry":   { "limit": 3, "backoff": "exponential" },
          "timeout": "3m"
        }
      },
      {
        "task": {
          "name": "compose-grid",
          "executor": "local.compose",
          "arguments": {
            "parameters": [
              { "name": "sources", "valueFrom": { "parameter": "tasks.panels.outputs.parameters.asset_ids" } },
              { "name": "layout",  "value": "\"2x2\"" }
            ]
          },
          "timeout": "1m"
        }
      }
    ]
  }
}
```

**连续影片（含预览门）：**

```json
{
  "apiVersion": "aether/v1",
  "kind": "Workflow",
  "metadata": { "name": "video-sequence" },
  "spec": {
    "entrypoint": "main",
    "arguments": {
      "parameters": [
        { "name": "shots", "type": "string" },
        { "name": "character_ref_file_id", "type": "string" },
        { "name": "preview_first", "type": "string", "default": "\"true\"" }
      ]
    },
    "templates": [
      {
        "dag": {
          "name": "main",
          "tasks": [
            { "name": "draft",   "template": "shot-loop-768p" },

            { "name": "gate",    "template": "preview-gate",
              "dependencies": ["draft"],
              "when": "workflow.parameters.preview_first == \"true\"" },

            { "name": "upgrade", "template": "regen-loop-2k",
              "dependencies": ["gate"] },

            { "name": "concat",  "template": "concat-video",
              "dependencies": ["upgrade"] }
          ]
        }
      },
      {
        "loop": {
          "name": "shot-loop-768p",
          "itemsFrom": "workflow.parameters.shots",
          "template": "gen-one-shot"
        }
      },
      {
        "task": {
          "name": "gen-one-shot",
          "executor": "minimax.video",
          "arguments": {
            "parameters": [
              { "name": "prompt",     "valueFrom": { "parameter": "item.text" } },
              { "name": "duration",   "valueFrom": { "parameter": "item.duration_s" } },
              { "name": "resolution", "value": "\"768P\"" },
              { "name": "first_frame_asset_id",
                "valueFrom": { "parameter": "item.prev_last_frame" } }
            ]
          },
          "retry":   { "limit": 2, "backoff": "exponential" },
          "timeout": "30m"
        }
      },
      {
        "task": {
          "name": "preview-gate",
          "executor": "human.gate",
          "comment": "执行器直接返回 ExecCode 1 (Suspended)，等前端 Resume 传入 selected_indices"
        }
      },
      {
        "loop": {
          "name": "regen-loop-2k",
          "itemsFrom": "tasks.gate.outputs.parameters.selected_shots",
          "template": "regen-one-shot"
        }
      },
      {
        "task": {
          "name": "regen-one-shot",
          "executor": "minimax.video.regen",
          "timeout": "30m"
        }
      },
      {
        "task": { "name": "concat-video", "executor": "local.ffmpeg.concat", "timeout": "10m" }
      }
    ]
  }
}
```

**注意 `gate` 节点用了 `when`**：`preview_first=false` 时该节点 Skipped，但 `upgrade` 依赖它——需要确认 Aether 对「依赖了 Skipped 节点」的语义（是跳过下游还是继续）。**这是闸门一必须验证的第 6 项**。若语义不符，改用两个独立 workflow 定义（带预览门 / 不带）更稳妥。

---

## 6. 职责分工

**这张表是 v0.2 最重要的一张表。** 引入 Aether 不等于什么都不用做——它管编排，剩下的全是你的。

| 能力 | Aether 提供 | 你必须自己建 |
|---|---|---|
| DAG 依赖解析与并行 | ✅ | |
| 循环 / 嵌套 / 递归组合 | ✅ | |
| 条件分支 `when` | ✅ | |
| 挂起 / 恢复 | ✅ | |
| 重试 / 超时策略 | ✅ | |
| 任务派发（Broker 抽象） | 接口 | **asynq/Redis 实现** |
| 状态持久化（Store 抽象） | 接口 | **MySQL 实现** |
| 表达式求值（Expr 抽象） | 接口 | **expr-lang 或 cel-go 接入** |
| 定时触发（Cron） | ✅ | |
| 生命周期回调（Hook） | 接口 | **投影到业务表 + SSE 推送** |
| 密钥管理（Secret） | 接口 | **MiniMax API Key 加密存取** |
| 产物存储（Artifact） | 接口 | **MinIO/OSS 实现** |
| **供应商并发/限流控制** | ❌ | **Redis 令牌桶 + Worker 池规格** |
| **积分预扣/结算/退款** | ❌ | **credit_ledger 全套** |
| **产物转存（URL 24h 过期）** | ❌ | **执行器内 materialize** |
| **内容审核** | ❌ | **DAG 节点 + moderation_records** |
| **用户/项目/资产/角色/预设** | ❌ | **完整业务层** |
| **PromptSpec 编译** | ❌ | **PromptCompiler** |
| **成本预估** | ❌ | **Capability Matrix + 定价函数** |
| **前端** | ❌ | **全部** |

粗略估算：Aether 帮你省掉的是编排引擎那 2000~3000 行，**占整个系统不到 20%**。但省掉的是**最容易写错、最难测试、最难重构**的那 20%。

---

## 7. 功能需求清单

> P0 = POC 必须；P1 = 应该；P2 = 留口不做

### F1 账号与积分

| ID | 需求 | 优先级 | 验收 |
|---|---|---|---|
| F1.1 | 邮箱注册/登录，JWT | P0 | token 7 天 + refresh |
| F1.2 | 匿名试用生成 1 次单图 | P1 | 设备指纹 + IP 限流 |
| F1.3 | 积分账户与账本 | P0 | `SUM(ledger.amount) == balance + held` 恒成立 |
| F1.4 | 提交前实时预估积分 | P0 | 参数变更前端实时刷新 |
| F1.5 | 按 MiniMax `usage` 字段精确结算 | P0 | 视频按 `output_seconds` 实扣 |
| F1.6 | CLI 手工发放积分 | P0 | |

### F2 资产库

| ID | 需求 | 优先级 | 验收 |
|---|---|---|---|
| F2.1 | 预签名直传上传参考素材 | P0 | 不经过 Go 服务 |
| F2.2 | 生成产物自动转存入库 | P0 | 产物 URL 是自己域名，非 MiniMax 临时链接 |
| F2.3 | 素材同步到 MiniMax 换 `file_id` 并缓存 | P0 | 同一素材第二次使用不重复上传 |
| F2.4 | 资产列表瀑布流 + 筛选 | P0 | cursor 分页 |
| F2.5 | 资产详情看参数 + 「以此再生成」 | P0 | PromptSpec 回填编辑器 |
| F2.6 | 视频自动抽首/尾帧存为 image asset | P0 | 连续影片的基础 |
| F2.7 | 软删 + 批量下载 | P1 | |

### F3 角色库

| ID | 需求 | 优先级 | 验收 |
|---|---|---|---|
| F3.1 | 创建角色：名称+描述+1~3 参考图+固定 seed | P0 | |
| F3.2 | 生成时绑定角色到槽位 A/B | P0 | |
| F3.3 | 角色在四格/连续中自动透传 | P0 | 4 格角色长相一致（主观验收） |
| F3.4 | 角色参考图预上传 MiniMax 拿 file_id 缓存 | P0 | |

### F4 预设库

| ID | 需求 | 优先级 |
|---|---|---|
| F4.1 | 分类 style/pose/composition/lighting/camera | P0 |
| F4.2 | 带封面图的横向卡片选择器 | P0 |
| F4.3 | 多预设叠加 + 按 priority 冲突裁剪 | P0 |
| F4.4 | 映射 image-01-live 的 `style_type`（漫画/元气/中世纪/水彩） | P0 |
| F4.5 | 用户自定义预设 | P2 |

### F5 图片生成

| ID | 需求 | 优先级 | 验收 |
|---|---|---|---|
| F5.1 | 单图 | P0 | |
| F5.2 | 批量 n=2/4/9（**单次调用**） | P0 | 部分被审核拦截时明确提示实出几张 |
| F5.3 | 四格漫画（4 格各自 prompt，共享角色/风格/seed） | P0 | 自动拼版 2x2 成品图 |
| F5.4 | 剧情自动拆 4 格 | P1 | 走 MiniMax 文本模型 |
| F5.5 | 连续生成 N 张（同 seed + 参考图链） | P0 | |
| F5.6 | 单节点重生成（不重跑整个 Job） | P0 | |
| F5.7 | `prompt_optimizer` 开关暴露给用户 | P0 | 默认开 |
| F5.8 | 图生图 | P1 | |

### F6 视频生成

| ID | 需求 | 优先级 | 验收 |
|---|---|---|---|
| F6.1 | 文生视频 | P0 | ratio 必填校验 |
| F6.2 | 图生视频（首帧） | P0 | 前端提示 ratio 由图片决定 |
| F6.3 | 首尾帧 | P1 | |
| F6.4 | 多模态参考（reference_image/video/audio） | P1 | 前端做数量+互斥校验 |
| F6.5 | **模式互斥的前端拦截** | P0 | 选了参考素材就禁用首尾帧，反之亦然，并说明原因 |
| F6.6 | 连续影片：N 分镜逐段生成 | P0 | 每段完成立即可预览 |
| F6.7 | 衔接策略：混合默认（首段 r2va，后续 i2va 尾帧续接） | P0 | |
| F6.8 | **768P 预览 → 勾选 → 2K 定稿** | P0 | 走 suspend/resume，成本对比可见 |
| F6.9 | ffmpeg 拼接成片 | P0 | 输出 mp4，分辨率/帧率统一 |
| F6.10 | H3-Context-IR 提示词增强（可选节点） | P1 | 用户可选开，显示额外费用 |

### F7 作业管理

| ID | 需求 | 优先级 | 验收 |
|---|---|---|---|
| F7.1 | 作业列表：状态/进度/耗时/积分 | P0 | |
| F7.2 | DAG 可视化（react-flow） | P0 | 含 Loop 展开与 Suspended 态 |
| F7.3 | SSE 实时进度 | P0 | 状态变更 2 秒内到前端 |
| F7.4 | 取消作业（含向 MiniMax 取消排队任务） | P0 | 未消耗积分退回 |
| F7.5 | 失败节点手动重试 | P0 | |
| F7.6 | 挂起态的「继续」交互 | P0 | 预览门的核心 UI |

### F8 内容安全

| ID | 需求 | 优先级 |
|---|---|---|
| F8.1 | prompt 本地敏感词前置过滤（命中不扣积分） | P0 |
| F8.2 | 处理 MiniMax 1026（涉敏）与 `failed_count` | P0 |
| F8.3 | 产物后置审核 | P1 |
| F8.4 | 审核记录留档 | P0 |

---

## 8. 技术架构

### 8.1 部署形态（POC：docker-compose，三个 Go 进程）

```
┌─ 前端 React SPA（Vite + Nginx） ─────────────────────────────┐
└──────────────────┬───────────────────────────────────────────┘
                   │ HTTPS / SSE
┌──────────────────▼───────────────────────────────────────────┐
│ cmd/api  ── Gin（无状态，可水平扩）                            │
│  · 鉴权 / 校验 / 幂等 / 限流                                   │
│  · 预估积分 → 预扣 → Engine.Submit(workflow, args)            │
│  · Resume 接口（预览门的"继续"）                               │
│  · SSE：订阅 Redis Pub/Sub 转发浏览器                          │
│  · MiniMax 回调接收端点（含 challenge 握手）                    │
└────┬─────────────────────────────────┬───────────────────────┘
     │                                 │
┌────▼──────────┐ ┌──────────────┐ ┌──▼──────────────────────┐
│  MySQL 8      │ │ MinIO / OSS  │ │ Redis 7                 │
│ ── 业务表 ──   │ │ · 用户素材    │ │ · asynq 队列(=Broker)   │
│ users/jobs    │ │ · 生成产物    │ │ · 供应商令牌桶           │
│ assets/credit │ │ · 抽帧/缩略图 │ │ · 幂等键                │
│ characters    │ │ · 拼接成片    │ │ · Pub/Sub → SSE         │
│ presets       │ └──────▲───────┘ └──▲──────────────────────┘
│ ── Aether ──  │        │            │
│ workflow_runs │        │ 转存        │ 派发
│ task_runs     │        │            │
└────▲──────────┘        │            │
     │ Store 实现        │            │
┌────┴────────────────────┴────────────┴──────────────────────┐
│ cmd/scheduler ── 内嵌 Aether Engine（单实例 + Leader 选举）   │
│  · 判定就绪 → 通过 TaskBroker 派发 Fat TaskAssignment        │
│  · 响应完成事件推进工作流                                     │
│  · hook.Notifier → 投影业务表 + 推 SSE + 触发积分结算         │
│  · 附加：轮询协调（next_poll_at）、超时回收、每日对账          │
└──────────────────────────┬──────────────────────────────────┘
                           │ TaskBroker (asynq / Redis)
┌──────────────────────────▼──────────────────────────────────┐
│ cmd/worker ── 执行器宿主（按队列分组独立部署与扩缩）           │
│  · minimax.image        同步调用 → materialize               │
│  · minimax.video        提交 → 回调/轮询 → materialize        │
│  · minimax.video.regen  768P→2K                              │
│  · minimax.context_ir   提示词增强                            │
│  · local.compose        拼版（imaging）                       │
│  · local.ffmpeg.*       抽帧 / 拼接 / 探测                    │
│  · human.gate           直接返回 Suspended                    │
│  · mock.*               本地假实现                            │
│  Worker 完全自给自足：不回调 Engine，不连业务库                │
└──────────────────────────┬──────────────────────────────────┘
                           ▼
              MiniMax Open Platform API
```

**注意 Engine 放在 `cmd/scheduler` 而不是 `cmd/api`**：引擎需要单实例（或带分布式协调）来避免重复推进。API 进程通过 Broker 或直接调用 Store 提交工作流，不持有引擎实例。

### 8.2 技术选型

| 层 | 选型 | 说明 |
|---|---|---|
| 语言 | Go 1.22+ | 你定的 |
| HTTP | Gin | 你定的 |
| ORM | GORM v2 | 你定的。⚠️ 状态 CAS、租约抢占用**原生 SQL** |
| 数据库 | MySQL 8.0 | 你定的。JSON 列存 spec |
| **编排** | **Aether（vendored）** | 见 §2 |
| **Broker** | **asynq → 实现 `broker.TaskBroker`** | 你的 MQ 需求在这里落地 |
| Store | 自实现 MySQL 版 `store.Store` | |
| Expr | `expr-lang/expr` 或 `google/cel-go` | 供 `when` / `phaseConditions` 求值 |
| 缓存/锁/信号量 | Redis 7 | |
| 对象存储 | MinIO → OSS/TOS | S3 兼容 |
| DI | google/wire | 编译期，无反射。**第 1 周先手写构造函数** |
| 日志 | zap，结构化 | 带 trace_id / job_id / task_run_id |
| 迁移 | goose | 禁用 GORM AutoMigrate |
| 观测 | Prometheus + Grafana | |
| 媒体处理 | ffmpeg（os/exec，带 ctx 超时） | |
| 前端 | React 18 + TS + Vite | 见 §14 |

**asynq 与 Aether 不是竞品，是上下层关系**：Aether 定义 `broker.TaskBroker` 接口负责「引擎 ↔ Worker」的桥接，asynq 是这个接口的一个实现。v0.1 里 asynq 是主角，v0.2 里它降级成一个实现细节——但仍然是正确的选择（延迟、退避重试、优先级、去重、asynqmon Web UI 全都用得上）。

---

## 9. 数据库设计

### 9.1 边界：哪些表是 Aether 的，哪些是你的

```
Aether Store（你实现，但 schema 由 store.Store 接口决定）
  aether_workflow_runs    工作流运行实例
  aether_task_runs        任务节点运行实例（含 scope 树、phase、inputs/outputs）
  aether_workflow_defs    工作流定义（可选，也可放业务表）

业务表（完全你自己设计）
  users / credit_accounts / credit_ledger
  projects / assets / characters / presets
  jobs                    ← 与 workflow_run 1:1
  job_nodes               ← 投影表，给列表页和 DAG 可视化用
  provider_accounts / provider_calls / provider_files
  moderation_records
```

⚠️ **`aether_*` 表结构必须对照仓库 `store/` 目录的接口定义来设计，不要凭猜。** 这是闸门一之后的第一件事。

**`job_nodes` 是投影表不是真相来源**：真相在 Aether Store，投影表存在的唯一理由是让列表页和 DAG 可视化不用穿透到引擎内部查询。通过 `hook.Notifier` 异步更新，允许短暂不一致。

### 9.2 核心业务表

```sql
CREATE TABLE jobs (
  id              BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  biz_id          CHAR(26)        NOT NULL,          -- ULID，对外暴露
  user_id         BIGINT UNSIGNED NOT NULL,
  project_id      BIGINT UNSIGNED NULL,
  workflow_name   VARCHAR(64)     NOT NULL,          -- image.comic4 / video.sequence
  workflow_run_id VARCHAR(64)     NOT NULL DEFAULT '', -- ⭐ Aether 的 RunID
  title           VARCHAR(128)    NOT NULL DEFAULT '',
  status          VARCHAR(16)     NOT NULL,          -- created/running/suspended/
                                                     -- succeeded/partial/failed/cancelled
  spec            JSON            NOT NULL,          -- PromptSpec 快照（不可变）

  node_total      INT NOT NULL DEFAULT 0,
  node_done       INT NOT NULL DEFAULT 0,
  node_failed     INT NOT NULL DEFAULT 0,

  credit_estimated INT NOT NULL DEFAULT 0,
  credit_held      INT NOT NULL DEFAULT 0,
  credit_settled   INT NOT NULL DEFAULT 0,

  idem_key        VARCHAR(64)  NULL,
  error_code      VARCHAR(64)  NOT NULL DEFAULT '',
  error_msg       VARCHAR(512) NOT NULL DEFAULT '',
  started_at      DATETIME(3) NULL,
  finished_at     DATETIME(3) NULL,
  created_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  updated_at      DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_biz (biz_id),
  UNIQUE KEY uk_user_idem (user_id, idem_key),
  KEY idx_run (workflow_run_id),
  KEY idx_user_created (user_id, created_at),
  KEY idx_status (status, created_at)
) ENGINE=InnoDB DEFAULT CHARSET=utf8mb4;

-- 投影表：只读视图，真相在 Aether Store
CREATE TABLE job_nodes (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  job_id        BIGINT UNSIGNED NOT NULL,
  task_run_id   VARCHAR(64)  NOT NULL,   -- Aether 的 TaskRunID
  node_name     VARCHAR(64)  NOT NULL,   -- gen-one-shot
  loop_index    INT          NOT NULL DEFAULT -1,  -- 循环轮次，非循环为 -1
  parent_scope  VARCHAR(64)  NOT NULL DEFAULT '',  -- 嵌套 scope 路径
  executor_type VARCHAR(32)  NOT NULL,
  phase         VARCHAR(16)  NOT NULL,   -- Created/Ready/Running/Succeeded/Failed/
                                         -- Error/Timeout/Skipped/Cancelled/Suspended
  exec_code     TINYINT      NULL,
  asset_ids     JSON         NULL,
  credit_cost   INT          NOT NULL DEFAULT 0,
  error_msg     VARCHAR(512) NOT NULL DEFAULT '',
  started_at    DATETIME(3) NULL,
  finished_at   DATETIME(3) NULL,
  updated_at    DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_task_run (task_run_id),
  KEY idx_job (job_id, loop_index)
) ENGINE=InnoDB;

CREATE TABLE assets (
  id, biz_id CHAR(26) NOT NULL, user_id BIGINT UNSIGNED NOT NULL,
  project_id BIGINT UNSIGNED NULL,
  type        VARCHAR(16) NOT NULL,   -- image/video/audio
  source      VARCHAR(16) NOT NULL,   -- upload/generated/derived
  from_task_run_id VARCHAR(64) NOT NULL DEFAULT '',
  parent_asset_id  BIGINT UNSIGNED NULL,

  storage_key VARCHAR(512)  NOT NULL,
  public_url  VARCHAR(1024) NOT NULL DEFAULT '',
  thumb_key   VARCHAR(512)  NOT NULL DEFAULT '',
  mime        VARCHAR(64)   NOT NULL,
  width INT NOT NULL DEFAULT 0, height INT NOT NULL DEFAULT 0,
  duration_ms INT NOT NULL DEFAULT 0, size_bytes BIGINT NOT NULL DEFAULT 0,
  resolution_tag VARCHAR(16) NOT NULL DEFAULT '',  -- 768P / 2K，升级流程要用

  first_frame_asset_id BIGINT UNSIGNED NULL,
  last_frame_asset_id  BIGINT UNSIGNED NULL,       -- ⭐ 连续影片衔接

  meta JSON NULL,   -- {seed, model, prompt_snapshot, minimax_task_id, usage}
  moderation_status VARCHAR(16) NOT NULL DEFAULT 'pending',
  deleted_at DATETIME(3) NULL,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id), UNIQUE KEY uk_biz (biz_id),
  KEY idx_user_type (user_id, type, deleted_at, id),
  KEY idx_project (project_id, id)
) ENGINE=InnoDB;

-- ⭐ 新增：MiniMax 素材 file_id 映射缓存，避免同一素材重复上传
CREATE TABLE provider_files (
  id            BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  asset_id      BIGINT UNSIGNED NOT NULL,
  provider_code VARCHAR(32)  NOT NULL DEFAULT 'minimax',
  account_id    BIGINT UNSIGNED NOT NULL,
  file_id       VARCHAR(128) NOT NULL,      -- 用于 mm_file://{file_id}
  purpose       VARCHAR(32)  NOT NULL DEFAULT '',
  expire_at     DATETIME(3)  NULL,
  created_at    DATETIME(3)  NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_asset_account (asset_id, account_id),
  KEY idx_expire (expire_at)
) ENGINE=InnoDB;

CREATE TABLE credit_accounts (
  user_id BIGINT UNSIGNED NOT NULL,
  balance INT NOT NULL DEFAULT 0,
  held    INT NOT NULL DEFAULT 0,
  version INT NOT NULL DEFAULT 0,
  updated_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3) ON UPDATE CURRENT_TIMESTAMP(3),
  PRIMARY KEY (user_id)
) ENGINE=InnoDB;

CREATE TABLE credit_ledger (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  user_id BIGINT UNSIGNED NOT NULL,
  direction VARCHAR(16) NOT NULL,   -- recharge/hold/commit/refund/expire
  amount INT NOT NULL,              -- 有符号
  balance_after INT NOT NULL, held_after INT NOT NULL,
  ref_type VARCHAR(32) NOT NULL,    -- job/task_run/admin
  ref_id   VARCHAR(64) NOT NULL,
  idem_key VARCHAR(96) NOT NULL,
  remark   VARCHAR(255) NOT NULL DEFAULT '',
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id),
  UNIQUE KEY uk_idem (idem_key),        -- ⭐ 幂等靠这个索引，不靠代码
  KEY idx_user_time (user_id, created_at)
) ENGINE=InnoDB;

CREATE TABLE provider_calls (
  id BIGINT UNSIGNED NOT NULL AUTO_INCREMENT,
  task_run_id VARCHAR(64) NOT NULL,
  provider_code VARCHAR(32) NOT NULL, account_id BIGINT UNSIGNED NOT NULL,
  model VARCHAR(64) NOT NULL,
  phase VARCHAR(16) NOT NULL,      -- submit/query/callback/download/upload
  request_body JSON, response_body JSON,
  http_status INT, biz_status_code INT,   -- ⭐ 图片接口的 base_resp.status_code
  latency_ms INT,
  minimax_task_id VARCHAR(128) NOT NULL DEFAULT '',
  minimax_request_id VARCHAR(64) NOT NULL DEFAULT '',  -- ⭐ 报障给厂商用
  usage_json JSON,                 -- ⭐ 精确结算依据
  cost_yuan  DECIMAL(10,4) NOT NULL DEFAULT 0,
  created_at DATETIME(3) NOT NULL DEFAULT CURRENT_TIMESTAMP(3),
  PRIMARY KEY (id), KEY idx_task (task_run_id), KEY idx_time (created_at)
) ENGINE=InnoDB;
```

`projects` / `characters` / `presets` / `moderation_records` / `provider_accounts` 沿用 v0.1 设计，此处从略。

---

## 10. 执行器设计

### 10.1 执行器清单

| Executor Type | 同步/异步 | 说明 |
|---|---|---|
| `minimax.image` | **同步** | `POST /v1/image_generation`，n=1..9 一次返回 |
| `minimax.video` | **异步** | 提交 → 回调/轮询 → 转存 |
| `minimax.video.regen` | **异步** | 768P → 2K |
| `minimax.context_ir` | **异步** | 提示词增强 |
| `minimax.file.upload` | 同步 | 素材换 file_id |
| `local.compose` | 同步 | 2x2 拼版 |
| `local.ffmpeg.extract` | 同步 | 抽首/尾帧 |
| `local.ffmpeg.concat` | 同步 | 拼接成片 |
| `local.moderation` | 同步 | 敏感词过滤 |
| `human.gate` | — | 直接返回 `ExecCode 1 (Suspended)` |
| `mock.image` / `mock.video` | 同步 | 本地假实现，**第 1 周就写** |

### 10.2 同步执行器（图片）—— 简单形态

```go
// internal/infra/executor/minimax/image.go
func (p *ImagePlugin) Type() string { return "minimax.image" }

func (p *ImagePlugin) Execute(ctx context.Context, req *ExecuteRequest) (*model.ExecOutputs, error) {
    in := parseInputs(req.Inputs)

    // 1. 取供应商令牌（并发/RPM 控制，Aether 不管这个）
    release, err := p.limiter.Acquire(ctx, "minimax.image", 20*time.Second)
    if err != nil {
        return outputs(ExecCodeError, "no_capacity"), nil   // 引擎会按 retry 策略退避
    }
    defer release()

    // 2. PromptSpec → MiniMax 请求（含 1500 字符裁剪）
    body := p.compiler.CompileImage(in)

    // 3. 同步调用
    resp, err := p.client.GenerateImage(ctx, body)
    if err != nil {
        return nil, err   // 网络层错误，引擎按 retry 处理
    }

    // 4. ⚠️ 错误在 body 里，HTTP 恒 200
    switch resp.BaseResp.StatusCode {
    case 0:            // 继续
    case 1002:         return outputs(ExecCodeError, "rate_limited"), nil        // 可重试
    case 1008:         return outputs(ExecCodeError, "insufficient_balance"), nil // 告警
    case 1026:         return outputs(ExecCodeFailed, "sensitive_content"), nil   // 终态，不重试
    case 1004, 2049:   return outputs(ExecCodeFailed, "auth_error"), nil
    case 2013:         return outputs(ExecCodeFailed, "bad_params"), nil
    default:           return outputs(ExecCodeError, resp.BaseResp.StatusMsg), nil
    }

    // 5. ⚠️ 部分成功：n=4 可能只回 3 张
    //    不在这里判成败——交给 phaseConditions 用表达式判
    assetIDs := p.materialize(ctx, resp.Data.ImageURLs)   // 24h 就过期，立刻转存

    return &model.ExecOutputs{
        Code: 0,
        Parameters: map[string]string{
            "asset_ids":     jsonArray(assetIDs),
            "success_count": resp.Metadata.SuccessCount,
            "failed_count":  resp.Metadata.FailedCount,
            "requested_n":   strconv.Itoa(in.N),
            "cost_yuan":     fmt.Sprintf("%.4f", float64(len(assetIDs))*0.025),
        },
    }, nil
}
```

### 10.3 异步执行器（视频）—— 三段式

视频执行器要处理「提交后等几分钟」。有两种做法：

**做法 A（推荐）：执行器内部阻塞等待。**
Execute() 里提交后自己轮询/等回调，直到终态才返回。配合 Aether 的 `timeout: "30m"` 和 Worker 池并发度控制。

- 优点：语义最简单，一个 Task = 一次完整生成，Aether 的重试/超时直接可用
- 缺点：占用一个 Worker 槽几分钟。但视频任务本来就受供应商并发限制，Worker 池按 MiniMax 并发额度设置（比如 4），槽位不是瓶颈

**做法 B：拆成 submit / poll 两个 Task 节点。**
- 优点：Worker 不阻塞
- 缺点：DAG 变复杂，轮询状态要自己存，失去了 Aether 重试语义的简洁性

**POC 用 A。** 简单、够用、和 Aether 的模型契合。将来并发规模大了再拆。

```go
func (p *VideoPlugin) Execute(ctx context.Context, req *ExecuteRequest) (*model.ExecOutputs, error) {
    in := parseInputs(req.Inputs)

    release, err := p.limiter.Acquire(ctx, "minimax.video", 60*time.Second)
    if err != nil { return outputs(ExecCodeError, "no_capacity"), nil }
    defer release()

    // ── 阶段一：提交 ──
    body := p.compiler.CompileVideo(in)   // 含模式互斥校验、file_id 复用、7000 字符裁剪
    body.CallbackURL = p.callbackURL      // 配了回调就走回调
    taskID, err := p.client.CreateVideoTask(ctx, body)
    if err != nil {
        return p.classifyHTTPError(err), nil   // 视频接口用真实 HTTP 码 + OpenAI 风格错误体
    }
    p.recordCall(ctx, req.TaskRunID, "submit", body, taskID)

    // ── 阶段二：等待（回调优先，轮询兜底，官方建议 10s 间隔） ──
    task, err := p.waiter.Wait(ctx, taskID, WaitOpts{
        CallbackCh:   p.callbacks.Subscribe(taskID),
        PollInterval: 10 * time.Second,
        MaxWait:      25 * time.Minute,   // 略小于 Aether 的 30m timeout
    })
    if err != nil { return outputs(ExecCodeTimeout, "wait_timeout"), nil }

    switch task.Status {
    case "failed":    return outputs(ExecCodeFailed, task.Error), nil
    case "cancelled": return outputs(ExecCodeFailed, "cancelled_upstream"), nil
    }

    // ── 阶段三：转存（绝对不能省） ──
    assetID := p.materialize(ctx, task.Content.URL)
    // 视频额外抽首尾帧存为 image asset —— 连续影片的下一段要用
    firstID, lastID := p.extractFrames(ctx, assetID)

    return &model.ExecOutputs{
        Code: 0,
        Parameters: map[string]string{
            "asset_id":            assetID,
            "first_frame_asset_id": firstID,
            "last_frame_asset_id":  lastID,   // ⭐ 下一轮 Loop 的 first_frame
            "minimax_task_id":     taskID,
            "output_seconds":      strconv.Itoa(task.Usage.OutputSeconds),
            "resolution":          task.Resolution,
            "cost_yuan":           fmt.Sprintf("%.4f", p.calcCost(task.Usage, task.Resolution)),
        },
    }, nil
}
```

### 10.4 错误分类表（两套错误约定必须统一）

| MiniMax 返回 | 来源 | 归一化 ExecCode | 重试 | 积分 |
|---|---|---|---|---|
| 图片 `status_code=0` | body | 0 Succeeded | — | 结算 |
| 图片 `1002` / 视频 `429` | body / HTTP | 3 Error | ✅ 退避 | 不扣 |
| 图片 `1004` `2049` / 视频 `401` | body / HTTP | 2 Failed | ❌ | 退全额 + 告警 |
| 图片 `1008` / 视频 `402` | body / HTTP | 2 Failed | ❌ | 退全额 + **紧急告警** |
| 图片 `1026` / 视频 `422` | body / HTTP | 2 Failed | ❌ | 退全额（涉敏） |
| 图片 `2013` / 视频 `400` | body / HTTP | 2 Failed | ❌ | 退全额（参数错误是我们的 bug） |
| 视频 `500` / `529` | HTTP | 3 Error | ✅ 退避 | 不扣 |
| 网络超时 / 连接失败 | 传输层 | 3 Error | ✅ 退避 | 不扣 |
| 等待超过 25 分钟 | 自定 | 4 Timeout | ❌ | 退全额 |
| 图片部分成功 | outputs | 由 `phaseConditions` 判 | — | 按实出张数结算 |

**视频接口的 `request_id` 字段务必落库**（`provider_calls.minimax_request_id`）—— 出问题找厂商时这是唯一凭证。

### 10.5 Capability Matrix

```go
var MiniMaxCaps = []ModelCap{
  {
    Code: "image-01", Kinds: []Kind{Text2Image, Image2Image},
    AspectRatios: []string{"1:1","16:9","4:3","3:2","2:3","3:4","9:16","21:9"},
    MaxN: 9, MaxPromptChars: 1500, SupportsSeed: true,
    SupportsWidthHeight: true, WidthRange: [2]int{512, 2048}, MustBeMultipleOf: 8,
    SupportsStylePreset: false,
    CostPerImageYuan: 0.025,
  },
  {
    Code: "image-01-live", Kinds: []Kind{Text2Image},
    AspectRatios: []string{"1:1","16:9","4:3","3:2","2:3","3:4","9:16"},  // 无 21:9
    MaxN: 9, MaxPromptChars: 1500, SupportsSeed: true,
    SupportsStylePreset: true,
    StyleTypes: []string{"漫画","元气","中世纪","水彩"},
    CostPerImageYuan: 0.025,
  },
  {
    Code: "MiniMax-H3", Kinds: []Kind{Text2Video, Image2Video, Ref2Video},
    Resolutions: []string{"768P","2K"},
    DurationRange: [2]int{4, 15}, DurationIntegerOnly: true,
    AspectRatios: []string{"adaptive","21:9","16:9","4:3","1:1","3:4","9:16"},
    MaxPromptChars: 7000,
    MaxRefImages: 9, MaxRefVideos: 3, MaxRefAudios: 3, MaxTotalFiles: 12,
    MaxFirstFrame: 1, MaxLastFrame: 1,
    SupportsSeed: false,          // ⚠️ 视频没有 seed
    SupportsCallback: true,
    SupportsRegeneration: true,   // 768P → 2K
    // 🔴 互斥规则
    MutualExclusions: [][]string{
      {"first_frame","last_frame"}, {"reference_image","reference_video","reference_audio"},
    },
    // 🔴 条件规则
    ConditionalRules: []Rule{
      {When: "mode==t2va", Require: "ratio != adaptive && ratio != ''"},
      {When: "mode==i2va", Force:   "ratio = adaptive"},
    },
    CostPerSecondYuan: map[string]float64{"768P": 0.50, "2K": 0.80},
    RegenCostPerSecondYuan: 0.30,
    FreeInputImages: 5, ExtraInputImageYuan: 0.20,
  },
}
```

前端启动拉 `GET /api/v1/capabilities` 缓存，参数选择器据此渲染禁用态。**互斥规则必须在前端就拦住**，不能等 API 报错。

---

## 11. 调度、并发与可靠性

### 11.1 队列分区（asynq 实现 `broker.TaskBroker`）

| 队列 | 承载执行器 | Worker 并发 | 说明 |
|---|---|---|---|
| `q:image` | minimax.image | 8 | 同步，秒级 |
| `q:video` | minimax.video / regen | **= MiniMax 视频并发额度** | 阻塞式，分钟级 |
| `q:local` | local.* | 4 | ffmpeg，吃 CPU |
| `q:llm` | minimax.context_ir | 4 | |
| `q:critical` | 手动重试、单图交互 | 4 | 优先级最高 |

asynq 队列权重（非严格优先级，避免低优饿死）：
`{critical: 6, image: 4, video: 3, local: 2, llm: 2}`

### 11.2 供应商并发与限流（Aether 不管，必须自建）

**两道控制：**

**① Worker 池规格 = 供应商额度。** 视频 Worker 进程的并发度直接设成 MiniMax 允许的并发数。这是 Aether「Worker 自给自足、按类型独立部署」设计的直接红利——不需要额外的分布式协调。

**② Redis 令牌桶（多 Worker 实例时的兜底）。** 用 ZSET 实现带过期的信号量，防止 Worker 崩溃导致令牌永久泄漏：

```
key: sem:minimax:video:{account_id}
Lua 原子执行：
  1) ZREMRANGEBYSCORE key 0 (now - leaseTTL)     清理过期令牌
  2) if ZCARD key < maxConcurrency:
  3)     ZADD key now token; return 1
  4) else: return 0
```

**同时做用户级公平限制**，防止一个用户批量提交饿死其他人：
`sem:user:{user_id}` 最大 in-flight 按 plan 分级（free:1 / standard:3 / premium:6）

拿不到令牌返回 `ExecCodeError`，引擎按退避策略重试，不算业务失败。

### 11.3 回调 + 轮询混合

```
配置 callback_url → MiniMax 先发 challenge 验证（3 秒内原样返回）
                  → 验证通过后每次状态变更 POST 推送

回调到达 → 写 Redis Pub/Sub → 执行器的 waiter 收到 → 立即继续
回调没到 → 10 秒轮询兜底（官方建议间隔）
两者都没到 → 25 分钟后 ExecCodeTimeout
```

回调端点必须做：
- **challenge 握手**（3 秒硬性要求，处理逻辑要极简，不查库不做业务）
- **签名/来源校验**（防伪造）
- **幂等**（同一 task_id 的同一状态重复推送要吃掉）
- **快速返回**（收到即入队，业务处理异步做）

### 11.4 Scheduler 进程的附加职责

| 任务 | 频率 | 做什么 |
|---|---|---|
| Aether Engine 主循环 | 事件驱动 | 判定就绪、派发、推进 |
| 轮询兜底扫描 | 60s | 有 `minimax_task_id` 但长时间无状态更新的 → 主动查 |
| 孤儿任务对账 | 10min | 用 MiniMax「查询任务列表」（近 7 天）比对本地，找出丢失的任务 |
| 挂起超时清理 | 1h | Suspended 超 7 天未 Resume → 自动取消并退积分 |
| 每日财务对账 | 03:00 | `balance + held == SUM(ledger.amount)`，不一致告警 |
| 素材 file_id 过期清理 | 每日 | 清 `provider_files` 过期记录 |

Scheduler 单实例，Redis `SET NX EX` 做 Leader 选举（30 秒续约）。

### 11.5 幂等四道防线

| 层 | 手段 |
|---|---|
| HTTP | `Idempotency-Key` header → Redis SETNX(24h) + `jobs.uk_user_idem` |
| 工作流 | Aether 的 TaskRunID 天然唯一 |
| 队列 | asynq `WithUniqueFor(5m)` |
| 积分 | `credit_ledger.uk_idem` 唯一索引 —— **由数据库保证，不靠代码** |
| 回调 | task_id + status 组合去重 |

### 11.6 观测指标

```
aigc_task_duration_seconds{executor,phase}       直方图
aigc_task_total{executor,exec_code}              计数器
aigc_queue_depth{queue}                          Gauge
aigc_minimax_concurrency_used{model}             Gauge   ← 判断要不要提额
aigc_minimax_error_total{code}                   计数器
aigc_cost_yuan_total{model,resolution}           计数器  ← 真金白银
aigc_job_e2e_seconds{workflow}                   直方图  ← 用户体感
aigc_suspended_jobs                              Gauge   ← 卡在预览门的数量
aigc_callback_vs_poll_ratio                      Gauge   ← 回调健康度
```

---

## 12. 积分与成本模型

### 12.1 MiniMax 刊例价（成本侧）

| 项 | 单价 |
|---|---|
| 图片 image-01 / image-01-live | **¥0.025 / 张** |
| 视频 H3 768P | **¥0.50 / 秒** |
| 视频 H3 2K | **¥0.80 / 秒** |
| 视频再生成 768P→2K | **¥0.30 / 秒** |
| 视频输入音频 | 免费 |
| 视频输入图片 | **5 张以内免费**，超出 ¥0.20/张（再生成时 ¥0.15/张） |
| 视频输入视频 | 按输入时长 × 输出分辨率单价：2K ¥0.80/s，768P ¥0.50/s（再生成 ¥0.30/s） |
| H3-Context-IR | 输入 ¥5.80/M tokens，输出 ¥23.00/M tokens |

⚠️ **输入视频很贵**：给 2K 生成挂一段 10 秒参考视频 = ¥8.00 额外成本，比 5 秒输出本身还贵。UI 必须在挂参考视频时明确展示增量费用。

### 12.2 积分定价（售价侧）

**定义：1 积分 ≈ ¥0.10 售价，毛利系数 K（配置项，POC 默认 2.0），向上取整，最低扣 1 积分。**

```
积分 = ceil( 成本(元) × K / 0.10 )
```

| 场景 | 成本 | 积分（K=2.0） |
|---|---|---|
| 单图 | ¥0.025 | **1** |
| 批量 4 张 | ¥0.10 | **2** |
| 批量 9 张 | ¥0.225 | **5** |
| 四格漫画 | ¥0.10 | **2** |
| 视频 768P 5s | ¥2.50 | **50** |
| 视频 768P 10s | ¥5.00 | **100** |
| 视频 2K 5s | ¥4.00 | **80** |
| 视频 2K 10s | ¥8.00 | **160** |
| 再生成 768P→2K 5s | ¥1.50 | **30** |
| 连续 3 段 5s，全 768P | ¥7.50 | **150** |
| 连续 3 段，768P + 升 1 段 | ¥9.00 | **180** |
| 连续 3 段，全部直接 2K | ¥12.00 | **240** |

**这个量级和即梦（8s 720P 显示 48 积分）基本对齐**，用户认知不会错位。

⚠️ **视频比图片贵 50~160 倍**，UI 上必须让用户对这个差异有感知，否则会出现「随手点了几十次视频生成然后余额没了」的投诉。建议：视频生成按钮上永久显示积分数，且首次生成视频时弹一次成本提示。

### 12.3 积分流转

```
提交时（单事务）：
  1. 校验 balance >= estimated
  2. UPDATE credit_accounts SET balance = balance - est, held = held + est,
       version = version + 1
     WHERE user_id = ? AND version = ? AND balance >= est
  3. INSERT credit_ledger(direction='hold', amount=-est, idem_key='job:{id}:hold')
  4. Engine.Submit(workflow, args)

每个节点成功时（hook.Notifier 触发）：
  实际成本 = 从执行器 outputs 的 cost_yuan 读（视频用 usage.output_seconds 精确算）
  actual_credits = ceil(cost_yuan × K / 0.10)
  INSERT credit_ledger(direction='commit', idem_key='task_run:{id}:commit')
  UPDATE credit_accounts SET held = held - actual

工作流终态时（结算）：
  refund = credit_held - SUM(已 commit)
  if refund > 0:
     UPDATE credit_accounts SET balance = balance + refund, held = held - refund
     INSERT credit_ledger(direction='refund', idem_key='job:{id}:refund')
```

**预览门的特殊处理**：`video.sequence` 提交时只预扣 768P 部分的积分。用户在预览门勾选要升 2K 的段之后，Resume 前**再做一次预扣**（`idem_key='job:{id}:hold:upgrade'`）。这样用户不会被一开始就冻结全额 2K 的积分吓退。

**对账不变量**（每日校验，结果必须为空）：

```sql
SELECT user_id FROM credit_accounts a
WHERE a.balance + a.held <>
      (SELECT COALESCE(SUM(amount),0) FROM credit_ledger l WHERE l.user_id = a.user_id);
```

---

## 13. API 设计

### 13.1 约定

Base `/api/v1`；`Authorization: Bearer <jwt>`；写接口支持 `Idempotency-Key`；cursor 分页；
错误体 `{"code":"...","message":"...","request_id":"..."}`

### 13.2 端点

```
【认证】
POST   /auth/register  /auth/login  /auth/refresh
GET    /me

【能力与预设】
GET    /capabilities                  # Capability Matrix（含互斥规则），前端缓存
GET    /presets?category=style

【项目 / 资产 / 角色】
POST   /projects              GET /projects        GET|PATCH|DELETE /projects/{biz_id}
POST   /assets/upload-url     # 预签名直传
POST   /assets/{biz_id}/complete
GET    /assets?type=&project_id=&cursor=
GET|DELETE /assets/{biz_id}
POST   /characters            GET /characters      PATCH|DELETE /characters/{biz_id}

【生成】
POST   /jobs/estimate                 # 只算积分与成本明细，不提交
POST   /jobs                          # ⭐ {workflow_name, spec, project_id}
GET    /jobs?status=&cursor=
GET    /jobs/{biz_id}                 # 含完整节点树（DAG + Loop 展开）
GET    /jobs/{biz_id}/events          # ⭐ SSE
POST   /jobs/{biz_id}/cancel          # 同时向 MiniMax 取消排队任务
POST   /jobs/{biz_id}/nodes/{node}/retry
POST   /jobs/{biz_id}/resume          # ⭐ 预览门"继续"

【积分】
GET    /credits/balance   /credits/ledger?cursor=

【内部】
POST   /internal/callbacks/minimax    # ⚠️ challenge 握手，3 秒内原样返回
GET    /internal/health   /metrics
```

### 13.3 预估接口（成本透明化）

```jsonc
// POST /api/v1/jobs/estimate
{ "workflow_name": "video.sequence", "spec": { ...PromptSpec... } }

// 200
{
  "credits_total": 150,
  "cost_yuan_estimated": 7.50,
  "breakdown": [
    { "node": "shot_1", "model": "MiniMax-H3", "resolution": "768P",
      "duration_s": 5, "credits": 50, "cost_yuan": 2.50 },
    { "node": "shot_2", "credits": 50, "cost_yuan": 2.50 },
    { "node": "shot_3", "credits": 50, "cost_yuan": 2.50 },
    { "node": "concat", "credits": 0, "cost_yuan": 0 }
  ],
  "upgrade_hint": {
    "message": "升级为 2K 需在预览后单独确认",
    "credits_per_shot": 30,
    "credits_if_all": 90,
    "compare_direct_2k": 240
  },
  "warnings": [
    "已选择参考视频（8 秒），将额外产生 ¥4.00 输入素材费用"
  ]
}
```

### 13.4 Resume 接口（预览门）

```jsonc
// POST /api/v1/jobs/{biz_id}/resume
{
  "node_name": "preview-gate",
  "inputs": {
    "selected_shots": [0, 2],       // 只升第 1、3 段为 2K
    "redo_shots": [1],              // 第 2 段重做 768P
    "redo_prompt_overrides": { "1": "改成近景，光线更暖" }
  }
}
```

### 13.5 SSE

```
event: node_update
data: {"node":"gen-one-shot","loop_index":0,"phase":"Running"}

event: node_update
data: {"node":"gen-one-shot","loop_index":0,"phase":"Succeeded",
       "assets":[{"biz_id":"ast_x","url":"https://cdn/...","resolution":"768P"}],
       "credit_cost":50}

event: job_update
data: {"status":"suspended","suspended_node":"preview-gate",
       "action_required":"select_shots_to_upgrade"}

event: job_update
data: {"status":"succeeded","credit_settled":180,"credit_refunded":0,
       "final_asset":{"biz_id":"ast_final","url":"https://cdn/output.mp4"}}

event: done
data: {}
```

⚠️ Nginx 必须配 `proxy_buffering off; proxy_read_timeout 600s;`，且前端必须有轮询降级，否则用户会看到进度永远卡住。

---

## 14. 前端方案

### 14.1 选型：React（理由与 v0.1 相同但更强）

`react-flow` 现在不只是「锦上添花」——Aether 的 DAG + **嵌套 Loop** 结构，用文本列表根本表达不清（三段视频每段又是一个内层 DAG）。这个可视化是刚需。

```
React 18 + TypeScript + Vite
├── 路由      react-router v6
├── 服务端态  TanStack Query v5
├── 客户端态  Zustand
├── 样式      Tailwind + shadcn/ui
├── 表单      react-hook-form + zod   ← zod 正好用来做互斥规则校验
├── 流程图    react-flow              ← DAG + Loop 嵌套可视化
├── 拖拽      dnd-kit                 ← 分镜重排
└── SSE       原生 EventSource + 轮询降级
```

熟 Vue 的话 Vue 3 + Pinia + Naive UI 也跑得起来，不影响后端。**POC 阶段用熟的框架比用"生态更好的"框架重要。**

### 14.2 核心页面

| 页面 | 关键点 |
|---|---|
| **创作台** | 结构化输入面板：文本 + 角色槽 A/B + 素材上传（`@图片1` 标签化）+ 预设卡片横滑 + 参数条 + **实时积分预估** + 生成按钮。右侧 SSE 实时填充结果 |
| **模式互斥拦截** | 选了参考素材 → 首尾帧入口置灰 + tooltip 说明原因；反之亦然。这是 P0，不做的话用户会一直遇到莫名其妙的 400 |
| **四格漫画编辑器** | 4 格文案输入（或一段剧情 + AI 拆分），共享角色/风格/seed，2x2 预览，单格可重生成 |
| **分镜编辑器** | 时间轴式列表，拖拽排序、增删镜头，每镜头独立 prompt + 时长，显示衔接方式（尾帧/参考）与漂移警告 |
| **⭐ 预览门** | 挂起态专属界面：N 段 768P 视频并排播放，每段有「升 2K」「重做」「丢弃」三个操作，底部实时显示「当前选择将消耗 X 积分」与「全部直接出 2K 需 Y 积分」的对比 |
| **作业详情** | react-flow 渲染 DAG，节点带状态色，Loop 折叠/展开，Suspended 节点高亮并带「继续」按钮 |
| **资产库** | 瀑布流 + 筛选，看生成参数，「以此再生成」回填 PromptSpec，视频显示 768P/2K 标签 |
| **角色库** | 角色卡片，参考图管理，显示已缓存的 MiniMax file_id 状态 |

---

## 15. 工程落地与目录结构

```
aigc-platform/
├── cmd/
│   ├── api/main.go              # Gin HTTP
│   ├── scheduler/main.go        # 内嵌 Aether Engine + 定时协调
│   ├── worker/main.go           # 执行器宿主
│   └── cli/main.go              # 发积分、重跑、种子数据、导入 workflow 定义
├── internal/
│   ├── domain/                  # 纯业务，零框架依赖
│   │   ├── job/                 # Job 实体
│   │   ├── prompt/              # PromptSpec + PromptCompiler + Capability
│   │   ├── asset/
│   │   ├── credit/
│   │   ├── workflow/            # ⭐ Engine 接口（业务只认这个，不认 aether）
│   │   └── provider/            # Provider 抽象（预留多供应商）
│   ├── application/
│   │   ├── jobsvc/              # Create / Cancel / Resume / Retry
│   │   ├── estimate/            # 成本预估
│   │   └── projection/          # hook.Notifier → 业务表 + SSE + 积分结算
│   ├── infra/
│   │   ├── workflow/aether/     # ⭐ 唯一 import aether 的包
│   │   │   ├── engine.go        #   Engine 接口的 aether 实现
│   │   │   ├── store_mysql.go   #   store.Store 实现
│   │   │   ├── broker_asynq.go  #   broker.TaskBroker 实现
│   │   │   ├── expr.go          #   expr.Evaluator（expr-lang）
│   │   │   ├── hook.go          #   hook.Notifier
│   │   │   └── secret.go        #   secret.Provider
│   │   ├── executor/
│   │   │   ├── minimax/         #   image / video / regen / context_ir / file
│   │   │   ├── local/           #   compose / ffmpeg / moderation
│   │   │   ├── human/           #   gate（返回 Suspended）
│   │   │   └── mock/            #   ⭐ 第 1 周就写
│   │   ├── persistence/         # GORM 仓储 + 关键路径原生 SQL
│   │   ├── storage/             # MinIO/OSS
│   │   ├── media/               # ffmpeg 封装
│   │   └── cache/               # Redis：锁、令牌桶、Pub/Sub
│   ├── interfaces/http/         # Gin handlers + DTO + 中间件
│   └── pkg/                     # ulid、errors、logger、config
├── workflows/                   # ⭐ aether/v1 工作流 JSON 定义（版本化）
│   ├── image.single.json
│   ├── image.batch.json
│   ├── image.comic4.json
│   ├── image.sequence.json
│   ├── video.single.json
│   └── video.sequence.json
├── third_party/aether/          # ⭐ vendored，go.mod replace 指向这里
├── migrations/                  # goose
├── deploy/docker-compose.yml
├── web/                         # React
└── Makefile
```

**分层铁律**：`domain` 不 import `infra`，更不 import `aether`。依赖方向 `interfaces → application → domain ← infra`。

`workflows/` 目录里的 JSON 要进 git 并做 CI 静态校验——文章说得对，**工作流定义就是契约**，改它要走 review。

### 15.1 对「不熟 Go」的具体建议

1. **第 1 周别碰 wire**，手写构造函数注入，跑通再引入
2. **禁用 GORM 的 Hooks 和 AutoMigrate**：前者让执行顺序不可预测，后者在生产是灾难。用 goose
3. **`context.Context` 一路往下传** —— 这是超时和取消的唯一机制，不遵守的话 §F7.4 的取消功能做不出来
4. **错误用 `fmt.Errorf("...: %w", err)` 包装**，配合 `errors.Is/As` 做分类 —— §10.4 的错误表完全依赖这个
5. **每个 goroutine 都要有退出路径**（能被 ctx 取消），否则优雅关机做不了
6. **读 Aether 源码**：55 个 commit，一下午读完。`engine_dag.go` / `engine_loop.go` / `engine_sched.go` 是核心。这是很好的 Go 并发学习材料，而且将来出问题你能自己修

---

## 16. 里程碑排期

> 1~2 人全职，**7 周**（比 v0.1 多 1 周，用于 Aether 验证与集成）

| 周 | 目标 | 交付物 | 验收 / 关卡 |
|---|---|---|---|
| **W0.5** | 🚦 **Aether 可行性验证（go/no-go）** | 用 Playground 跑通 §2.3 的 6 项验证；读完 `specs/` 和 `store/` 接口 | **6 项全过 → 继续；任一不过 → 切 Plan B 自研编排器** |
| **W1** | 骨架 + 单图打通 | docker-compose；Gin + JWT；goose 迁移；`assets`/`jobs` 表；**mock 执行器**；`image.single` workflow | 前端点按钮 → 3 秒后看到假图，全链路通 |
| **W2** | Aether 生产化集成 | `store.Store` MySQL 实现；`broker.TaskBroker` asynq 实现；`expr.Evaluator`；`hook.Notifier` → 投影表 + SSE；scheduler 进程 | 杀掉 worker 重启，任务自动恢复；SSE 实时可见 |
| **W3** | MiniMax 图片 + 批量 + 四格 | `minimax.image` 执行器；错误码归一化；materialize 转存；`phaseConditions` 处理部分成功；`image.batch`（n=9）、`image.comic4`（DAG + compose）；Capability Matrix + 前端参数条 | 9 张一次出；四格自动拼版；断网重试成功；产物是自己域名 |
| **W4** | 角色 + 预设 + 连续图 | 角色库 + file_id 缓存；预设库 + 卡片选择器；PromptCompiler（含长度裁剪）；`image.sequence`（Loop 串行） | 四格里角色长相一致；序列图风格连贯 |
| **W5** | MiniMax 视频 | `minimax.video` 执行器（三段式 + 回调 challenge + 轮询兜底）；ffmpeg 抽首尾帧；`video.single`；**模式互斥的前后端校验** | 5s 768P 视频出片；故意触发互斥被前端拦住 |
| **W6** | 连续影片 + 预览门 | `video.sequence`（Loop + 混合衔接策略）；`human.gate` 执行器；Resume 接口；预览门 UI；`minimax.video.regen`；`local.ffmpeg.concat` | ⭐ 3 段 768P → 挂起 → 勾选 2 段升 2K → 拼接出成片 |
| **W7** | 收口 | 积分预扣/结算/退款 + 分段预扣 + 对账；敏感词过滤；限流；Prometheus + Grafana；DAG 可视化；孤儿任务对账；文档 + 部署脚本 | 强制失败 → 积分正确退回；对账 SQL 返回空 |

**风险缓冲**：W7 的监控和 DAG 可视化可延后。优先保证 W1–W6 功能闭环。
**如果 W0.5 走了 Plan B**，W2 改为实现自研编排器（约 400 行 + 测试），后续周次不变。

---

## 17. 演进路径

| 维度 | POC | 完整系统 | 改动量 |
|---|---|---|---|
| 编排引擎 | Aether vendored | 升级 Aether / 换 Temporal / 换自研 | 只改 `infra/workflow/aether/`；workflow JSON 定义可移植 |
| Broker | asynq (Redis) | RabbitMQ / Kafka / NATS | 换 `broker.TaskBroker` 实现 |
| 供应商 | MiniMax + mock | 多供应商 + 按成本/延迟/成功率智能路由 + 自动降级 | 新增执行器 + 路由策略；Capability Matrix 已抽象 |
| 视频执行器 | 阻塞式等待 | 拆 submit / poll 两节点，Worker 不阻塞 | workflow 定义改，执行器拆两个 |
| 存储 | MinIO 单机 | OSS/TOS + CDN + 多地域 | 换 Storage 实现 |
| 部署 | 3 进程同机 | K8s：worker 按队列分组独立扩缩，GPU 池独立 | Aether 的 Worker 自给自足设计已支持，只写 manifest |
| Workflow 定义 | `workflows/*.json` 文件 | DB 存储 + 版本管理 + 可视化编辑器（类 ComfyUI） | 协议已是声明式，改加载源即可 |
| 一致性 | hook 投影 + 定时对账 | Outbox + relay | 加 `outbox` 表和 relay 进程 |
| 数据库 | 单 MySQL | 读写分离 → `job_nodes` 按 user_id 分片 | 已有 user_id 冗余 |
| 积分 | CLI 发放 | 支付 + 订阅 + 优惠券 | ledger 已支持 `direction=recharge` |
| 多租户 | 无 | 加 `tenant_id` | 建表时预留 `tenant_id BIGINT DEFAULT 0` |

**现在就预留但不实现**（成本几乎为零，将来省大事）：
`tenant_id`、`outbox` 表、`assets.visibility`、`presets.owner_user_id`、`characters.lora_ref`、`provider_accounts` 多账号

---

## 18. 风险清单

| # | 风险 | 影响 | 缓解 |
|---|---|---|---|
| **R1** | **Aether 是 3 star / 无 release / 无文档的个人项目** | **最大单点风险**：API 突变、作者弃坑、遇 bug 无社区可问 | §2.3 三道闸门：W0.5 硬验证关卡 + vendored 锁 commit + 自有 Engine 接口包一层；Plan B 自研编排器（400 行）随时可切；BSD-3 允许 fork 自维护；代码量小到能整个读完 |
| **R2** | **MiniMax 首尾帧与参考素材互斥** | 连续影片无法同时做到「无缝衔接」+「角色强一致」 | §5.4 混合策略：首段 r2va 建立角色，后续 i2va 尾帧续接（尾帧本身携带角色）；每 3 段用 r2va 校准一次防漂移；前端硬拦互斥组合 |
| **R3** | **产物 URL 24 小时过期** | 致命：用户资产全变死链 | 执行器内强制 materialize；Scheduler 兜底扫描 `Succeeded` 但 `storage_key` 为空的 asset |
| **R4** | **视频成本是图片的 50~160 倍** | 用户误操作烧钱 / 你自己测试烧钱 | 默认 768P；预览-定稿两阶段；UI 永久显示积分；首次视频生成弹成本提示；用户日限额；开发期用 mock 执行器 |
| **R5** | 图片接口 HTTP 200 但 body 里报错，且 `n=4` 可能只回 3 张 | 静默失败，用户以为成功 | §10.2 错误码分支 + `phaseConditions` 判部分成功；前端明确展示「请求 4 张，实出 3 张」 |
| **R6** | MiniMax 新账号并发/RPM 额度低 | 批量排队极慢 | 提前申请提额；Worker 池规格对齐额度；前端展示排队位置与预估等待；`aigc_minimax_concurrency_used` 指标监控 |
| **R7** | 回调 challenge 3 秒硬超时 | 回调验证失败，退化成纯轮询 | 回调端点逻辑极简（不查库不做业务，收到即入队）；轮询兜底永远保留；`callback_vs_poll_ratio` 指标监控健康度 |
| **R8** | 四格/连续的**角色一致性不达标** | 核心卖点失效 | 图片侧固定 seed + 相同 prompt 前缀 + 角色参考图；视频侧无 seed，只能靠帧链条；接受"较一致"而非"完全一致"；预留 LoRA 路线 |
| **R9** | 连续影片**接缝跳变**与帧链漂移 | 成片不可用 | 尾帧续接 + prompt 显式写衔接动作；concat 时加 0.2s 交叉淡化；`recalibrate_every` 参数化 |
| **R10** | 你不熟 Go，W0.5 读 Aether 源码 + W2 实现 Store/Broker 是硬骨头 | 排期滑坡 | W1 只做 mock 执行器最小闭环，不被外部 API 阻塞；Store/Broker 接口小，可以先写内存版跑通再换 MySQL；关键并发代码（令牌桶、CAS）可以逐行讲 |
| **R11** | GORM 隐式行为导致并发 bug | 数据不一致，难排查 | 状态流转一律原生 SQL + 检查 RowsAffected；禁 Hooks 和 AutoMigrate |
| **R12** | 挂起的作业无人 Resume，积分长期冻结 | 用户余额"消失" | Suspended 超 7 天自动取消并退款；前端消息中心提醒待处理作业 |
| **R13** | ffmpeg 处理大视频占满 CPU | 拖垮 Worker | 独立 `q:local` 队列且并发写死；`os/exec` 带 ctx 超时；未来拆独立进程 |
| **R14** | 内容合规（真人肖像、二次元 IP） | 法律风险 | prompt 前置过滤 + 产物后置审核；不提供已知 IP 角色预设；`aigc_watermark` 按需开启 |
| **R15** | SSE 在 Nginx 反代下被缓冲 | 进度永远卡住 | `proxy_buffering off; proxy_read_timeout 600s;` + 前端轮询降级（必做） |

---

## 19. UI 设计规范与页面详细规格

> 本章设计语言主要参照字节系两款产品：**即梦**（AI 创作平台，你的直接对标）和**剪映**（视频编辑工具，其"模块化 + 草稿箱 + 素材生态"的交互哲学延续到了即梦身上）。参考依据见 §19.0。

### 19.0 参考产品的四条可迁移经验

先说清楚从即梦/剪映身上抄的到底是什么，不是抄样式，是抄**四条经过验证的交互哲学**：

| # | 经验 | 即梦/剪映的做法 | 在我们产品里的落地 |
|---|---|---|---|
| ① | **三栏工作台，创作区永远居中占主位** | <cite index="60-1">顶部导航六大模块（图像/视频/角色/素材/历史/个人中心）；左侧素材操作区（上传、历史素材、角色档案）；中间核心创作区（提示词、参数、预览）；右侧辅助区；底部作品管理区（草稿箱/已生成/导出/二次编辑）</cite> | §19.4「创作台」页面结构直接采用这个骨架，这是 P0 |
| ② | **参数公开 + 一键同款，把消费者转成创作者** | <cite index="62-1">即梦公开所有生成参数，用户看到喜欢的作品可以"一键同款"复现，把社区从展示区变成教学场，形成增长飞轮</cite> | POC 阶段没有公开社区，但机制原理同样用在"资产库"里：**任何一个历史产物点开都能看到完整 PromptSpec 并一键复现/微调**，这是 F2.5 的强化版，见 §19.4 |
| ③ | **预测式交互，生成完主动推荐下一步** | <cite index="62-1">用户生成一张图后，系统智能预测下一步，主动推荐"扩图"适配封面或"转视频"增加动效，主动推断意图降低操作与思考成本</cite> | §19.5.1，这是 UI 章节里最值得做的一条差异化功能 |
| ④ | **模块化 + 草稿箱，容错优先** | <cite index="63-1">剪映按功能模块化排布主界面，未完成的草稿自动保存；素材沉淀成可复用的共享库（黑罐头），支持素材包组合</cite> | §19.4「资产库/草稿」与 §19.6 的失败态设计；对应我们的 `Job` 可中断续接、`assets` 库可复用 |

**明确不抄的部分**：即梦/剪映是 C 端海量用户产品，有社区分发、模板市场、抖音生态联动。POC 阶段没有这些，社区化和生态联动放进 §17 演进路径，不在本期范围。

### 19.1 设计原则

1. **创作区永远是视觉重心**：结果预览区占屏幕最大面积，参数面板可折叠，绝不用弹窗打断创作流程（挂起态/预览门除外，那是真正需要用户决策的时刻）
2. **所有生成参数可见、可追溯、可复现**：任何产物点开必须能看到完整 PromptSpec，这是资产作为"可复用资产"而非"一次性输出"的前提（呼应 §1.1 PolyBuzz 反推）
3. **进度永远可见，绝不出现"黑箱等待"**：SSE 驱动的实时状态是全站基础设施，不允许任何页面在生成过程中只显示一个转圈图标而不说明当前在哪一步
4. **成本前置透明**：任何会花积分的操作，按钮上或按钮旁必须显示积分数，重大操作（切 2K、多素材参考）显示费用增量提示（呼应 §12.3 视频成本 50~160 倍图片的风险）
5. **容错优先于流畅**：网络抖动、SSE 断线、供应商超时是常态而非异常，每个状态都要有对应的降级 UI，不能只设计"理想路径"

### 19.2 设计系统基础

**视觉基调**：深色主题为主（创作类工具的行业默认，即梦/剪映/Midjourney/ComfyUI 均如此——深色背景让生成的图片/视频色彩更突出，长时间创作不刺眼）。浅色主题作为可切换的次要选项，P2。

```
色板（Tailwind token 命名）
├── background     zinc-950 / zinc-900（主背景 / 卡片背景）
├── foreground      zinc-50 / zinc-400（主文字 / 次要文字）
├── border          zinc-800
├── primary         violet-500（品牌色，用于生成按钮、进度条、选中态）
├── success         emerald-500（Succeeded）
├── warning         amber-500（Suspended / 需要用户操作）
├── destructive     red-500（Failed / 危险操作）
├── info            sky-500（Running）
└── muted           zinc-700（Pending / 禁用态）

字体：Inter（英文/数字）+ 系统默认中文字体栈；等宽字体用于积分数字与 ID
圆角：卡片 12px，按钮 8px，标签 999px（胶囊）
间距刻度：4 / 8 / 12 / 16 / 24 / 32 / 48（Tailwind 默认刻度）
动效：状态切换 200ms ease-out；SSE 更新的数字变化用 200ms 计数动画，不做生硬跳变
```

**组件库**：`shadcn/ui`（复制到项目内可直接改，不是黑盒依赖）+ Tailwind。状态色与 Job/Task 的 `phase` 枚举一一对应，全局唯一映射表（组件层和数据层共用同一套颜色语义，避免"状态是绿色但图标是灰色"这类不一致）：

| Phase | 颜色 | 图标 |
|---|---|---|
| Created / Ready / Pending | muted 灰 | 空心圆 |
| Running | info 蓝，带脉冲动画 | 旋转环 |
| Suspended | warning 黄，带呼吸动画（吸引注意） | 暂停符 |
| Succeeded | success 绿 | 勾 |
| Failed / Error | destructive 红 | 叉 |
| Timeout | destructive 红，带时钟图标 | 时钟叉 |
| Skipped | muted 灰，斜纹填充 | 斜杠 |
| Cancelled | muted 灰 | 禁止符 |

### 19.3 信息架构

```
/                          创作台（首页，登录/匿名皆可进入）
├── /studio/:workflow?     创作台，按形态切 tab（单图/批量/四格/连续图/单段视频/连续影片）
├── /jobs                  作业列表
│   └── /jobs/:id          作业详情（DAG 可视化 + 预览门入口）
├── /assets                资产库
│   └── /assets/:id        资产详情
├── /characters            角色库
│   └── /characters/:id
├── /presets               预设库（浏览/收藏，POC 阶段不支持创建）
├── /credits                积分余额与账本
└── /settings              账号设置
```

**导航结构直接借鉴即梦的顶部六模块 + 左侧素材区骨架**：顶部是「创作台 / 作业 / 资产 / 角色 / 积分」的一级导航（对应即梦的"图像生成/视频生成/角色管理/素材库/创作历史"），不做深层级菜单——创作类工具的核心诉求是"点最少的次数进入创作状态"。

### 19.4 页面详细规格

#### 19.4.1 创作台（Studio）—— 全站最高优先级页面

**布局**（三栏，参照即梦骨架）：

```
┌─────────────────────────────────────────────────────────────────┐
│ 顶部导航：创作台 | 作业 | 资产 | 角色 | ✦积分余额 128        头像 │
├──────────────┬──────────────────────────────┬───────────────────┤
│ 左栏 320px    │  中栏（弹性宽度，主视觉）        │  右栏 360px        │
│              │                                │                   │
│ 形态切换 Tab  │  ┌──────────────────────────┐  │  参数面板（可折叠） │
│ □ 单图        │  │                          │  │                   │
│ □ 批量出图    │  │   结果预览区              │  │  模型：MiniMax-H3  │
│ □ 四格漫画    │  │   （空状态/生成中/结果）    │  │  比例：3:4 ▾       │
│ □ 连续生成    │  │                          │  │  分辨率：768P ▾    │
│ □ 单段影片    │  │                          │  │  数量：n=4 ▾       │
│ □ 连续影片    │  └──────────────────────────┘  │  时长：5s ▾        │
│              │                                │                   │
│ 角色槽        │  ┌──────────────────────────┐  │  预设卡片横滑区     │
│ [A: 未选择+]  │  │ 文本输入框（多行，字数计数） │  │  风格｜姿势｜构图    │
│ [B: 未选择+]  │  │ "两人在天台对峙，黄昏..." │  │  [封面][封面][封面] │
│              │  └──────────────────────────┘  │                   │
│ 素材上传区     │                                │  ─────────────    │
│ [@图片1][+]   │  [✦ 预计消耗 50 积分]  [生成]  │  ⚠ 已选参考视频，   │
│ [@视频1][+]   │                                │  额外产生 ¥4.00    │
│              │                                │  素材费用          │
└──────────────┴──────────────────────────────┴───────────────────┘
```

**关键交互细节**：

- **形态 Tab 切换时参数面板联动变化**：切到"连续影片"，中栏文本框自动变成分镜列表（内嵌迷你分镜编辑器，见 19.4.4）；切到"单图"，右栏的 `n` 选择器隐藏
- **素材上传区即拖即传**：拖拽文件到 `[@图片1][+]` 卡片直接触发预签名上传，上传完成后卡片显示缩略图，鼠标悬停显示「设为：角色锚定 / 场景定调 / 运镜参考」的角色选择（对应 §5.2 的 `refs[].role`）
- **模式互斥的实时拦截**（F6.5，P0）：一旦上传了 `reference_*` 类型素材，「首尾帧」入口整体置灰并显示 tooltip「已选择参考素材，首尾帧模式不可用，点击移除参考素材以切换」；反之亦然。**这个规则必须在 zod schema 里和 UI 禁用态里各实现一遍，双保险**
- **积分预估实时刷新**：任何参数变更（比例、分辨率、数量、时长）触发防抖 300ms 后调用 `POST /jobs/estimate`，按钮文案从「生成」变成「✦ 50 生成」，成本明细可展开查看（对应 §13.3）
- **生成按钮态**：默认态「✦ 50 生成」→ 点击后「排队中...」（乐观更新，立即在结果区插入一个 pending 卡片）→ 变成「生成中 · 预计 2 分钟」（视频场景显示预估时长）
- **结果预览区的空状态**：不是空白，是**灵感引导**——展示 3~4 个「猜你想生成」的示例卡片（复用 presets 表的封面图），点击直接把该预设的 prompt 片段填入输入框。这是把「预测式交互」原则前移到冷启动场景

#### 19.4.2 预测式下一步推荐 —— 结果卡片的核心交互（P0，差异化功能）

单图/批量生成完成后，每张结果图下方浮现一排推荐操作，不是固定菜单，是**根据产物类型动态生成**：

```
┌────────────────────┐
│                    │
│    [生成的图片]      │
│                    │
├────────────────────┤
│ 猜你想接着做：        │
│ [🎬 转成视频]  [🔁 再来一批]  [✨ 变成四格]  [⋯ 更多] │
└────────────────────┘
```

推荐规则（对应 §19.0 原则③）：

| 产物类型 | 推荐动作 | 背后逻辑 |
|---|---|---|
| 单图 | 转视频（预填 image2video，此图为首帧） | 图→视频是最高频的下一步 |
| 单图 | 再来一批（同 prompt 不同 seed，n=4） | 一次没满意，最低成本重试 |
| 单图（且未绑定角色） | 存为角色 | 引导角色库的沉淀，提升四格/连续生成的复用率 |
| 四格产物 | 转成分镜（预填 video.sequence，每格文案变成一个镜头） | 图文剧情天然延伸成动态叙事 |
| 768P 视频 | 升级 2K（预填单段再生成） | 呼应 §3.5/§12.2 的成本策略 |
| 视频（任意） | 抽帧存为角色参考 | 把视频里满意的一帧沉淀为可复用素材 |

这一条不是锦上添花——它是**把六种孤立形态串成一条创作流水线**的关键交互，直接提升「单图 → 批量 → 四格 → 连续图 → 单段视频 → 连续视频」这条演进路径的转化率，这也是即梦"预测式交互"的核心价值所在。

#### 19.4.3 四格漫画编辑器

```
┌─────────────────────────────────────────────────────────┐
│ 角色：[A: 少年 ▾] [B: 少女 ▾]   风格：[治愈日系 ▾]         │
├─────────────────────────────────────────────────────────┤
│ ┌───剧情输入（可选）──────────────────┐  [AI 拆成 4 格 →]  │
│ │ 少年在雨夜的便利店遇见了失忆的少女…    │                  │
│ └────────────────────────────────────┘                  │
│                                                           │
│ ┌──格 1──┐ ┌──格 2──┐ ┌──格 3──┐ ┌──格 4──┐              │
│ │[文案框] │ │[文案框] │ │[文案框] │ │[文案框] │              │
│ │[预览图] │ │[预览图] │ │[预览图] │ │[预览图] │              │
│ │ 🔁重做  │ │ 🔁重做  │ │ 🔁重做  │ │ 🔁重做  │              │
│ └────────┘ └────────┘ └────────┘ └────────┘              │
│                                                           │
│           [✦ 8 积分  生成四格]                            │
└─────────────────────────────────────────────────────────┘
```

- 4 个格子并行生成，**每格独立显示自己的状态**（Pending/Running/Succeeded/Failed），不等全部完成才展示，符合「进度永远可见」原则
- 单格失败时该格显示「重试」按钮，只重跑该节点（对应 F5.6），不影响其他 3 格
- 全部完成后自动触发 `compose_grid` 节点，中央大预览区展示拼版成品，四个小格仍保留可各自「重做」

#### 19.4.4 分镜编辑器（连续影片）

```
┌─────────────────────────────────────────────────────────┐
│ 镜头 1        镜头 2        镜头 3        [+ 添加镜头]     │
│ ┌────────┐   ┌────────┐   ┌────────┐                    │
│ │[封面帧] │ ⇢ │[封面帧] │ ⇢ │[封面帧] │                    │
│ │ 5s      │   │ 5s      │   │ 5s      │                    │
│ │[文案]   │   │[文案]   │   │[文案]   │                    │
│ │r2va·角色│   │i2va·尾帧│   │i2va·尾帧│                    │
│ └────────┘   └────────┘   └────────┘                    │
│   ⋮⋮ 拖拽排序                                              │
├─────────────────────────────────────────────────────────┤
│ 衔接方式：●混合（推荐） ○全部尾帧续接 ○每段独立           │
│ ⚠ 镜头数 >4 建议开启"每 3 段校准一次"防止角色漂移          │
├─────────────────────────────────────────────────────────┤
│ 预计消耗：✦ 150（先出 768P 预览）                          │
│                                    [生成预览 →]            │
└─────────────────────────────────────────────────────────┘
```

- 镜头卡片间的箭头标注衔接方式（`r2va·角色` / `i2va·尾帧`），让用户对 §5.4 的混合策略有直觉认知，不需要理解底层术语
- `dnd-kit` 拖拽重排镜头顺序，重排后衔接关系自动重算（尾帧箭头跟着重连）
- 底部**永远只显示 768P 预览的价格**，不显示 2K 全价——把用户默认引导到「先预览后定稿」的成本策略上（呼应 §3.6 产品核心循环）

#### 19.4.5 ⭐ 预览门（Preview Gate）—— 全站最重要的单一交互

这是 Aether suspend/resume + MiniMax 再生成定价共同催生的核心页面，是这个产品区别于「调一次 API 显示结果」的关键差异点。

```
┌─────────────────────────────────────────────────────────────┐
│  ⏸ 作业已暂停 · 请选择要升级为高清的镜头                        │
├─────────────────────────────────────────────────────────────┤
│  镜头 1 (768P)      镜头 2 (768P)      镜头 3 (768P)          │
│  ┌───────────┐     ┌───────────┐     ┌───────────┐          │
│  │ [视频播放] │     │ [视频播放] │     │ [视频播放] │          │
│  │  ▶ 0:05    │     │  ▶ 0:05    │     │  ▶ 0:05    │          │
│  ├───────────┤     ├───────────┤     ├───────────┤          │
│  │ ☑ 升级2K   │     │ ☐ 升级2K   │     │ ☑ 升级2K   │          │
│  │ ○ 重做     │     │ ● 重做     │     │ ○ 重做     │          │
│  │            │     │ [改写提示词]│     │            │          │
│  └───────────┘     └───────────┘     └───────────┘          │
├─────────────────────────────────────────────────────────────┤
│  当前选择：升级 2 段 · 重做 1 段                                │
│  将消耗 ✦ 90（对比：全部直接出 2K 需 ✦ 240，省 62%）            │
│                                                                │
│                          [取消作业]      [确认，继续生成 →]     │
└─────────────────────────────────────────────────────────────┘
```

- **视频卡片内联播放**，不是缩略图，用户需要真的看过 768P 效果才能决策——这是这个页面存在的全部意义
- 三个选项互斥单选：升级 2K / 重做（可改写 prompt）/ 保持 768P 不变（默认，不选升级也不选重做）
- 成本对比行是 P0：必须同时显示「当前选择」和「全都升 2K」两个数字，让省钱这件事可感知（对应 §12.2 的成本策略要转化成真实决策）
- 页面顶部用**呼吸动画的黄色**提示条常驻（对应 §19.2 状态色表的 Suspended），即使用户切换到其他页面，顶部导航栏的「作业」入口也要有一个数字角标提示「有 1 个作业等待你决策」——挂起态最大的风险是用户忘记回来处理（对应 R12），UI 必须主动提醒

#### 19.4.6 作业详情 / DAG 可视化

```
┌─────────────────────────────────────────────────────────┐
│  作业：连续影片 · 天台对峙        状态：● 运行中 (2/4)      │
├─────────────────────────────────────────────────────────┤
│                                                           │
│     [draft: Loop×3]━━┓                                   │
│      ┌──shot1──┐     ┃                                   │
│      │ ✓ 已完成 │     ┣━▶ [gate: 暂停中 ⏸]                │
│      ├──shot2──┤     ┃      需要你的操作 →                │
│      │ ⟳ 生成中 │     ┃                                   │
│      ├──shot3──┤     ┃                                   │
│      │ ○ 等待中 │     ┃                                   │
│      └─────────┘     ┛                                   │
│                                                           │
│  [Loop 节点可折叠展开，默认展开当前进行中的]                  │
├─────────────────────────────────────────────────────────┤
│  已消耗 ✦ 100 / 预估 ✦ 150          [取消作业] [下载已完成]  │
└─────────────────────────────────────────────────────────┘
```

- `react-flow` 渲染，Loop 节点用「折叠分组」样式（一个大框里套小节点），展开后能看到每一轮循环的独立状态——这是 v0.1 没有的复杂度，因为 v0.1 没有 Loop 原语
- 节点颜色严格对应 §19.2 的 Phase 颜色表
- 点击任意节点弹出侧边抽屉：显示该节点的 input/output、耗时、消耗积分、失败时的错误信息与「重试」按钮
- Suspended 节点直接可点击跳转到 §19.4.5 预览门

#### 19.4.7 资产库

```
┌─────────────────────────────────────────────────────────┐
│ 筛选：[全部▾] [图片][视频]   项目：[全部项目▾]   🔍 搜索     │
├─────────────────────────────────────────────────────────┤
│ [img][img][video 768P][img]                              │
│ [img][video 2K][img][img]        瀑布流，虚拟滚动           │
│ [img][img][img][video 768P]                              │
└─────────────────────────────────────────────────────────┘

点开任意资产 →
┌─────────────────────────────┐
│      [大图/视频播放]           │
├─────────────────────────────┤
│ 生成参数：                      │
│  模型 MiniMax-H3 · 768P · 5s    │
│  Prompt: "两人在天台对峙…"       │
│  Seed: 12345                   │
│  来自作业：#job_01HX...  →      │
├─────────────────────────────┤
│ [✏️ 以此再生成]  [⬇ 下载]  [🗑]  │
└─────────────────────────────┘
```

- 「以此再生成」是 §19.0 原则②的直接落地：点击后跳转创作台，PromptSpec 完整回填，用户可以改一两个词再生成——这是"参数公开+一键复现"机制在 POC（无公开社区）场景下的最小实现
- 视频资产卡片右上角常驻显示 `768P` / `2K` 标签，方便用户一眼分辨哪些是预览稿哪些是定稿

### 19.5 关键交互模式补充

#### 19.5.1 全局进度指示：绝不出现黑箱等待

除结果卡片内联的进度外，顶部导航栏「作业」入口本身是一个**持续可见的进度枢纽**：有进行中作业时显示环形进度角标；有挂起作业时显示黄色数字角标；纯失败/成功不常驻角标（避免通知疲劳）。

#### 19.5.2 移动端与响应式

POC 阶段**桌面优先**，三栏创作台在窄屏（<1024px）降级为单栏 + 底部抽屉参数面板（类似即梦/剪映移动端「素材区收起为底部弹层」的模式），预览门等关键决策页面在移动端保留完整信息密度，不裁剪成本对比数字——省钱信息任何屏幕尺寸都不能丢。这部分留 P1，POC 交付时允许移动端「可用但不精修」。

#### 19.5.3 Toast 与错误提示规范

| 场景 | 展现方式 |
|---|---|
| 积分不足无法提交 | 阻断式：按钮直接禁用 + 内联红字提示「余额不足，还需 ✦12」+ 充值入口 |
| 提交后网络错误 | Toast（可重试） |
| 单节点失败但作业仍在跑（partial） | 不打断，结果区对应位置显示失败态卡片 + 重试按钮，不弹 Toast 打扰 |
| SSE 断线 | 静默降级为轮询，仅在断线超 30 秒后显示细小的「重新连接中」提示条，不用 Toast 惊扰用户 |
| 内容审核拦截 | 阻断式：明确说明「描述涉及敏感内容，未扣除积分」，不展示具体触发词 |

### 19.6 关键组件规格表

| 组件 | 关键 props/状态 | 复用位置 |
|---|---|---|
| `<GenerationCard>` | phase / assets / creditCost / suggestedActions[] | 创作台结果区、四格格子、分镜卡片 |
| `<PhaseBadge>` | phase（统一枚举）| 全站任意展示状态的地方 |
| `<CreditEstimate>` | breakdown[] / totalCredits / warnings[] | 创作台、预览门、分镜编辑器 |
| `<RefUploadSlot>` | tag / role / assetPreview | 创作台素材上传区 |
| `<PresetCarousel>` | category / items[] / selected | 创作台右栏 |
| `<PreviewGateCard>` | shotIndex / videoUrl / action(upgrade/redo/keep) | 预览门 |
| `<WorkflowGraph>` | nodes[] / edges[] / loopGroups[] | 作业详情 |
| `<SuspendedBanner>` | jobId / actionRequired | 全站顶部条 + 作业列表 |

---

## 20. 用户画像与用户案例

### 20.1 用户画像

**画像一：小雨 —— 独立内容创作者 / 自媒体博主**
制作短剧解说、AI 情感故事类内容为主，单人产出，追求速度和成本可控。对标即梦/剪映的核心用户群。她关心「多久能出片」「够不够便宜」「角色像不像上一集」。不写代码，不理解「工作流」「节点」这些术语，只关心「点几下能拿到我要的东西」。

**画像二：阿哲 —— 小型工作室的美术/项目负责人**
带 2~3 人做条漫或轻小说插画外包，四格漫画/角色一致性是刚需，经常需要「同一批角色出不同场景」。他关心「角色能不能反复复用」「一次能不能批量出多个场景」「团队里其他人能不能看懂参数复现我的效果」。会主动研究预设组合，是「参数公开+一键同款」机制的重度受益者。

**画像三：Leo —— 你自己 / POC 的内部验证者**
既是产品决策者也是开发者，用这个产品验证从技术方案到用户体验的每一个环节是否闭环。他关心的不是「好不好看」，是「挂起态会不会卡死」「取消作业积分退没退」「断网重连后状态对不对」——这个画像存在的意义是提醒团队：**POC 的第一批真实用户就是自己，UI 的容错性和可观测性和视觉打磨同等重要，甚至更重要**。

### 20.2 核心用户故事

> 格式：作为 [画像]，我想要 [做什么]，以便 [达成什么]。附验收要点。

**单图 / 批量**

- 作为**小雨**，我想要输入一句话描述就能立刻看到一张图，以便快速判断这个 prompt 值不值得往下做。
  验收：从点击生成到首张图出现 ≤ 15 秒（MiniMax 图片接口同步返回，无排队）。
- 作为**小雨**，我想要一次性看到同一句话的 4 种不同演绎，以便挑一张我最喜欢的继续用。
  验收：`n=4` 一次调用返回，4 张图同时展示，若供应商因审核只回 3 张，页面明确显示「4 张中 3 张成功」而不是静默少显示一张。

**四格漫画**

- 作为**阿哲**，我想要把一段剧情描述交给系统自动拆成 4 个分镜，以便不用自己逐格构思文案。
  验收：AI 拆分完成后 4 格文案可编辑，不是只读结果。
- 作为**阿哲**，我想要 4 格里的角色长得像同一个人，以便这组图能直接拿去排版发布。
  验收：4 格共享同一角色参考图 + 同一 seed；若某一格明显跑偏，可单独重做该格而不牺牲另外 3 格。

**角色与预设复用**

- 作为**阿哲**，我想要把一个满意的角色形象存下来反复使用，以便系列内容保持人物一致。
  验收：角色库创建后，在任意生成表单里都能作为槽位 A/B 选中；角色参考图只需上传一次，后续复用走 `file_id` 缓存不重新计费上传。
- 作为**小雨**，我想要点开别人（或我自己）做过的一张图，看到它是怎么做出来的并直接复现，以便学习或复用好的效果。
  验收：资产详情页「以此再生成」一键回填完整 PromptSpec 到创作台。

**连续影片与预览门**

- 作为**小雨**，我想要先看 3 段视频的便宜预览版，确认满意的部分再花钱出高清，以便不把钱浪费在不满意的镜头上。
  验收：作业提交后先只扣 768P 部分积分；预览门里能清楚看到「只升 2 段」比「全部升级」省多少钱；确认后才产生 2K 部分的扣费。
- 作为**小雨**，我想要某一段效果不理想时能改写提示词重做这一段，而不用整个作业从头再来。
  验收：预览门支持单段「重做」并可改写该段 prompt，重做只影响该镜头节点，其余镜头保留。
- 作为**Leo**，我想要即使我中途关掉浏览器，之前生成到一半的挂起作业依然能在我回来后继续处理，以便不因为网络问题损失已经产生的花费。
  验收：Suspended 状态持久化在数据库；重新打开「作业」页面能看到待处理角标并跳回预览门，之前已生成的 768P 素材原样可见。

**成本与信任**

- 作为**小雨**，我想要在点生成之前就知道要花多少积分，以便决定要不要这么做。
  验收：任何参数变更后 300ms 内更新预估积分，不需要提交才知道价格。
- 作为**阿哲**，我想要一次生成失败或被取消时钱能退回来，以便放心尝试而不担心亏损。
  验收：失败/取消/超时节点对应的预扣积分在作业终态时自动退回，账本记录可查（`credits/ledger` 页面）。

**容错（Leo 视角为主，但影响所有用户）**

- 作为用户，我想要网络短暂断开时页面不假装卡死，以便知道系统还在正常工作。
  验收：SSE 断线 30 秒内静默切换轮询，无感知；超过 30 秒才显示细提示条。
- 作为用户，我想要看到某一步具体失败在哪、为什么失败，而不是一句「生成失败」，以便决定是重试还是改需求。
  验收：失败节点展示分类后的错误原因（审核拦截 / 参数错误 / 供应商限流 / 超时），不同原因给不同的下一步建议（改写 prompt / 稍后重试 / 检查参数）。

### 20.3 端到端旅程：小雨做一条 15 秒连续短片

这是一条贯穿创作台、分镜编辑器、预览门、资产库四个页面的完整旅程，用来验证整个 UI 体系是否真的闭环，不是四个孤立页面的堆砌：

```
1. 小雨打开创作台，切到「连续影片」Tab
   → 左栏角色槽选择她之前存过的角色「林夏」（一次上传参考图，反复复用）
   → 中栏分镜编辑器新增 3 个镜头，各写一句话描述，各 5 秒
   → 系统自动判定衔接方式：镜头1 用角色参考（r2va），镜头2/3 用尾帧续接（i2va）
   → 底部显示「✦ 150（先出 768P 预览）」

2. 点击「生成预览」
   → 顶部导航「作业」角标出现进度环
   → 跳转到作业详情页，看到 DAG：3 个并行/串行的镜头节点 + 一个 gate 节点
   → 镜头 1、2 陆续变绿，镜头 3 还在跑
   → 小雨切去做别的事，全程无需守着页面

3. 3 个镜头全部完成，gate 节点变黄色呼吸动画
   → 导航栏「作业」角标变成黄色数字「1」
   → 小雨点回来，自动跳转到预览门
   → 逐段播放 768P 预览：镜头1、3 满意勾选「升级2K」，镜头2 不满意点「重做」并改写了提词
   → 底部显示「将消耗 ✦90（对比全部升级需 ✦240，省62%）」
   → 点击「确认，继续生成」

4. 作业恢复运行（Resume）
   → 镜头2 重新生成 768P，镜头1、3 并行升级 2K
   → 全部完成后自动触发拼接节点，生成最终 mp4

5. 作业成品出现在资产库，标签「2K · 15s」
   → 小雨点击下载，同时该资产详情页保留完整 PromptSpec
   → 下次做同系列内容，直接从这个资产「以此再生成」，角色和风格不用重新配置
```

**这条旅程验证的不是某个单一页面好不好看，是整套「预扣-预览-决策-补扣-结算」的积分与状态机制，能不能被一个不懂技术的用户在不看文档的情况下走完。** 这是 UI 章节存在的根本目的，也是判断 §7 功能需求是否真正可用的最终标准。

---

## 附录 A：v0.1 → v0.2 变更对照

| 章节 | v0.1 | v0.2 | 原因 |
|---|---|---|---|
| 编排 | 自研 `Recipe` Go 接口 + DAG 编排器 | aether/v1 声明式 workflow JSON | Aether 协议直接覆盖需求，且 Loop 递归组合正好是连续影片的形状 |
| 批量出图 | N 个并行 Task | **1 个 Task（n=1..9）** | MiniMax 图片接口单次支持 9 张 |
| Worker 形态 | 统一三段式 submit/poll/materialize | 图片同步、视频异步，两套 | MiniMax 图片接口是同步的 |
| 连续影片 | 尾帧续接 或 provider 原生 extend | **只有尾帧续接**，且首段用 r2va 混合 | MiniMax 无 extend；首尾帧与参考素材互斥 |
| 人工介入 | 无 | **预览门（suspend/resume）** | Aether 原语 + MiniMax 再生成定价，两者结合的产物 |
| 成本策略 | 无 | **默认 768P，选择性升 2K** | 0.50 + 0.30 = 0.80，再生成成本中性 |
| asynq | 主角（任务队列） | 配角（`broker.TaskBroker` 的实现） | 层级关系变了，选型没变 |
| 积分定价 | 抽象公式 | 基于 MiniMax 刊例价的具体表 | 有了真实价格 |
| 排期 | 6 周 | **7 周**（含 W0.5 验证关卡） | Aether 集成成本 |
| 风险 | R5「不熟 Go」是最大风险 | **R1「Aether 项目成熟度」是最大风险** | |

## 附录 B：资料来源

| 来源 | 用途 |
|---|---|
| 你提供的 Aether 博客全文（vese.ai，2026-04-08） | 协议设计、三原语、suspend/resume、ExecCode/Phase、Fat TaskAssignment、六边形架构 |
| `github.com/BabySid/aether`（实测抓取） | 仓库真实状态：3 star / 0 fork / 55 commits / 无 release / README 2 行 / BSD-3-Clause / 包结构 |
| 你提供的 MiniMax 视频生成 V2 OpenAPI | content 数组、role 枚举与互斥、素材限制、错误码、callback challenge |
| MiniMax 文生图 API 文档（platform.minimaxi.com） | 同步接口、n=1..9、seed、style、prompt_optimizer、base_resp 错误约定、failed_count |
| MiniMax 视频生成指南 | 三种模式、12 文件上限、10 秒轮询建议、H3-Context-IR、视频再生成、`[运镜]` 指令 |
| MiniMax 按量计费定价页 | 全部成本数字与 768P→2K 成本中性的推导依据 |
| polybuzz.ai（含你提供的截图） | 结构化 prompt 槽位、预设卡片网格、资产化、会员配额 |
| jimeng.jianying.com（含你提供的截图） | 素材引用 DSL、参数条设计、积分预估、结果页二次加工 |
| 即梦 AI 全功能教程（CSDN） | 即梦六大模块顶部导航 + 左中右三栏工作台骨架，§19.4.1 布局依据 |
| 《从"聊天"到"画布"：即梦如何用工具化设计重新定义 AI 创作》（人人都是产品经理） | 参数公开+一键同款的社区飞轮、预测式交互主动推荐下一步，§19.0/§19.4.2 依据 |
| 剪映产品分析（知乎《剪映的新故事》） | 模块化交互、草稿自动保存、素材生态（黑罐头/素材包），§19.0④ 依据 |

**⚠️ 需要在开发前二次核实的点**（本文档基于博客文章推断，未经代码验证）：
1. `task` 模板绑定 executor 的确切字段名
2. `retry` / `timeout` / `resources` 的协议字段结构
3. `store.Store` 接口方法签名 → 决定 `aether_*` 表结构
4. `broker.TaskBroker` 接口方法签名 → 决定 asynq 实现方式
5. Loop 中如何访问 `item` 与上一轮 outputs 的变量路径
6. 依赖了 `Skipped` 节点的下游节点行为（继续还是跳过）

以上 6 项全部在 W0.5 的验证清单里。

---

*文档结束。Store/Broker 实现、PromptCompiler、预览门执行器这三块是集成的硬骨头，需要时可以单独拉出来写代码。*
