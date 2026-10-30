# WProxyman 迭代 TODO

> 状态图例：`[ ]` 未开始 · `[~]` 进行中 · `[x]` 已完成
> 每一项固定包含：**目标 / 现状证据 / 设计要点 / 子任务 / 验收标准 / 预估**
> 执行顺序：P0 → P1 → P2 → P3；同优先级内按「价值 ÷ 成本」排序。
> 约定：每项完成后补测试（Go 单测 + 前端 tsc），并更新 README/AGENT.md 中受影响的部分。

## 进度总览

| 优先级 | 项目 | 状态 | 预估 |
|---|---|---|---|
| P0 | 系统代理未恢复（关闭软件后断网） | `[x]` 已完成（v1.0.7） | 1 天 |
| P1 | 流式转发 + SSE / LLM 视图（OpenAI / Anthropic / 非标） | `[x]` 已完成（v1.1.0） | 6–8 天 |
| P1 | MCP Server：**通过 MCP 控制整个软件**（能力注册表 + 反射） | `[~]` 后端完成（见下），缺设置面板与文档 | 6–7 天 |
| P1 | **文档：重写 README（重点：怎么用 + 有什么功能）** | `[x]` 已完成 | 1 天 |
| P2 | 手机一键接入（二维码 / iOS 描述文件 / adb） | `[ ]` | 2 天 |
| P2 | WebSocket 双向：手动发帧 / 重放 / 二进制解析 | `[ ]` | 2–3 天 |
| P3 | GraphQL 专用视图（操作名聚合 + variables 重放） | `[ ]` | 3 天 |
| P3 | gRPC / Protobuf 解码（无 schema → proto → reflection） | `[ ]` | 8–10 天 |

---

## P0 · 系统代理未恢复：关闭软件后系统断网

**目标**：无论正常退出、面板停止、换端口还是崩溃强杀，系统代理都不会残留在"指向一个已经没人监听的端口"的状态。

### 现状与证据（已定位，共 4 条独立成因）

1. **主因：macOS / Linux 下 `Current()` 恒为空串**，导致 `shutdown()` 的守卫永远不成立，`Clear()` 一次都不会执行。
   - `internal/systemproxy/darwin.go` 末尾：`func (platformManager) Current() string { return "" }`
   - `internal/systemproxy/linux.go:92`：同上
   - `app.go:237-244`：`if cur == want || cur == "localhost:..." { Clear() }`
   - → 只有 Windows 的 `Current()` 读了注册表，所以这个 bug 在 macOS 上 100% 复现。
2. **面板"停止"不清系统代理**：`SettingsPanel.tsx` 的 handleStop 只调 `api.stopProxy()`；而 `StopProxy()`（app.go）只停服务器，不动系统代理 → 停止抓包后整机流量打到死端口。
3. **改端口后系统代理不跟随**：`SetSettings` 重启到新端口，但不重新 `Apply` → 系统代理仍指向旧端口。
4. **崩溃/强杀无自愈**：进程被 kill 时 `shutdown()` 不执行；下次启动若代理又启动失败（端口被占），旧的系统代理仍然残留，用户表现为"完全没网"。

### 设计要点

- **不再依赖 `Current()` 做判断**：新增持久化标记 `settings.SystemProxyApplied bool` + `settings.SystemProxyTarget string`（如 `127.0.0.1:10086`），作为"这是我们设的"的权威依据；跨平台一致。
- 让 `Current()` 在 macOS 上真正可用（解析 `networksetup -getwebproxy/-getsecurewebproxy` 各服务的 `Enabled/Server/Port`），Windows 的 `Current()` 也补上"端口必须匹配"的校验；作为第二道保险。
- **单一收敛点**：把「系统代理的开启/关闭」收敛到一个内部方法（如 `applySystemProxyForPort(port)`），任何端口变化/停止/启动都只走它，避免多处遗漏。
- **兜底清理**：`main.go` 捕获 SIGINT/SIGTERM 执行一次 best-effort 清理；启动时若 `SystemProxyApplied==true` 但代理启动失败 → 立即 `Clear()` 并在 UI 提示"已还原系统代理，因为代理端口被占用"。
- UI 增加显式的「立即还原系统代理」按钮 + 当前指向的目标展示（`系统代理：127.0.0.1:10086` / `已失效`）。

### 子任务

- [x] `systemproxy`：`Proxy` 增加目标标识；macOS 实现真正的 `Current()`；Windows `Current()` 校验端口；补 `systemproxy` 单测（可用假命令输出）
- [x] `Settings`：新增 `systemProxyApplied` / `systemProxyTarget` 字段（含向后兼容默认值）
- [x] `app.go`：抽出 `applySystemProxyForPort`；`StopProxy` / `startProxy` / `SetSettings` 端口变化时统一收敛处理
- [x] `shutdown()`：改为「标记为已应用 或 当前值匹配」即清理
- [x] `startup()`：自愈逻辑（代理启动失败 → 还原系统代理 + 提示）
- [x] `main.go`：SIGINT/SIGTERM 清理
- [x] 前端：设置面板加「还原系统代理」按钮与失效状态提示；停止抓包时提示系统代理已一并还原
- [x] 测试：用假 `Manager`（接口已存在，可注入）覆盖 4 条路径：正常退出 / 面板停止 / 换端口 / 启动自愈

