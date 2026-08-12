// WProxyman 入口：Wails 桌面应用的主进程。
//
// 主要职责：
//  1. 通过 //go:embed 将前端构建产物（frontend/dist）内嵌进二进制，
//     实现单文件分发（无需额外资源目录）。
//  2. 配置窗口属性（尺寸、主题、防闪策略等）并启动 Wails 运行时。
//  3. 绑定 App 实例——App 的所有导出方法都会被自动生成为前端可调用的
//     TypeScript API（见 frontend/wailsjs/）。
//
// 关于防闪（Windows 上 WebView2 的黑色闪烁）：
//   - StartHidden：窗口先隐藏，等前端渲染完成发出 "ui:ready" 事件后再显示；
//   - WebviewGpuIsDisabled：禁用 GPU 合成，规避新显卡驱动下的闪烁；
//   - BackgroundColour 与界面背景色一致，任何绘制间隙都不会露出黑框。
package main

import (
	"embed"

	"github.com/wailsapp/wails/v2"
	"github.com/wailsapp/wails/v2/pkg/options"
	"github.com/wailsapp/wails/v2/pkg/options/assetserver"
	"github.com/wailsapp/wails/v2/pkg/options/windows"
)

//go:embed all:frontend/dist
var assets embed.FS

func main() {
	app := NewApp()

	err := wails.Run(&options.App{
		Title:     "WProxyman",
		Width:     1280,
		Height:    800,
		MinWidth:  960,
		MinHeight: 640,
		AssetServer: &assetserver.Options{
			Assets: assets,
		},
		// 窗口背景色与前端主题 --bg-app (#1a1b1e) 保持一致，防止绘制间隙闪黑框。
		BackgroundColour: &options.RGBA{R: 26, G: 27, B: 30, A: 1},
		// 启动时隐藏窗口，直到前端渲染完成（见 App.startup 的 "ui:ready" 监听）。
		StartHidden: true,
		OnStartup:   app.startup,
		// 兜底：前端始终未就绪时也强制显示窗口（3 秒后）。
		OnDomReady: app.domReady,
		OnShutdown: app.shutdown,
		Bind: []interface{}{
			app,
		},
		Windows: &windows.Options{
			WebviewIsTransparent: false,
			WindowIsTranslucent:  false,
			Theme:                windows.Dark,
			// 禁用 WebView2 GPU 合成：新 NVIDIA 驱动下合成器在窗口交互时闪黑帧。
			WebviewGpuIsDisabled: true,
			// 窗口缩放时防抖重绘，避免边缘闪烁。
			ResizeDebounceMS: 100,
		},
	})

	if err != nil {
		println("Error:", err.Error())
	}
}
