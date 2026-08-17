# WProxyman

一款跨平台（Windows / macOS / Linux）的 **HTTP / HTTPS 抓包调试代理工具**，
界面与交互参考 Proxyman 设计，支持 MITM 解密、HTTP/2、WebSocket 帧捕获，
以及 Map Local、断点、脚本等常用调试工具。

纯 Go 自研代理内核 + Wails v2 桌面壳 + React/TypeScript 前端，**零第三方
代理库依赖**，单文件分发。

---

## ✨ 功能特性

| 能力 | 说明 |
|---|---|
| 🔓 **HTTPS MITM 解密** | 本地 CA 动态签发域名证书，三平台自动信任；证书未信任时自动降级为隧道，保证流量不中断 |
| ⚡ **HTTP/2 支持** | 拦截连接经 ALPN 协商 h2，完整解密多路复用流量 |
| 🔌 **WebSocket 捕获** | 帧级双向中继 + 消息记录（文本/二进制/控制帧） |
| 🧰 **调试工具集** | Map Local、Map Remote、Block List、Allow List、断点（可编辑请求/响应）、goja 脚本引擎、网络限速、Rules、外部代理、No Caching、Compose、Repeater、Diff |
| 📦 **内容查看** | JSON / XML 自动格式化、图片预览、二进制 hex 视图、素材一键下载保存 |
| 🌐 **国际化** | 简体中文 / English，启动跟随系统语言，设置内可手动切换 |
| 🎛 **搜索与过滤** | 虚拟滚动表格（十万级流量不卡）、按域名/方法/状态过滤、全文搜索 |
| 💾 **会话管理** | 会话保存/打开（gzip JSON）、HAR 导入/导出、cURL 导入、代码生成（cURL / Node-fetch / Postman） |

---

## 🛠 技术栈

- **后端**：Go 1.25+（自研 MITM 代理引擎，纯标准库 + x/net/http2）
- **桌面壳**：Wails v2（WebView2 / WKWebView / WebKitGTK）
- **前端**：React 19 + TypeScript + Vite + Monaco Editor + zustand

---

## 🚀 构建

> Wails 依赖系统原生库，各平台需在对应系统上构建（不支持交叉编译）。

### 前置要求

- Go 1.25+
- Node.js 20+
- Wails CLI（构建脚本会自动安装）
- macOS：Xcode Command Line Tools
- Linux：`libgtk-3-dev`、`libwebkit2gtk-4.0-dev` 等（脚本会检测并提示）

### Windows

```bat
make.bat          :: 构建 + 冒烟测试（产物 build\bin\WProxyman.exe）
make.bat smoke    :: 仅运行冒烟测试（需已构建）
make.bat dev      :: 开发模式（热重载）
make.bat test     :: 运行单元测试
make.bat clean    :: 清理
```

### macOS / Linux

```sh
make              # 构建 + 冒烟测试（产物 build/bin/WProxyman(.app)）
make smoke        # 仅运行冒烟测试
make dev          # 开发模式
make test         # 运行单元测试
make dmg          # macOS 本地 ad-hoc 打包
make clean        # 清理
```

### 冒烟测试

构建完成后自动执行（`scripts/smoke-test.ps1` / `scripts/smoke-test.sh`），
真实启动应用并验证：

1. 产物存在且非空；
2. 应用自动开启系统代理（等待端口就绪）；
3. 通过代理发送 HTTP 请求（期望 200）；
4. 通过代理发送 HTTPS 请求（期望 200）；
5. 关闭应用后系统代理被自动清理。

任一步失败则构建视为失败（退出非零）。

### 构建产物

```
build/bin/
├── WProxyman.exe      # Windows
├── WProxyman.app      # macOS
└── WProxyman          # Linux
```

---

## 📖 使用