### 实际实现（v1.0.7）

- **所有权判断改用持久化记录**：`settings.SystemProxyTarget`（我们最后写入的地址）+ `settings.SystemProxyEnabled`（用户意图）。`Current()` 退为兜底：能读到且与我们记录不符 → 认为已被别的程序接管，**不清**（不抢别人的代理）；读不到（Linux 部分桌面）→ 信任记录。
- **macOS `Current()` 真正实现**：解析 `networksetup -getwebproxy/-getsecurewebproxy` 的 `Enabled/Server/Port`，返回第一个启用服务的地址；`Status()` 从"只看第一个网络服务"改为"任一服务启用即为 true"。
- **Windows `Current()` 修正**：`ProxyEnable=0` 时返回空串（Clear 会保留 ProxyServer 值，旧实现会误报）；支持 `http=host:port;https=...` 分写格式。
- **Linux `Current()` 尽力实现**：GNOME（`mode=manual` + http/https 的 host/port）优先，其次 KDE `kioslverc`；读不到返回空串。
- **收敛点**：新增 `applySystemProxy` / `clearSystemProxy` / `systemProxyIsOurs` / `healStaleSystemProxy`；`SetSystemProxyEnabled`、`StopProxy`、`startProxy`、`startup`、`shutdown` 全部走它们。
- **停止抓包即还原**：`StopProxy` 会还原系统代理但保留用户意图，下次启动/恢复抓包自动重新接管（含端口变化后跟随新端口）。
- **崩溃自愈**：启动时若系统代理仍指向我们、而代理没能起来（端口被占用等）→ 立即还原，避免"开机就没网"。
- **信号兜底**：`main.go` 捕获 SIGINT/SIGTERM 调用 `Cleanup()`；清理用 `sync.Once` 保证只执行一次（信号与 Wails OnShutdown 可能都触发）。
- **UI**：显示"系统代理 → 127.0.0.1:10086"，并提供「还原」应急按钮；停止抓包时提示已还原。
- **测试**：新增 `app_sysproxy_test.go`（假 Manager 注入，9 个用例）+ `internal/systemproxy/systemproxy_test.go`（地址归一化）；另加 `main_test.go` 把用户配置目录重定向到临时目录，避免测试覆盖开发者真实的 settings.json。
- **变异测试验证**：把 `systemProxyIsOurs` 临时退化回"只凭 Current() 判断"的旧逻辑，核心回归用例立即 FAIL（`Clear called 0 times`），证明该测试确实守住这个 bug。

### 验收标准

- macOS 上：开抓包 → 直接关窗口 → `networksetup -getwebproxy Wi-Fi` 显示 `Enabled: No`；
- 面板点「停止」后系统代理立即还原，且不影响局域网设备抓包（AllowLAN 场景下手机仍可连）；
- 运行中改端口 → 系统代理自动指向新端口；
- `kill -9` 后重启应用且 10086 被占用 → 启动即自动还原系统代理并提示。

**预估**：1 天（含测试）。**风险**：macOS `networksetup` 需要管理员权限，`Apply/Clear` 的错误目前被吞掉，需一并改为回传并提示。

---

## P1 · 流式转发 + SSE / LLM 视图

**目标**：流式响应（SSE、LLM 逐 token、chunked）能实时透传并在界面上逐条还原；内置 OpenAI / Anthropic 兼容解析，且任何私有 SSE 协议都能通过自定义规则解析。

### 现状与证据（这是一个功能缺口，不只是"视图不好看"）

- 全仓库 grep `event-stream|Flush|flush` **零命中** → 目前完全没有流式处理。
- `internal/proxy/http_forward.go:174-205` `captureResponse` 用 `io.ReadAll(io.LimitReader(...))` **把整个响应体读完**，再由 `writeResponseFromFlow` 写给客户端。
- 后果：**所有流式接口在开启解密时都被缓冲到结束才返回**——LLM 逐 token 输出会一直卡住，长时间流会被 `MaxBodyBytes` 截断，无限流永不返回。这同时也是 #11（gRPC streaming）与 #13（WebSocket 之外的实时协议）的公共底座。

### 设计要点

**后端（流式转发）**
- 识别流式响应（`Content-Type: text/event-stream`、`Transfer-Encoding: chunked`、或 `application/grpc` 的 server-streaming），进入"边转发边采集"模式：
  - 一边按块 `Flush()` 给客户端（保持 `http.Flusher`），一边解析事件、更新 Flow、推送 `flow:sse` 事件给前端；
  - 保留上限保护：正文累积超过 `MaxBodyBytes` 时只停止**保存**，不中断**转发**（当前语义相反，会截断上游）。
