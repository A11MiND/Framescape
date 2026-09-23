# 四格漫画编辑器

在现有平台内新增 `/comics`，创作台的四格漫画入口进入这里。保留旧版 MiniMax／Gemini 链路及历史任务；工作台上方可返回旧版。视频、单图及其他工作流不切换提供商。

## 已实现

- 原提示词直出：一页一次 OpenAI Image API 调用（无参考图走 `/v1/images/generations`，有参考图走 `/v1/images/edits`，参考图由 worker 下载后以 multipart 上传，OpenAI 不需要能访问我们的存储），不经过 MiniMax 规划器、H3、参考图风格化或逐格链式生成。没有背景摘录／参考用途说明时，提示词原样发送。
- 可编辑模式：同样整页生成，附加固定的等分 2×2、无文字／无气泡／无 Logo 绘图约束。对白和品牌原图独立叠加。
- 最多 15 张人物／画风参考图（PNG／JPEG／WebP，单张 ≤ 20 MB），可从素材库选择或直接上传，每张可写用途说明。单格重画附加原整页作为额外参考（合计不超过 OpenAI 的 16 张上限），仅替换选中格；旧底图、其他格、对白及 Logo 保留。生成模型仍可能不严格遵循格线或人物要求，需要用户检查。
- 图层移动、缩放、方向键微调、字号／颜色／气泡尾巴、锁定、置顶、复制、删除、撤销／重做。
- 原生中文文本框，随应用打包 Noto Sans TC；预览与导出共用 Canvas 渲染器。文本溢出阻止导出，Logo 等比缩放而非生成重画。
- 编辑稿保存到账号，版本号防止跨窗口覆盖；按用户隔离的本机自动备份，JSON 备份／导入及另存副本。
- 导出 1536×1024 PNG。生成原图和可编辑图层分开保存，不把生成图中的文字伪装成可编辑文本。
- PDF 文本层、TXT、Markdown 导入；20 MB、PDF 最多 300 页、背景最多 200,000 Unicode 字符（不是 tokens）。扫描件提示先做 OCR。
- 本地关键词检索产生带原文字符位置的摘录，用户可检查和修改；PDF 原文包含页码标记。绘图接口只接收已选摘录（最多 8,000 字符），不是整本原文。没有自动语义摘要、事实核验或自动故事规划。

## 灰度开放

AI 生成按账号灰度开放（迁移 `00022_user_comic_ai.sql` 新增 `users.comic_ai_enabled`，默认关闭）。管理员后台「人员管理」每行有「开放／关闭 AI 漫画」按钮；管理员账号始终可用。未开通的账号在创作台点「四格漫画」仍进入旧版 MiniMax／Gemini 表单；直接打开 `/comics` 可以导入底图、编辑对白和导出，但生成按钮不可用。后端在 `POST /jobs` 强制检查，未开通返回 403 `comic_ai_not_enabled`，不会预留积分。

## 启用

1. 部署前按现有流程运行 `go run ./cmd/migrate`，新增迁移为 `00021_comic_documents.sql` 与 `00022_user_comic_ai.sql`。本次开发只迁移了隔离测试数据库，没有迁移线上数据库。
2. 在 API 和 worker 的服务端环境配置 `OPENAI_API_KEY`。不要设置成 `VITE_*`，不要提交到 Git。scheduler 仅注册执行器结构，不持有密钥也可以工作。
3. `OPENAI_IMAGE_MODEL` 默认 `gpt-image-2.5-flare`（OpenAI 原生模型名，不带 `openai/` 前缀）。执行器内置了 `gpt-image-2.5-flare`／`gpt-image-2.5-sunburst`（及日期快照）的 token 单价；换成没有内置单价的模型时功能自动视为未配置，不会产生无法结算的调用。新增模型需在 `internal/infra/executor/openai/image.go` 的 `prices` 表补单价。
4. API、scheduler、worker 使用一致的 `OPENAI_USD_TO_CNY` 与 `OPENAI_IMAGE_RESERVE_USD`。默认分别为 7 和 0.50；前者是显式账务换算参数，不是实时汇率，后者是每次调用的预留额度（未用部分在任务结束时退回），不是供应商硬费用上限。每张参考图另加 `OPENAI_IMAGE_RESERVE_PER_REF_USD`（默认 0.03）。
5. 运行 `npm ci`、`npm run build`，并重启 API、scheduler、worker。图像存储必须允许浏览器的 CORS 图片读取，否则预览／PNG 导出会提示错误。worker 需要能下载参考图的公共 URL。

