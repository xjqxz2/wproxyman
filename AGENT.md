# WProxyman — Agent Knowledge Base

跨平台（Windows/macOS/Linux）HTTP/HTTPS 抓包调试代理，界面参考 Proxyman。
**纯 Go 自研 MITM 代理内核**（零第三方代理库）+ Wails v2 桌面壳 + React 19/TypeScript 前端，单文件分发。
注释为中文；代码全中文注释是既有约定，新代码沿用。

## 架构全景

```
frontend (React 19 + Vite + zustand + @tanstack/react-virtual + Monaco)
   │  Wails 绑定 RPC（api.ts 封装）+ 事件总线（Events）
app*.go 服务层 — App 是唯一 Wails 绑定对象，exported 方法自动变 RPC
   ├─ app.go            代理启停/SSL/证书/系统代理/事件泵/flow 索引
   ├─ app_settings.go   设置持久化 settings.json
   ├─ app_flows.go      流量查询/清空/删除/置顶
   ├─ app_tools.go      断点恢复、Compose/Repeater 重放
   ├─ app_sessions.go   会话(.wpx)/HAR/cURL 导入导出
   ├─ app_codegen.go    cURL/Node-fetch/Postman 代码生成
   └─ app_download.go   素材 Save-to-file
internal/
   ├─ proxy/      自研代理引擎（HTTP/1.1 + h2 + WebSocket + 原始隧道）
   ├─ cert/       本地 CA、按域名动态签发、三平台信任库
   ├─ tools/      工具管线引擎（Map Local/Remote、断点、goja 脚本、限速…）
   ├─ storage/    会话持久化 + HAR 1.2 导入导出
   ├─ systemproxy/ 系统代理（注册表/networksetup/gsettings）
   ├─ codegen/    cURL/Node-fetch/Postman 生成 + cURL→Flow 解析
   └─ models/     Flow 数据模型（前后端共享的契约核心）
```

## 核心数据流

```
客户端请求 → handler.ServeHTTP
  ├─ CONNECT → handleConnect：SSLProxyEnabled(host)? → mitmConnect（ALPN 协商 h2/http1.1）
  │                        └─ 否 → 原始隧道（不记录，流量不中断）
  └─ HTTP → handleHTTP：构造 Flow → 工具管线 OnRequest → 转发上游
        → captureResponse（超限溢出临时文件）→ OnResponse 管线 → 写回客户端
```

- **工具管线固定顺序**（internal/tools/pipeline.go）：`AllowList → 断点(request) → 脚本 → BlockList → MapLocal → MapRemote → Rules → NoCaching`；响应阶段 `Rules → NoCaching → 脚本 → 断点`。`InterceptDecision{ShortCircuit/Wait/UpstreamURL/SkipCapture}` 四类决策。
- **MITM 决策回调**（app.go startProxy）：`SSLProxyEnabled(host)` 仅当 CA 已被客户端信任才解密；否则降级隧道。CA 不可用时 mitmConnect 重新走 handleConnect（隧道兜底）。
- **事件推送**：proxy `OnFlow(f, phase)` → App `emit`（非阻塞 512 队列）→ 单泵 goroutine → `wruntime.EventsEmit`。事件名集中在 `frontend/src/services/api.ts` 的 `Events` 表（flow:new/updated/completed/paused/resumed/deleted、flows:cleared/replaced/imported、proxy:status、cert:status、app:ready）。
- **防闪**：StartHidden + 前端 `emit('ui:ready')` 才 WindowShow；3 秒 domReady 兜底。
- **WebSocket**（ws.go）：帧级双向中继，`raw` 原样转发（保留 mask），解析副本记入 `Flow.WebSocketMsgs`，单消息上限 1 MiB，超长按帧累积截断。
- **Body spool**（http_forward.go）：请求体超 `MaxBodyBytes`（默认 64MiB）溢出临时文件；上游收到完整 body，Flow 只留头部 + `Truncated` 标记。**`flow_builder.go` 的 `fillRequestBody`/`applyResponse` 是死代码，别用。**

## 关键位置速查

| 任务 | 位置 |
|---|---|
| 改代理行为（MITM/隧道/WS/转发） | `internal/proxy/handler.go` + `http_forward.go` + `ws.go` |
| 加/改调试工具 | `internal/tools/*.go`（每工具一文件）+ `pipeline.go` 挂顺序 |
| 改断点编辑/重放 | `internal/tools/breakpoint.go` + `app_tools.go` |
| 改证书/信任 | `internal/cert/ca.go` + `trust_{windows,darwin,linux}.go` |
| 改系统代理 | `internal/systemproxy/{windows,darwin,linux}.go` |
| 改会话/HAR 格式 | `internal/storage/session.go` + `har.go` |
| 改代码生成 | `internal/codegen/codegen.go`（生成）+ `curl_parser.go`（解析） |
| 加前端页面/组件 | `frontend/src/components/*`，工具页在 `ToolsPanel.tsx` 的 switch |
| 改 RPC/事件契约 | Go `App` 方法 ↔ `frontend/src/services/api.ts`（**wailsjs/ 生成物勿改**） |
| 改 UI 文案 | `frontend/src/i18n/en.ts`（先加 en）→ `zh-CN.ts` |

## 约定