- SSE 解析器：`event:` / `data:` / `id:` / `retry:` / 注释行(`:`)、多行 data 合并、`\n\n` 分帧、`[DONE]` 哨兵。
- 事件按序号 + 到达时间戳存储（`models.SSEMessage{Seq, Event, Data, AtMs, Size}`），Flow 增加 `IsStream bool` + `SSEMsgs []SSEMessage`。

**前端（视图）**
- `text/event-stream` 自动切到 "Stream" 标签页：时间线（序号 / 相对时间 / 间隔 / 大小 / event 名），可暂停自动滚动、可过滤事件类型。
- 双视图：**原始帧** ↔ **累积还原**（把增量拼成最终完整内容，OpenAI/Anthropic 直接显示成可读文本/JSON）。
- 会话级汇总：总时长、首字节时间(TTFB)、token 速率（由解析器给出）。

**OpenAI 兼容模式（预设解析器）**
- `chat.completions` 流：`choices[].delta.content` / `reasoning_content` / `tool_calls[]` 分片拼接 / `finish_reason`；`stream_options.include_usage` 的 usage 汇总；`[DONE]`。
- 新版 `responses` API 的语义事件：`response.output_text.delta`、`response.reasoning_summary_text.delta`、`response.completed` 等。
- 兼容任何走 OpenAI 协议的自建服务（vLLM / Ollama / one-api / DeepSeek / Qwen 等）——按报文结构嗅探，不按域名白名单。

**Anthropic 兼容模式（预设解析器）**
- `message_start` / `content_block_start` / `content_block_delta`（`text_delta`、`input_json_delta`、`thinking_delta`）/ `content_block_stop` / `message_delta` / `message_stop` / `ping` / `error`；把 `tool_use` 的 JSON 分片拼成完整入参。

**其它非标 SSE**
- 通用兜底：未识别协议也照常按"event 名 + 原始 data + 时间"渲染，不丢数据。
- **自定义解析规则**：用户可配 JSONPath（如 `$.choices[0].delta.content`）指定"增量字段"，从而把私有 SSE 也拼成累积文本；规则可保存复用（与后续 Rules 体系共用存储）。

### 子任务

- [ ] `models`：`SSEMessage` 结构 + Flow 字段（`isStream` / `sseMessages` / `firstByteAt`）
- [ ] `internal/proxy`：流式透传改造（`http_forward.go` / `handler.go`），含 Flush、截断语义修正
- [ ] `internal/proxy/sse.go`：SSE 解析器 + 单测（分帧、多行 data、注释、`[DONE]`、UTF-8 跨块边界）
- [ ] `internal/sse`：OpenAI / Anthropic 预设解析器 + 单测（用真实报文样本）
- [ ] 自定义 JSONPath 解析规则（配置存储 + 执行）
- [ ] 事件推送：`flow:sse` 增量事件（避免整条 flow 反复过桥）
- [ ] 前端：Stream 标签页、时间线、原始/累积双视图、TTFB 与速率
- [ ] 前端：自定义解析规则编辑器（放在 Settings 或 tool 页）
- [ ] 文档：README 增加"调试 LLM 应用"章节

### 验收标准

- 用 `curl` 打一个 SSE/LLM 流，客户端**逐条**收到（不被缓冲），界面同步逐条出现；
- OpenAI 与 Anthropic 两种流都能在"累积视图"中看到完整可读回复（含 tool_calls / tool_use 拼接正确）；
- 一个自造的私有 SSE 协议，通过自定义 JSONPath 规则也能拼出累积文本；
- 关闭"SSL 解密"时行为不变（隧道直通）。

**预估**：后端流式改造 3–4 天；OpenAI/Anthropic 解析 3 天；自定义规则 + 导出 1–2 天。**依赖**：无（建议在 P1 先做，因为它是 #11 streaming 的前置）。

### 进度（2026-10-30 起）

- [x] `models`：`SSEMessage` + Flow 字段（`isStream` / `streamKind` / `streamEnded` / `firstByteAt` / `sseMessages` / `streamTruncated`）
- [x] `internal/proxy`：流式透传（`http_forward.go` 的 `streamResponse`），读到即写 + Flush；`MaxBodyBytes` 只限制保存、不限制转发；RequestTimeout 拆成「等响应头」与「读正文」，流式不设总时长上限
- [x] `internal/proxy/sse.go`：增量 SSE / NDJSON 解析器（CRLF/CR/LF、多行 data、注释心跳、id/retry、`[DONE]`、跨块 UTF-8、超长行防御）
- [x] 事件推送：`Config.OnStream` → `flow:stream` 增量事件（按 16 条或 100ms 批量打包，不重推整条 flow）
- [x] 测试：31 个 Go 用例（7 个端到端流式，含 **MITM HTTPS 与 HTTP/2 下的逐块下发** + 24 个解析器） + 19 个前端单测（vitest，`make test` 一并执行）+ `flow:stream` 契约测试 —— commit `293c535` / `df1ec23`
- [x] OpenAI 兼容预设解析器（`chat.completions`：delta.content / reasoning_content / tool_calls 分片按 index 归并 / finish_reason / usage；`responses` API：语义事件）
- [x] Anthropic 兼容预设解析器（message_start / content_block_* / message_delta；text_delta / thinking_delta / input_json_delta 把 tool_use 参数拼回 JSON）
- [x] 自定义 JSONPath 解析规则（存 `settings.streamRules`，设置内可增删改；含 matchPath 与 stopValue）
- [x] 前端 Stream 视图：时间线（偏移/间隔/大小/心跳过滤）+ 累积还原（文本/思维链/工具调用/用量）+ 原始帧，含 TTFB 与速率
- [x] 截图验证（`verify/stream-*.png`）—— commit `7232c55`
- [x] README 章节「调 LLM / 流式接口」（随 README 重写一起完成，含 3 张界面截图）
- [ ] 随下一版发布（v1.1.0）

