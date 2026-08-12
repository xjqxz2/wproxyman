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
make.bat          :: 构建（产物 build\bin\WProxyman.exe）
make.bat dev      :: 开发模式（热重载）
make.bat test     :: 运行测试
make.bat clean    :: 清理
```

### macOS / Linux

```sh
make              # 构建（产物 build/bin/WProxyman(.app)）
make dev          # 开发模式
make test         # 运行测试
make clean        # 清理
```

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

GitHub Actions（`.github/workflows/build.yml`）在每次推送时于四个原生
Runner 上并行构建 Windows / macOS（Intel + Apple Silicon）/ Linux：

- **push 到 `main`**：构建产物保存为 **Actions Artifacts**；
- **打 tag（如 `v1.0.0`）**：自动发布到 **GitHub Release**，四个平台的安装包
  直接挂在 Release 页面下载。

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