- **RPC 命名 1:1**：前端 `api.X` ↔ Go `App.X`。`[]byte` 过 JSON 自动 base64；前端用 `types.ts` 规范类型（生成模型不准，需 `as unknown as Promise<T>`）。
- **锁纪律**：App 单把 `a.mu sync.RWMutex` 保护 flows+flowIdx+proxySrv；读 RLock / 写 Lock，事件在解锁后发。持有锁时勿调 `emitProxyStatus` 等内部加锁 helper（会自死锁）。
- **Store 同步**：前端一律"先调后端、成功后再写 zustand store"（optimistic 需谨慎，见 Toolbar/SourceList）。
- **ToolsPanel 立即生效**：`patchSection(key, fn)` 读最新 `useApp.getState()`、写 store、随即 `api.applyToolConfig`（Proxyman 式即时应用）。
- **FlowTable 十万级性能**：@tanstack/react-virtual 固定行高 30px、overscan 14；排序前先复制数组（防污染 store 引用）；near-bottom 自动跟随新 flow。
- **i18n**：9 个命名空间（app/toolbar/statusbar/sidebar/flowtable/detail/settings/common/tools），`t('dot.path', {param})`，zh 缺键回退 en；检测顺序 localStorage `wpx-lang` → navigator.language。
- **Monaco 离线**：`loader.config({paths:{vs:'./monaco/vs'}})` 用本地副本（`scripts/copy-monaco.mjs` 构建前拷贝），`EditorBoundary` 兜底降级 `<pre>`。
- **Windows 证书可静默自动安装**（用户 Root 库无 UAC）；macOS/Linux 必须用户手动安装一次（授权弹框），装好后 `IsInstalled` 检测防重复弹框。
- **事件名是字符串字面量**，两侧靠人工保持一致；Go 侧改事件名必须同步 `api.ts` Events。

## 本项目的反模式（禁止）

- **禁止手改 `frontend/wailsjs/`**：每次 `wails dev/build` 重新生成覆盖，只通过 `api.ts` 加封装。
- **禁止在代理热路径同步 emit**：Windows 上 EventsEmit 是同步 ExecJS，会卡代理。
- **禁止使用 `fillRequestBody`/`applyResponse`**（死代码）；新 body 逻辑走 `bodySpool`/`captureResponse`。
- **HAR 二进制有损**：导出仅标记 `encoding:"base64"` 但未真正编码，导入不解码——二进制 body 无法经 HAR 无损往返（不要"修复"成真编码，除非同步改导入端 + 前端）。
- **系统代理只在自己端口上清理**：`shutdown` 先比对 `Current()` 再 `Clear()`，勿无条件清（会覆盖其他工具代理）。
- **`GetFlows` 返回浅拷贝共享指针**：改返回的 Flow 会改到 store 内对象；`SaveSession` 在解锁后改 `IsSaved` 有潜在竞态，勿模仿。

## 命令

```bash
# Windows
make.bat          # 构建 + 冒烟测试 → build\bin\WProxyman.exe
make.bat dev      # wails dev 热重载
make.bat test     # go test ./... + tsc --noEmit
make.bat smoke    # 仅冒烟（需已构建）

# macOS / Linux（Wails 不支持跨平台交叉构建）
make              # 构建 + 冒烟测试 → build/bin/WProxyman(.app)
make dev / make test / make smoke / make dmg   # dmg=本地 ad-hoc 打包
```

- 冒烟测试：Windows 版验证 exe≥1MB、系统代理置位、HTTP+HTTPS 经代理 200、退出后代理清理；shell 版**只测 HTTP**（不测 HTTPS、不查大小）。CI 不跑冒烟测试。
- CI（.github/workflows/build.yml）：win amd64+arm64（同 job 交叉编译）、mac arm64（ad-hoc dmg）、linux amd64（AppImage）；**push main → 预发布 "Latest build"（tag `continuous` 覆盖）**；打 tag `v*` → 正式 Release。版本/品牌**不在仓库管理**：wails.json 无 info 段 → 二进制元数据是 Wails 默认（ProductVersion 1.0.0 / ProductName "wproxyman"）。

## 注意事项

- **图标**：`build/logo.svg`（含 WP 字标+数据链路节点）→ `build/appicon.png`(512) → macOS Wails 自动转 .icns。macOS 图标须背景满幅（四角不透明，系统裁圆角），否则 Dock 显示"没占满"；Windows 用独立 `build/windows/icon.ico`（圆角风格可保留）。
- **SettingsPanel.tsx 的中文注释是 mojibake**（编码损坏），别照抄；代码本身正常。
- **SourceList 的 Tools 导航**被 `SHOW_TOOLS = false` 硬隐藏。
- 未封装的后端 RPC（wailsjs 已生成但 api.ts 未包）：`GetFlowCount`、`GetFlowsByDomain`、`GetNetworkProfiles`、`GetRequestBody`、`GetResponseBody`、`GetScriptTemplates`、`ImportCurlFromFile`、`SetUpstreamInsecure`。
- goja 脚本引擎与规则预编译在 `SetConfig` 时重建，配置变更即时生效；规则通配符/正则匹配见 `engine.go Matches`。
- 会话格式：`.wpx` = gzip JSON（Version 1，`App:"wproxyman"`，打开时 gzip 失败回退纯 JSON）；HAR 为 1.2 子集。