**实现上的取舍（记录下来免得以后重复讨论）**：解析放在前端而不是后端，原因是自定义规则要改完即生效、私有协议不该需要重新发版、原始事件本来就存在 flow/会话里可随时重新推导。识别方式是**按报文结构嗅探**而非域名白名单，因此 vLLM / Ollama / one-api 这类自建 OpenAI 兼容服务同样能识别。

---

## P1 · MCP Server：让 AI 控制整个软件

> **需求变更（2026-10-30）**：原计划"先做只读工具"作废 —— 用户要求**通过 MCP 控制整个软件**，
> 因此目标是把 App 的完整能力面（当前 44 个导出方法）暴露给 MCP 客户端。

**目标**：Claude / Cursor / 其他 MCP 客户端连上后，既能查询抓到的流量，也能像在界面上操作一样
控制整个应用：代理启停与端口、系统代理、SSL 解密规则、13 个工具的配置、会话与导出、证书管理、
Compose/Repeater、断点决策、流式解析规则……

### 设计要点

**1) 不手写工具，用「能力注册表 + 反射」自动生成（关键决定）**

手写 44 个工具必然与代码漂移（加一个 App 方法就漏一个）。做法：

- 用 `reflect` 遍历 `*App` 的导出方法，按注册表元数据生成 MCP 工具；
- 注册表只维护**元数据**（描述、分组、权限档、参数改名），签名与 JSON Schema 从反射推导 ——
  改了签名不会漂移；
- 结构体参数（`Settings` / `Flow` / `EngineConfig`）按 `encoding/json` 标签递归生成
  Schema（限深，允许 additionalProperties）；
- 工具名用 snake_case（`set_ssl_proxy_enabled`），并保留一个 `list_capabilities`
  方便客户端先问"你能做什么"。

**2) 少数方法需要"AI 友好"的包装，不能直接暴露**

| 原方法 | 问题 | 处理 |
|---|---|---|
| `GetFlows()` | 一次返回全部 flow（含正文）→ 上下文爆炸 | 换成 `list_flows`：过滤（host/method/status/时间/仅失败/仅流式）+ 分页 + 字段裁剪 |
| `GetFlow(id)` | 正文可能几十 MB | `get_flow` 支持 `bodyLimit`，超限给摘要 + 提示用 `get_flow_body` 分段取 |
| `GetCACertPEM()` | 一大段 PEM，对模型无意义 | 不暴露为工具，改为资源 `cert://ca` |
| `GetRequestBody` / `GetResponseBody` | base64 大块 | 包装成带 `offset/limit` 的 `get_flow_body` |
| `Cleanup()` | 生命周期内部方法 | **不暴露** |

**3) 权限分档（默认最小权限）**

| 档位 | 例子 | 默认 |
|---|---|---|
| `read` | list/get/search flows、get_stats、get_settings、get_tool_config、get_proxy_port | ✅ 开 |
| `write` | set_settings、set_ssl_proxy_enabled、apply_tool_config、start/stop_proxy、set_flow_pinned、resolve_breakpoint、send_request | ❌ 需显式开启 |
| `dangerous` | clear_flows、install/remove_certificate、set_upstream_insecure（关掉上游证书校验）、所有落盘/读文件方法（SaveSession/OpenSession/ExportHAR/ImportHAR/SaveFlowContent 等，路径任意） | ❌ 需显式开启 + 每次调用写入审计 |

- 只监听 `127.0.0.1`；token 随机生成、可在 UI 一键吊销/轮换；
- **审计日志**：时间 / 工具名 / 参数摘要 / 档位 / 结果，UI 可查，环形缓冲（上限 500 条）；
- 破坏性操作在返回里标注 `requires_confirmation`，避免模型"顺手清空"。

**4) 协议版本：必须做 dual-era（重要，已核对官方规范）**

MCP 在 `2026-07-28` 修订版改成了**无握手**：没有 `initialize` 会话，每个请求自带
`_meta.protocolVersion`（HTTP 上还有 `MCP-Protocol-Version` 头），版本不支持要回
`-32022 UnsupportedProtocolVersionError` 并带上 `data.supported` 列表；服务端**必须**实现
`server/discover`。`2025-11-25` 及更早是"legacy"代（`initialize` 握手 + 会话）。

