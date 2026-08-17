// Toolbar.tsx — 顶部工具栏组件
// 职责：提供抓包的核心快捷操作：捕获开关（开始/停止）、清空流量列表、
// 搜索框（同时支持命令输入）、SSL 代理开关、设置入口等。
// 交互模块：
//   - store：useApp（代理状态、SSL 状态、设置弹窗开关）、useFlows（流量数量、搜索关键字）；
//   - services/api：api.setSystemProxyEnabled / stopProxy / startProxy / clearFlows / setSSLProxyEnabled。
import { Lock, Pause, Pin, Play, Search, Settings, Trash2, Turtle } from 'lucide-react';
import { useApp, useFlows } from '../store';
import { api } from '../services/api';
import { useI18n } from '../i18n';
import { WindowToggleMaximise } from '../../wailsjs/runtime/runtime';

// Toolbar — 顶部工具栏组件（无 props、无对外状态，全部读写全局 store）。
export default function Toolbar() {
  const { t } = useI18n();
  // 代理运行状态（running/port/systemProxy）、SSL 默认开关及各主机开关。
  const proxy = useApp((s) => s.proxy);
  const sslDefault = useApp((s) => s.sslDefault);
  const sslHosts = useApp((s) => s.sslHosts);
  const setSSL = useApp((s) => s.setSSL);
const setSettingsOpen = useApp((s) => s.setSettingsOpen);

  // 流量总数（决定清空按钮是否可用）与搜索关键字。
  const flowCount = useFlows((s) => s.flows.length);
  const searchQuery = useFlows((s) => s.searchQuery);
  const setSearch = useFlows((s) => s.setSearch);

  // toggleProxy — 切换抓包（代理）运行状态。
  // 运行中 → 先关闭系统代理再停止代理；未运行 → 启动代理（端口 0 表示默认）
  // 并尝试开启系统代理，若系统代理失败则提示但仍保持代理运行。
  const toggleProxy = async () => {
    try {
      if (proxy.running) {
        // 停止抓包：先取消系统代理指向，再停止本地代理。
        await api.setSystemProxyEnabled(false);
        await api.stopProxy();
      } else {
        // 启动抓包：启动本地代理后尝试接管系统代理。
        const port = await api.startProxy(0);
        try {
          await api.setSystemProxyEnabled(true);
        } catch (sysErr) {
          // 代理已启动但系统代理开启失败：记录日志并弹窗提醒（不中断代理）。
          console.error('Proxy started but system proxy failed:', sysErr);
          window.alert(`${t('toolbar.proxyRunningSysProxyFailed', { port })}\n${String(sysErr)}`);
        }
      }
    } catch (err) {
      // 代理启停本身失败：记录日志并弹窗展示错误。
      console.error('Failed to toggle proxy', err);
      window.alert(`${t('toolbar.proxyStartFailed')}\n${String(err)}`);
    }
  };

  // clearFlows — 清空全部流量列表（调用后端清空，失败仅记录日志）。
  const clearFlows = async () => {
    try {
      await api.clearFlows();
    } catch (err) {
      console.error('Failed to clear flows', err);
    }
  };

  // toggleSSL — 切换全局 SSL 解密开关。
  // 先调用后端（host 为空串表示全局默认），成功后同步本地 store 状态。
  const toggleSSL = async () => {
    const next = !sslDefault;
    try {
      await api.setSSLProxyEnabled('', next);
      setSSL(next, sslHosts);
    } catch (err) {
      console.error('Failed to toggle SSL proxy', err);
    }
  };

  // onToolbarDoubleClick — macOS 隐藏标题栏后，系统自带的双击放大/还原
  // 不再生效（Wails 拖拽机制拦截了原生双击）。在工具栏空白处双击时
  // 手动触发窗口最大化切换，补回原生行为。交互控件上的双击不拦截。
  const onToolbarDoubleClick = (e: React.MouseEvent) => {
    const target = e.target as HTMLElement;
    // 点击在按钮/搜索框等交互元素上时不触发最大化（由元素自身处理）。
    if (target.closest('.tb-btn, .tb-search, .tb-sep')) return;
    WindowToggleMaximise();
  };

  return (
    <div className="toolbar" onDoubleClick={onToolbarDoubleClick}>
      {/* 捕获开关：运行中显示 Pause（暂停），停止时显示 Play（开始）。 */}
      <button
        type="button"
        className={`tb-btn${proxy.running ? ' active' : ''}`}
        onClick={toggleProxy}
        title={proxy.running ? t('toolbar.stopCapture') : t('toolbar.startCapture')}
        aria-pressed={proxy.running}
        aria-label={proxy.running ? t('toolbar.stopCapture') : t('toolbar.startCapture')}
      >
        {proxy.running ? <Pause size={16} fill="currentColor" /> : <Play size={16} />}
      </button>

      {/* 清空流量按钮：列表为空时禁用。 */}
      <button
        type="button"
        className="tb-btn"
        onClick={clearFlows}
        disabled={flowCount === 0}
        title={t('toolbar.clearFlows')}
        aria-label={t('toolbar.clearFlows')}
      >
        <Trash2 size={16} />
      </button>

      {/* 置顶按钮：当前为占位（不可交互，仅供展示）。 */}
      <button type="button" className="tb-btn" tabIndex={-1} aria-hidden="true" title={t('sidebar.pinned')}>
        <Pin size={16} />
      </button>

      <span className="tb-sep" />

      {/* 搜索框：实时更新 store 中的 searchQuery，列表随之过滤。 */}
      <div className="tb-search">
        <Search size={14} />
        <input
          type="text"
          value={searchQuery}
          onChange={(e) => setSearch(e.target.value)}
          placeholder={t('toolbar.search')}
          spellCheck={false}
          aria-label={t('toolbar.searchFlows')}
        />
      </div>

      <div className="tb-right">
        {/* SSL 代理开关：高亮表示当前全局 SSL 解密开启。 */}
        <button
          type="button"
          className={`tb-btn${sslDefault ? ' active' : ''}`}
          onClick={toggleSSL}
          title={t('toolbar.sslProxy')}
          aria-pressed={sslDefault}
          aria-label={t('toolbar.toggleSSL')}
        >
          <Lock size={16} />
        </button>
        {/* 网络条件（限速）按钮：当前为占位（不可交互，仅供展示）。 */}
        <button type="button" className="tb-btn" onClick={() => {}} tabIndex={-1} aria-hidden="true" title={t('toolbar.networkConditions')}>
          <Turtle size={16} />
        </button>
        {/* 设置按钮：打开设置弹窗。 */}
        <button type="button" className="tb-btn" onClick={() => setSettingsOpen(true)} title={t('toolbar.settings')} aria-label={t('toolbar.openSettings')}>
          <Settings size={16} />
        </button>
      </div>
    </div>
  );
}