固定请求：`n=1`、`quality=high`、`size=1536x1024`、`output_format=png`、`background=opaque`，并附带哈希后的用户标识（`user`）供 OpenAI 做滥用归因。没有自动付费重试；HTTP 超时提示先核对 OpenAI 用量记录。OpenAI 审核拒绝（`moderation_blocked`）以 `sensitive_content:` 前缀报错，会写入现有审核记录表。

同一用户／幂等键在 MySQL 连接级锁内串行提交，并在锁内重查任务，避免并发重复请求在写入唯一键之前就启动多次付费工作流。浏览器保留未确认请求的标识，刷新后相同请求可重用；确认成功后清除。该保护要求客户端提供幂等键，新编辑器自动提供。

OpenAI 只返回 token 用量，不返回金额。执行器按内置单价计算美元成本（输入文字 $5、输入图片 $8、输出 $30，每百万 token；输出全部按图片输出单价计，偏向平台一侧），再按配置换算 `cost-yuan` 进入现有积分结算。实测一次无参考图的 high 整页约 $0.045。已知收费但存储失败也结算该次费用。若返回图片但缺少用量，按该次预留额度（含参考图部分）结算并标记 `usage-known=false`，不会变成免费调用。

## 接口与数据

- `POST /api/v1/jobs`：工作流仍为 `image.comic4`；新增 `comic_mode: direct|editable`、`comic_panel: 0..4`、`comic_context`，并指定 `image_provider: openai`。
- `GET /api/v1/capabilities`：增加漫画配置状态与输入限制，不返回密钥。
- `GET/POST /api/v1/comics`：列表／创建编辑稿。
- `GET/PATCH /api/v1/comics/:bizID`：读取／版本化保存，PATCH 必须提供读取时的 `version`。
- `comic_documents.document`：版本化 JSON，含背景、简报、参考图片 ID、底图与替换格 ID、图层几何／文字／样式。所有素材和待完成任务必须属于当前用户，禁止任意外部 URL 注入编辑稿。

图片生成指令与资料原文分离；摘录被标记为不可信数据。上传文件不会执行其中的指令。资料检索不等于内容事实核验。

## 测试

新增可复现的隔离基础设施：

```sh
docker compose -f deploy/docker-compose.test.yml up -d --wait
export MYSQL_DSN='root:test-only@tcp(127.0.0.1:13316)/aigc?parseTime=true&loc=UTC&charset=utf8mb4'
export REDIS_ADDR=127.0.0.1:16386
export MINIO_ENDPOINT=127.0.0.1:19000
export MINIO_PUBLIC_BASE_URL=http://127.0.0.1:19000/aigc-assets
go run ./cmd/migrate
go build ./...
go vet ./...
GIN_MODE=release go test -race -count=1 ./...
```

前端：

```sh
cd web
npm ci
npx playwright install chromium
npm test
npm run test:e2e
npm run build
npm run lint
```

2026-09-20 验证结果：后端全量竞态检测 310 个测试／子测试通过；1 个旧管理员测试因套件中的其他管理员记录跳过，隔离运行也通过。7 个前端单元测试、12 个 Chromium 端到端测试通过。覆盖并发幂等提交、200k 原文、PDF 页码、扫描件提示、中文输入事件、拖动缩放、Logo 分层、保存恢复、版本冲突、单格替换、旧任务再生成路由、PNG 真实文件尺寸、文本溢出、375/768/1440px 与浅色布局。构建／静态检查通过；前端保留原有 3 条 lint warning 和主 bundle 大小 warning。

所有自动化生成测试使用模拟提供商响应，未额外调用付费图像接口。没有声称已经验证线上密钥、参考图生成品质、真实操作系统输入法的所有行为、Safari/Firefox 或扫描件 OCR。

清理隔离环境（只删除本文件创建的测试容器／临时数据）：

```sh
docker compose -f deploy/docker-compose.test.yml down
```

## 后续边界

本版只做四格。多页小册子、自动 OCR、表格／图片内容理解、自动故事规划与事实引用审核、可编辑 PPTX 导出和团队协作仍未实现。复杂中文排版目前按字符换行，不是完整出版社级禁则排版。模型可能生成意外文字或不等分格线，用户需要重画或导入合适底图；软件不会把这些品质问题标成保证解决。

接口依据：[OpenAI Image Generation](https://developers.openai.com/api/docs/guides/image-generation)、[Create image edit](https://developers.openai.com/api/reference/python/resources/images/methods/edit)、[OpenAI Pricing](https://developers.openai.com/api/docs/pricing)。2026-09-23 起由 OpenRouter 改为 OpenAI 原生接口。