结论：本实现做成 **dual-era** —— 带 `_meta` 的请求按现代语义无状态处理；收到 `initialize`
则按 legacy 语义服务该会话，两种客户端都能用。（来源：modelcontextprotocol.io 的
versioning 与 transports 规范。）

**5) 传输**

1. **内置本地 HTTP MCP（首选）**：app 内起 `127.0.0.1:<端口>`，`POST /mcp` 实现
   JSON-RPC 2.0（`initialize` / `tools/list` / `tools/call` / `resources/list` /
   `resources/read` / `prompts/list`），支持 `Mcp-Session-Id`；UI 显示 URL + token，
   客户端填 URL 即可用。
2. **stdio 桥（第二个入口）**：`wproxyman mcp` 子命令，把 stdio 上的 JSON-RPC 转发到上面
   的本地端点 —— 兼容 Claude Desktop 这类只支持 stdio 的客户端；app 没在跑时给出明确提示。

### 进度（2026-10-30，自动轮次结束时的状态）

**已完成并验证**（提交：`8ae9c63` `31984c6` `1dc8b8a` `d2d0253` `98a3024` `e8cd158` `f137056` `e990ac0` `e96c10d` `263e352` `e4b9523` `f7664e9`）：

- [x] 协议层 `internal/mcp/protocol.go`：JSON-RPC 2.0、错误码（含 `-32022`）、版本协商、`_meta` 解析
- [x] **dual-era**：现代（`_meta.protocolVersion` + `server/discover`，无握手）与 legacy（`initialize`）都能服务
- [x] 能力注册表 `registry.go`：元数据手写、签名与 JSON Schema 从反射推导；**生命周期方法硬性拒绝名单**（Cleanup/startup/shutdown/domReady）；handler 型自定义工具
- [x] `appspec.go`：**43 个工具覆盖整个 App**（read 19 / write 10 / dangerous 12），排除项（Cleanup、GetCACertPEM、GetFlows、以及 4 个 MCP 自管理方法）都有理由且被漂移守卫测试锁住
- [x] 包装工具：`list_flows`、`get_flow`、`get_flow_body`、`get_stats`、`list_capabilities`（`internal/flowquery` 提供过滤/分页/字段裁剪/统计，正文永不可请求）
- [x] 服务层 `service.go`：权限按调用现查、审计环形缓冲（只记参数指纹，不记内容）、`isError` 与 JSON-RPC 错误的区分
- [x] HTTP 传输 `http.go`：`POST /mcp`、Bearer/自定义头令牌（常数时间比较、空令牌拒绝一切）、`MCP-Protocol-Version` 头合并、1 MiB 上限、通知回 202
- [x] 装配 `app_mcp_server.go`：只绑 `127.0.0.1`、设置项（默认关闭）、令牌自动生成与轮换（动态读取，无需重启）、随应用起停
- [x] stdio 桥 `mcp_bridge.go`：`wproxyman mcp`，失败路径都有明确提示
- [x] 测试：`internal/mcp` 20 条（协议/注册表/handler/服务/HTTP）+ `internal/flowquery` 13 条 + `app_mcp*_test.go` 端到端若干；**连续三次全量 `go test ./...` 全绿**

**临时启用方式（设置面板做好之前）**：编辑 `settings.json`（Windows `%AppData%\wproxyman`，macOS `~/Library/Application Support/wproxyman`，Linux `~/.config/wproxyman`）：

```json
{ "mcpEnabled": true, "mcpPort": 10087, "mcpToken": "<自己填一个随机串>", "mcpAllowWrite": false, "mcpAllowDangerous": false }
```

重启应用后端点即 `http://127.0.0.1:10087/mcp`，用 `Authorization: Bearer <token>` 调用。

**剩余工作（按顺序）**：

- [ ] **设置面板 MCP 区块**（唯一的功能缺口）：
  1. `cd frontend && npx wails generate module`（或 `wails build`）重新生成绑定，让 `GetMCPStatus` / `SetMCPEnabled` / `RotateMCPToken` 出现在 `wailsjs/go/main/App.d.ts`；
  2. `services/api.ts` 加三个封装；
  3. `SettingsPanel.tsx` 在"流式解析规则"卡片后加一张卡片：启用开关、端口输入、URL 与令牌（带复制按钮）、轮换令牌、`allowWrite`/`allowDangerous` 两个开关（开启时给出醒目警告文案）、审计日志列表（时间/工具/档位/结果）；
  4. i18n 中英各补一组 `settings.mcp*` 键（注意 en/zh 键必须一一对应）；
  5. 用截图环境（`/tmp/shots/readme-shots.js` 那套 Chromium + Wails 桩）渲染验证，**至少确认两个档位开关的警告文案与令牌显示正确**。
- [ ] **README「用 AI 控制 WProxyman」章节**：Cursor / Claude Code（填 URL + 令牌头）与 Claude Desktop（`command: /path/to/WProxyman`、`args: ["mcp"]`）两种接入写法；权限三档说明；`list_capabilities` 用法；安全提示（只在回环、令牌可轮换、审计可查）；已知限制（不支持 SSE 推送、`prompts/list` 为空）。
- [ ] **发版**（v1.2.0）：MCP + README 一起。
- [ ] 可选增强：`resources/read` 真正返回 CA 证书内容；`prompts/list` 提供 `explain_flow` / `draft_rule` 提示模板；`search_flows`（现在 `list_flows` 的 `search` 参数已覆盖，故未单列）。

