# 生成能力与供应商适配

本包只负责能力目录、参数校验、供应商 HTTP 契约与生成结果导入。它不修改时间线；运行时凭据仅通过 SecretSource 注入。本次开发与验证未读取用户真实密钥、未自动发起付费测试。所有真实账号、地区、配额和生成质量仍未验证；HTTP fixture 通过不等于模型已完成账号接入。

## 当前状态

| 家族 | 具体适配 | 状态与范围 |
| --- | --- | --- |
| OpenAI GPT Image | GPT Image 2.5 Sunburst/Flare 及其已列出的快照 | 文生图、JSON 编辑图像、b64_json 结果；当前公开有限尺寸组合、PNG/JPEG 输出和本地 PNG/JPEG 参考 |
| Google Nano Banana | `gemini-2.5-flash-image` 的 generateContent | 文生图、最多 3 张图片参考编辑；不擅自将此契约套用到其他 Nano Banana 版本或 Interactions API |
| MiniMax H3 | H3、Highspeed、Lite、Max 的已核对参数差异 | 真实异步创建/查询骨架；本轮开放文字、首尾帧和图片参考，未开放视频/音频参考 |
| Seedance 2.5 | 火山方舟北京区标准 API，`doubao-seedance-2-5-260628` | 文字、首帧/首尾帧及1–30张PNG/JPEG参考图；同步声音开关、4–30秒、480p/720p/1080p、MP4；提交/查询/原ID恢复与下载通过本地夹具，账户按用户指令后置 |

取消接口默认不可用。停止本地等待不等于取消远端计费任务；`Cancel` 返回原任务和明确的未支持错误。

## 应用层接口

- `NewOpenAI/NewGemini/NewMiniMax/NewSeedance(Config)` 创建对应适配器。
- `Provider` 提供 `Capabilities`、`Validate`、`Submit`、`Get`、`Cancel`。
- `NewRegistry(providers...)` 提供可扩展目录、查询与参数校验。
- `SecretSource.Secret(ctx, providerID)` 仅在发起请求时取凭据，不进入 Request、Recipe 或项目文件。
- `ReferenceSource.Read(ctx, Reference)` 根据已登记 asset ID 读取素材；`FileReferences` 实现受限本地 PNG/JPEG 读取、格式/尺寸/指纹和文件替换检查。
- `NewResultImporter(ImportOptions).Import(ctx, task)` 将成功结果与配方作为一组发布，返回 `ImportedAsset`。随后由应用媒体探测模块验证媒体元信息，再经统一工程命令添加到素材库或时间线。

`Request.ID` 应由上层持久任务服务提前分配并保存。适配器不替代持久任务/幂等数据库；上层不能因为调用超时而再次调用 Submit。请求配方记录精确模型、参数、源素材指纹和父配方，不保存用户密钥、源文件路径或临时下载 URL。

## 提交与恢复

没有自动 POST 重试。连接中断、服务器故障、非预期重定向或不可解析成功响应会返回 `SubmissionUncertain`，上层应保留原尝试。H3 返回远端 ID 后可以 `Get` 重查；查询错误不自动把远端任务改为失败，也不会重新生成。

同步图像没有本包已确认的远端轮询任务。应用应及时导入成功返回的图像，再把完整业务任务记为完成；仅有 `Task.Status=succeeded` 表示供应商返回成功，不表示文件已经落盘。同步未知提交需检查供应商记录，不能伪造通过 Get 恢复或再次扣费。

下载失败可对同一任务重新 Get 获取新下载地址再 Import。已导入的同一配方按文件指纹复用；不同配方、缺失或被篡改结果不会静默覆盖。

## 安全与限制

- 不接受任意用户输入 URL 作为云端参考。PNG/JPEG 字节来自授权文件集合，编码为 data URL/inlineData；本机回环素材 URL 从不发送到云端。
- 正式 API 地址为非本地 HTTPS，鉴权放请求头，禁止自动跟随生成请求重定向。服务响应正文和底层凭据错误不直接出现在公共错误里。
- 输入图小于 50MB，总参考数据另有客户端限制；H3 图片还核对 256–5760 像素、宽高比与 30MB 限制，首尾帧与图片参考互斥。
- Google legacy generateContent 的内联 JSON 请求按 20MB 限制；H3 按 64MB 请求体限制；大参考文件需后续独立 Files API/上传机制，不用 localhost URL 顶替。
- 下载器不用生成请求鉴权或 Cookie，限制类型/大小、重定向、公网地址和 DNS；标准 Transport 固定已验证的公网 IP。默认不使用环境代理，避免未经检查的跳转路径。
- `AllowLocalTestHTTP` 仅为显式回环 fixture/开发配置，标记不进入 JSON，不能从普通工程文件恢复。非标准 Transport 仅允许显式测试模式；正式下载保留安全 Dial。
- 每个结果目录包含 `recipe.json` 与派生媒体，文件名由安全 ID/序号生成。PNG/JPEG 检查解码；WebP/MP4 在本包只检查容器类型，仍需媒体模块实际探测，不能把它当作完整编解码验收。

## 官方依据

- [OpenAI 图像生成](https://developers.openai.com/api/docs/guides/image-generation)
- [OpenAI 创建图像](https://developers.openai.com/api/reference/resources/images/methods/generate)
- [OpenAI 编辑图像](https://developers.openai.com/api/reference/resources/images/methods/edit)
- [Nano Banana 具体模型](https://ai.google.dev/gemini-api/docs/models/gemini-2.5-flash-image)
- [Gemini generateContent](https://ai.google.dev/api/generate-content)
- [Gemini legacy 内联图像限制](https://ai.google.dev/gemini-api/docs/generate-content/image-understanding)
- [H3 创建](https://platform.minimax.io/docs/api-reference/video-generation-v2-create)
- [H3 查询](https://platform.minimax.io/docs/api-reference/video-generation-v2-query)
- [Seedance 创建文档入口](https://docs.byteplus.com/en/docs/modelark/create-video-generation-task-api)：本轮读取到的 JS 壳不足以证明完整 2.5 调用契约，保留未验证状态。

## 实际验证范围

本地 HTTP fixture 核查请求路径、头和 JSON，使用测试图像与虚构密钥。覆盖两家图像、H3 提交/轮询、互斥条件、未知提交、错误脱敏、凭据不随下载传播、受限参考、符号链接变化、私网/重定向限制、下载重试、配方/文件篡改及结果原子发布。没有上传用户素材或调用真实生成。

`go test -count=1 ./internal/generation`

`go vet ./internal/generation`

2026-10-26：Seedance只固定已核对渠道，不能使用BytePlus/代理/Agent Plan凭据混调。声音参数进入请求签名与不可变配方，未声明该能力的模型拒绝声音参数。当前不开放MOV、视频/音频参考、编辑/延长或远端取消。详见[规格](../../docs/specs/2026-10-26-Seedance视频生成适配.md)。