1. 启动应用 —— **自动开启代理并接管系统代理**，浏览器刷新即可开始抓包；
2. 首次使用 HTTPS：设置 ⚙ → SSL 解密 → **安装证书**（三平台自动信任）；
3. 点击流量行查看请求/响应详情：概览、Headers、格式化正文、Raw、WebSocket 消息；
4. 素材类响应（图片/音频/PDF 等）可直接 **Save to file** 下载。

> 抓包期间浏览器会自动禁用 HTTP/3（QUIC 无法走 HTTP 代理），所有流量以
> HTTP/1.1 / HTTP/2 呈现并完整解密——这是行业标准行为（Charles/Proxyman 相同）。

---

## 🔄 CI 与 Release

GitHub Actions（`.github/workflows/build.yml`）在每次推送时并行构建全平台矩阵：

| 平台 | 架构 | 产物 |
|---|---|---|
| Windows | x64 | `WProxyman_windows_amd64.exe` |
| Windows | ARM64 | `WProxyman_windows_arm64.exe`（交叉编译） |
| macOS | Apple Silicon | `WProxyman_darwin_arm64.dmg` |
| macOS | Intel | `WProxyman_darwin_amd64.dmg` |
| Linux | x64 | `WProxyman_linux_amd64.AppImage` |

（Linux ARM64 暂不发布；其他架构可在本机执行 `make` 构建）

- **push 到 `main`**：构建产物自动发布到 **Releases** 页面的 *Latest build*（预发布，每次覆盖更新），无需手动操作；
- **打 tag（如 `v1.0.0`）**：自动发布**正式 Release**。
- 注：macOS 使用 macos-26 镜像（arm64 原生）与 macos-26-intel（免费 Intel 标准
  runner，替代已下线的旧 Intel runner）；macos-14 镜像已进入弃用期（2026-11-02 下线）。

> **macOS 首次打开**：应用未通过 Apple 公证（notarization），macOS Gatekeeper
> 会提示"无法打开/无法验证开发者"。解决方式（任选其一）：
>
> 1. 右键（或按住 Control）点击 `WProxyman.app` → 选择**打开**；
> 2. 终端执行：`xattr -d com.apple.quarantine /Applications/WProxyman.app`；
> 3. 系统设置 → 隐私与安全性 → **仍要打开**。
>
> 要彻底消除提示（与商业软件一致），需要配置 Apple Developer ID 证书并在
> CI 中执行公证（notarization），详见 [notarytool 文档](https://developer.apple.com/documentation/security/notarizing-macos-software-before-distribution)。

```bash
git tag v1.0.0 && git push origin v1.0.0
```

---

## 📁 项目结构

```
wproxyman/
├── main.go                 # Wails 入口（窗口/防闪/绑定）
├── app*.go                 # 服务层：代理控制、证书、会话、下载、代码生成
├── internal/
│   ├── proxy/              # 自研 MITM 代理引擎（HTTP/2、WebSocket、隧道）
│   ├── cert/               # 本地 CA、动态证书、三平台信任库
│   ├── tools/              # 工具管线：Map Local/Remote、断点、脚本、限速等
│   ├── storage/            # 会话持久化、HAR 导入/导出
│   ├── systemproxy/        # 系统代理（Windows 注册表 / macOS networksetup / Linux gsettings）
│   └── codegen/            # cURL / Node-fetch / Postman 代码生成与解析
├── frontend/               # React + TypeScript 前端
│   └── src/
│       ├── components/     # 工具栏、来源列表、流量表格、详情面板、设置等
│       ├── i18n/           # 国际化（中英双语，跟随系统语言）
│       └── services/       # Wails 绑定封装与事件总线
├── Makefile                # macOS / Linux 构建脚本
├── make.bat                # Windows 构建脚本
└── LICENSE                 # Apache-2.0
```

---

## 📄 License

[Apache License 2.0](LICENSE)

Copyright © 2026 **Wind** · xjqxzmxl@gmail.com

本项目为独立开发的原创软件，与 Proxyman LLC 及其产品无任何关联。