### 验收标准

- Claude/Cursor 配置后能问"刚才那个 500 的请求什么原因"，AI 通过 `list_flows` + `get_flow` 回答；
- 未开写档时任何写操作被拒且给出可操作提示；开启后 AI 能新增一条 Map Local 规则且 UI 立即反映；
- AI 能完成"改端口 → 重启代理 → 验证系统代理指向新端口"的完整操作链；
- 审计日志能看到每一次调用；`list_capabilities` 与 App 的导出方法集合一致（有测试守着）。

**预估**：协议层 + 注册表 2 天；包装与查询层重构 1.5 天；UI + 审计 1 天；stdio 桥 0.5 天；测试与文档 1 天。

---

## P1 · 文档：重写 README（重点：怎么用 + 有什么功能）

> **决策记录（2026-10-30）**：与下一个版本**同时发布**。要求以"怎么用"为主线，而不是功能罗列。

**目标**：README 面向第一次拿到这个工具的人 —— 30 秒能跑起来、10 分钟能上手抓并修改一条 HTTPS 请求、遇到问题能在 FAQ 里找到答案。功能清单只写**确实已经能用**的东西。

**现状与问题**（现有 README 125 行，偏"功能罗列"，缺"怎么用"）：

- `README.md:110` 还留着占位符 `http://电脑IP:代理端口/wpx-cert.pem`，与第 106 行"端口固定 10086"自相矛盾；
- 缺**平台要求与安装细节**：Windows 的 WebView2、macOS 最低版本、Linux 三件套（deb / rpm / tar.gz）各自怎么装、需要哪些系统库（GTK/WebKitGTK）；
- 缺**端到端示例**：从启动 → 装证书 → 抓到第一条流量 → 改一条响应（Map Local）→ 导出，没有一条完整链路；
- 缺 **FAQ**：证书不生效、手机连不上、退出后没网（系统代理残留）、端口被占用、HTTP/3 抓不到、防火墙授权；
- 缺**截图**（现为纯文字）；本项目已有可复用截图环境（Chromium + Wails 桩，见 `verify/` 与 `/tmp/shots/shot.js`）；
- 未反映 v1.0.7 的新行为：**停止抓包 / 退出应用会自动还原系统代理**；
- ⚠️ 需注意：侧栏的"设备 / 已保存"目前点击后并不真正过滤（`filterFlows` 无对应分支），README **不要**把这些写成已实现能力，避免又一处"UI 承诺了但没实现"。

**结构建议**：

1. 一句话定位 + 平台 + 首屏截图；
2. **30 秒跑起来**：下载 → 启动（自动开启抓包并接管系统代理）→ 刷新浏览器 → 看到第一条流量；
3. **怎么用（主线）**：抓 HTTPS（装 CA）→ 看内容 → 改内容（Map Local / 断点 / 脚本）→ 重放（Repeater / Compose）→ 导出（cURL / Node-fetch / Postman / HAR / 会话）；
4. **手机抓包**：扫码/手填两条路径，iOS "完全信任"那一步单独强调；
5. **功能清单**：按"抓取 / 查看 / 修改 / 自动化 / 协作"分组，每条一句话；
6. **常见问题 FAQ**；
7. **安装与平台要求**（Windows / macOS / Linux 分别写）；
8. **从源码构建** + 已知限制（未公证、HTTP/3、SOCKS5 未支持、Diff 尚为占位）。

**完成情况（2026-10-30）**：README 已重写（8 节：30 秒跑起来 / 怎么用主线 / 调 LLM 流式 / 手机抓包 /
功能一览 / 设置说明 / 常见问题 FAQ / 安装与平台要求 / 已知限制），并附 5 张真实界面截图（`docs/images/`）。

**子任务**：

- [x] 逐条核对功能描述与代码实现一致（Diff 占位、SOCKS5 未支持、侧栏设备/已保存未生效均已写进"已知限制"）
- [x] 用截图环境生成 5 张界面图（主界面 / 流式累积 / 流式时间线 / 设置 / 设备），放 `docs/images/`
- [x] 占位符已修正；macOS 首次打开引导按版本区分（15+ 走 系统设置 → 隐私与安全性 → 仍要打开）
- [x] FAQ 共 6 条（证书、手机连不上、退出后没网、端口被占用、HTTP/3、LLM 流还原为空）
- [x] 补平台要求与三平台安装方式（含 deb/rpm/tar.gz 命令）
- [x] 补系统代理自动还原的说明（设置项说明 + FAQ）
- [x] 已补「调 LLM / 流式接口」章节（MCP 尚未实现，不写）

**验收标准**：没用过该工具的人只看 README，能在 5 分钟内完成"安装 → 装证书 → 抓到并修改一条 HTTPS 请求"；FAQ 覆盖 6 类常见故障；所有功能描述与代码一致。

