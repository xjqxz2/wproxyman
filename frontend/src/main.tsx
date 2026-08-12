// main.tsx — 前端应用入口
// 职责：负责 React 应用的挂载与全局错误兜底。
// 交互模块：imports App（根组件），并应用全局样式 theme.css。
// 说明：应用被包裹在 <React.StrictMode>（开发期严格模式检查）与
// AppBoundary（全局错误边界）中，任何组件渲染崩溃都会显示降级页面，
// 而不是让整个应用白屏。
import React, { Component } from 'react'
import type { ReactNode } from 'react'
import {createRoot} from 'react-dom/client'
import './styles/theme.css'
import App from './App'
import { LanguageProvider } from './i18n'

// AppBoundary — 全局错误边界组件
// 用途：捕获任意子组件渲染（render）阶段抛出的异常。
// 参数：children — 需要被保护的应用子树。
// 返回值：错误边界未触发时返回子组件；触发后返回降级错误提示页。
class AppBoundary extends Component<{ children: ReactNode }, { failed: boolean }> {
  state = { failed: false }

  // 静态方法：当子组件渲染抛出错误时被 React 调用，
  // 返回新的 state 以标记"已出错"，从而触发降级 UI 渲染。
  static getDerivedStateFromError() {
    return { failed: true }
  }

  render() {
    // 若已标记出错，则渲染全屏居中的错误提示（而非卸载整个应用）；
    // 否则正常渲染受保护的子组件树。
    if (this.state.failed) {
      return (
        <div style={{ height: '100%', display: 'flex', flexDirection: 'column', alignItems: 'center', justifyContent: 'center', gap: 12, color: '#e4e4e7', background: '#1a1b1e', fontFamily: 'Inter, -apple-system, sans-serif' }}>
          <div style={{ fontSize: 15, fontWeight: 600 }}>Something went wrong</div>
          <div style={{ fontSize: 12, color: '#9b9ca3' }}>An unexpected error occurred. Restart the app to recover.</div>
        </div>
      )
    }
    return this.props.children
  }
}

const container = document.getElementById('root')

// 使用 React 19 的 createRoot API 在 #root 容器上创建根节点，
// 并通过 StrictMode + 错误边界挂载整个应用。
const root = createRoot(container!)

root.render(
    <React.StrictMode>
        <LanguageProvider>
            <AppBoundary>
                <App/>
            </AppBoundary>
        </LanguageProvider>
    </React.StrictMode>
)