**依赖**：与 P1 功能同版本发布；截图环境已就绪。

---

## P2 · 手机一键接入

**目标**：手机从"手抄 IP + 端口 + 手动装证书"变成"扫一个码就完成代理配置 + 证书安装"。

### 现状与证据

- Devices 区只列出裸 IP（`frontend/src/components/SettingsPanel.tsx`），证书地址是纯文本 URL；
- 证书下载端点已存在（`/wpx-cert.pem` 与安装引导页 `/wpx-cert`），可以直接复用；
- 端口已在上一轮固定为 10086，具备了"配置一次长期有效"的前提。

### 设计要点

- **二维码 3 张**（可切换）：① 代理地址 `IP:10086`；② 证书下载 `http://IP:10086/wpx-cert.pem`；③ iOS 描述文件 `http://IP:10086/wpx-mobileconfig`。前端用 `qrcode.react`（纯 JS，渲染 SVG 可缩放）。
- **iOS `.mobileconfig`**（后端动态生成，XML plist）：一个描述文件里同时放 ① CA 根证书载荷（`com.apple.security.root`）② 代理载荷（`com.apple.SystemConfiguration` 的 `Proxies`：`HTTPEnable/HTTPProxy/HTTPPort` + `HTTPSEnable/...`）。装完即"代理 + 证书"一步到位；安装后仍需在"证书信任设置"里开启完全信任（页面明确提示）。
- **Android**：给一键复制的 `adb shell settings put global http_proxy IP:10086`（含还原命令 `:0`）；证书走现有下载页。
- **连通性自检**：Devices 区加"测试连接"按钮——从本机发起一次经 `IP:10086` 的请求，确认 AllowLAN 生效、防火墙已放行（首次连接常被系统防火墙拦）。
- 记住上次选中的二维码，复制按钮带成功反馈。

### 子任务

- [ ] 后端：`/wpx-mobileconfig` 动态生成（含 LAN IP 探测 + CA 证书 base64），与 `/wpx-cert` 页互链
- [ ] 后端：`GetLANIPs` 补"推荐地址"排序（优先 Wi-Fi/活动网卡）
- [ ] 前端：`qrcode.react` 依赖 + Devices 区二维码切换与复制
- [ ] 前端：adb 命令一键复制（配置/还原两条）
- [ ] 前端：连通性测试按钮（后端新增 `TestLANConnectivity`）
- [ ] i18n：中英文案
- [ ] 文档：README 手机章节改写成"扫码三步走"

### 验收标准

- iPhone 扫描述文件二维码 → 安装 → 打开 Safari 抓 HTTPS 成功（仅"完全信任"需手动一次）；
- Android 复制 adb 命令执行后，模拟器代理生效；
- 未开 AllowLAN 时，测试连接给出明确提示（而不是静默失败）。

**预估**：2 天。**依赖**：无。

---

## P2 · WebSocket 双向

**目标**：不只是看 WebSocket 消息，还能手动发帧、改后重发、重放历史帧，并能读懂二进制帧。

### 现状与证据

- `models.WSMessage{Direction, Opcode, Data, Timestamp}` 已在采集；
- 前端 `DetailPanel.tsx:429` 仅只读展示消息列表；
- 后端无"向已建立连接发送帧"的接口。

### 设计要点

- 后端保留存活 WebSocket 连接的写入口：`SendWSFrame(flowID string, data []byte, isBinary bool)`；连接已关闭时返回明确错误；
- 帧能力：文本/二进制发送、编辑后重发、按序号重放、批量重放（带间隔）；
- 视图增强：按方向/opcode 过滤、JSON 自动美化、ping/pong 与 close 帧单列、帧大小与间隔、导出为 JSON/CSV；
- 二进制帧：JSON 尝试美化；Protobuf 走 #11 的解码器（先做十六进制 + 字符串可见部分）。
- 安全：发送前二次确认（会立刻影响对端），并在 UI 标注"帧已注入"。

### 子任务

- [ ] 后端：连接注册表（flowID → ws conn）+ `SendWSFrame` API + 单测
- [ ] 后端：帧序号与方向修正（确保 seq 连续可用）
- [ ] 前端：帧详情编辑框（Monaco）+ 发送/重发/批量重放
- [ ] 前端：过滤、美化、导出
- [ ] 文档：README/AGENT 记录"注入帧"的能力与风险

### 验收标准

- 对一个公开 WebSocket echo 服务：连上后能从界面手动发一条 JSON 并收到回显；
- 连接关闭后发送按钮禁用并提示原因；
- 二进制帧以十六进制 + 可打印字符串展示。

**预估**：2–3 天。

---

## P3 · GraphQL 专用视图

**目标**：GraphQL 请求按"操作名"可读、可搜索、可重放。

### 现状与证据

- `frontend/src/types.ts` 中仅把 `application/graphql` 归为文本类型用于展示；
- 后端无任何 query/variables/operationName 解析。

### 设计要点

- 嗅探式识别（不能只靠 content-type）：POST + JSON body 含 `query` 字段、GET + `?query=`、以及 `application/graphql`；
- 提取 `operationName` / `operationType`（query|mutation|subscription）/ `variables`，写入 Flow 新字段；
- 列表：新增"操作名"列与按操作名聚合的侧栏分组（与 Domains 平级）；
- 详情："GraphQL" 标签页：query 高亮（Monaco graphql 语法）、variables 表单可编辑、一键送 Compose/Repeater 重放；
- 错误高亮：`errors[].path` / `errors[].message` 定位到对应字段；批处理（数组 body）逐条展开。

### 子任务

- [ ] 后端：解析器 + Flow 字段（`graphqlOperation` / `graphqlType` / `graphqlVariables`）
- [ ] 前端：列表列 + 侧栏分组
- [ ] 前端：GraphQL 标签页 + variables 重放（复用 Compose 通路）
- [ ] 测试：解析器单测（POST/GET/批处理/无 operationName 的匿名查询）

**验收标准**：任意 GraphQL 接口的请求能在列表中直接看到操作名，并能在详情页改 variables 后重放。

**预估**：3 天。

---

## P3 · gRPC / Protobuf 解码

**目标**：gRPC 流量可读 —— 拆帧、看状态、并在有 schema 时显示字段名。

### 现状与证据

- HTTP/2 已支持（`internal/proxy/h2_test.go`），但 body 按二进制处理；
- 无 gRPC 帧拆分、无 protobuf 解析、无 `.proto` 导入入口。

### 设计要点（分三步走，每步独立可用）

1. **无 schema 结构化**（先做，收益最直接）：按 gRPC 帧格式（1 字节压缩标志 + 4 字节长度）拆出多条 message，按 protobuf wire format 递归解出字段号 + wire type + 原始值（长度分隔字段可见可打印字符串）。没有 `.proto` 也能看出结构，比一堆乱码强得多。
2. **导入 `.proto`**：用户导入 proto 文件（或目录），动态编译 descriptor（`protocompile` / `jhump/protoreflect`），按方法名匹配 message 类型，显示真实字段名与嵌套结构。
3. **Server Reflection**：对目标服务发起 reflection 请求自动拉取 descriptor（很多服务默认开启）。

配套：
- Method 解析：`:path` = `/package.Service/Method`，展示服务/方法名与 streaming 类型（unary / client / server / bidi）；
- trailer 与状态：`grpc-status` / `grpc-message` / `grpc-encoding`（含 trailer-only 响应）、`grpc-timeout`；
- 与 #14 联动：server-streaming 走流式透传 + 逐条 message 展示；
- 与 #11 复用：同一个解码器给 WebSocket 二进制帧使用。

### 子任务

- [ ] `internal/grpc`：帧拆分 + wire-format 递归解码 + 单测（含压缩标志、跨块帧）
- [ ] Method/状态/trailer 解析（含 HTTP/2 trailer 获取）
- [ ] 详情页 "gRPC" 标签页（多 message、树形展示）
- [ ] `.proto` 导入与 descriptor 编译 + 持久化
- [ ] Reflection 客户端（超时与失败降级）
- [ ] 流式联动（依赖 P1 流式改造）

### 验收标准

- 对任意 gRPC 服务导出接口：unary 与 server-streaming 都能逐条 message 展示，状态码/错误信息正确；
- 导入 `.proto` 后字段名正确显示；
- 未导入 proto 时退化为"字段号 + 值"的可读结构，不报错。

**预估**：无 schema 解码 3–4 天；proto 导入 4–5 天；reflection 2 天。**依赖**：P1 的流式透传（server-streaming 场景）。

---

## 跨项公共依赖（先做能省重复工）

1. **流式透传底座**（P1）：被 gRPC server-streaming、SSE、WebSocket 之外的长连接共用。
2. **面向 AI/前端的查询层**（P1-MCP）：过滤 / 分页 / 字段裁剪，替换现在"一次返回全部 flow"的 `GetFlows`，前端大列表也同时受益。
3. **二进制解码器**（P3-gRPC）：gRPC 与 WebSocket 二进制帧共用。
4. **规则存储统一**（P1-SSE 自定义规则 与 现有 Rules/Map Local）：避免各写一套配置结构。

## 后续候选（本轮不排期）

- **"Latest build" 预发布里混入了一个多余的裸二进制**：Linux 构建 job 会把中间产物 `build/bin/WProxyman`（22MB、无扩展名）上传成 artifact 供打包用，而 publish job 用 `find artifacts -type f -exec cp {} release/` 无差别收集，于是它也被挂到了 `continuous` 预发布上（正式 Release 不受影响）。修法是在 assemble 阶段排除它，一行改动。（2026-10-30 发现，待确认是否修）
- 侧栏 `Devices`（本地/远程设备）与 `Saved` 三个条目目前在 `filterFlows` 里没有对应分支，点击等于不筛选；`Favorites` 与 `Pinned` 语义重叠。属于"UI 已承诺、逻辑未实现"，建议在 P2 手机接入时一并收敛（设备维度正好需要它）。
