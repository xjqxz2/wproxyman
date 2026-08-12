// SourceList.tsx — 左侧来源（Source）侧栏组件
// 职责：提供流量过滤的入口树：All requests（全部）、Devices（设备，含远程设备占位）、
// Domains（按主机名统计的域名列表）、Favorites/Saved/Pinned（收藏/已保存/置顶）。
// 交互模块：
//   - store：useFlows（流量列表、来源/域名筛选）、useApp（当前视图、切换视图）、TOOL_VIEWS（工具视图清单）。
//   - 说明：Tools 分区目前按用户要求隐藏（SHOW_TOOLS = false），改回 true 可恢复工具导航。
import { useMemo, useState } from 'react';
import type { CSSProperties } from 'react';
import {
  Ban,
  Braces,
  ChevronRight,
  FolderHeart,
  Gauge,
  Globe,
  GitCompareArrows,
  MonitorSmartphone,
  PenSquare,
  Pin,
  Server,
  ShieldBan,
  Shuffle,
  Star,
  Turtle,
  Wand2,
  Wrench,
} from 'lucide-react';
import { useApp, useFlows, TOOL_VIEWS } from '../store';
import { useI18n } from '../i18n';

// chevronStyle — 生成折叠区标题左侧箭头的样式。
// 参数：open — 该分区是否展开；返回旋转 90°（展开）或 0°（收起）的过渡样式。
const chevronStyle = (open: boolean): CSSProperties => ({
  color: 'var(--text-tertiary)',
  transition: 'transform 0.15s',
  transform: open ? 'rotate(90deg)' : 'rotate(0deg)',
});

// SHOW_TOOLS — 是否显示"Tools"侧栏分区。
// 目前按用户要求隐藏；改为 true 即可恢复工具导航。
const SHOW_TOOLS = false;

// TOOL_ICONS — 工具视图 id → 图标 的映射表（未命中的回退到 Wrench 图标）。
const TOOL_ICONS: Record<string, React.ComponentType<{ size?: number | string; className?: string }>> = {
  'tools:maplocal': FolderHeart,
  'tools:mapremote': Shuffle,
  'tools:blocklist': Ban,
  'tools:allowlist': ShieldBan,
  'tools:breakpoint': PenSquare,
  'tools:scripting': Braces,
  'tools:networkconditions': Turtle,
  'tools:nocaching': Wrench,
  'tools:rules': Wand2,
  'tools:externalproxy': Gauge,
  'tools:compose': PenSquare,
  'tools:repeater': GitCompareArrows,
  'tools:diff': GitCompareArrows,
};

// SourceList — 来源侧栏组件（无 props，数据全部来自全局 store）。
export default function SourceList() {
  const { t } = useI18n();
  // 流量列表与来源/域名筛选状态（读取与更新）。
  const flows = useFlows((s) => s.flows);
  const sourceSelection = useFlows((s) => s.sourceSelection);
  const setSourceSelection = useFlows((s) => s.setSourceSelection);
  const domainSelection = useFlows((s) => s.domainSelection);
  const setDomainSelection = useFlows((s) => s.setDomainSelection);
  // 当前活动视图及其切换函数（用于 Tools 分区导航）。
  const activeView = useApp((s) => s.activeView);
  const setActiveView = useApp((s) => s.setActiveView);

  // 各分区折叠/展开的本地开关状态。
  const [sourcesOpen, setSourcesOpen] = useState(true);
  const [devicesOpen, setDevicesOpen] = useState(false);
  const [remoteDevicesOpen, setRemoteDevicesOpen] = useState(false);
  const [domainsOpen, setDomainsOpen] = useState(true);
  const [toolsOpen, setToolsOpen] = useState(true);

  // 统计各主机名的请求数量（依赖 flows 变化时重算），返回 [host, count] 数组与总数。
  const { hosts, total } = useMemo(() => {
    const counts = new Map<string, number>();
    for (const f of flows) {
      counts.set(f.host, (counts.get(f.host) ?? 0) + 1);
    }
    return { hosts: [...counts.entries()], total: flows.length };
  }, [flows]);

  return (
    <aside className="sidebar">
      {/* "Sources" 分区标题：点击折叠/展开。 */}
      <div
        className="sl-section-title"
        onClick={() => setSourcesOpen((v) => !v)}
        role="button"
        aria-expanded={sourcesOpen}
      >
        <ChevronRight size={12} style={chevronStyle(sourcesOpen)} />
        <span>{t('sidebar.sources')}</span>
      </div>

      {sourcesOpen && (
        <>
          {/* "All requests"：显示全部流量及总数。 */}
          <div
            className={`sl-item${sourceSelection === 'all' ? ' selected' : ''}`}
            onClick={() => setSourceSelection('all')}
          >
            <Server size={14} />
            <span className="sl-label">{t('sidebar.allRequests')}</span>
            <span className="sl-count">{total}</span>
          </div>

          {/* "Devices"：设备分组（含本地设备与远程设备占位）。 */}
          <div
            className="sl-item"
            onClick={() => setDevicesOpen((v) => !v)}
            role="button"
            aria-expanded={devicesOpen}
          >
            <ChevronRight size={12} className={`chevron${devicesOpen ? ' open' : ''}`} />
            <MonitorSmartphone size={14} />
            <span className="sl-label">{t('sidebar.devices')}</span>
          </div>
          {devicesOpen && (
            <>
              {/* 本地设备：筛选 sourceSelection = 'device:local'。 */}
              <div
                className={`sl-item${sourceSelection === 'device:local' ? ' selected' : ''}`}
                onClick={() => setSourceSelection('device:local')}
              >
                <span className="sl-label">{t('sidebar.localDevice')}</span>
              </div>
              {/* 远程设备分组（可折叠）。 */}
              <div
                className="sl-item"
                onClick={() => setRemoteDevicesOpen((v) => !v)}
                role="button"
                aria-expanded={remoteDevicesOpen}
              >
                <ChevronRight size={12} className={`chevron${remoteDevicesOpen ? ' open' : ''}`} />
                <span className="sl-label">{t('sidebar.remoteDevices')}</span>
              </div>
              {remoteDevicesOpen && (
                // 暂无远程设备接入时的占位提示。
                <div className="sl-item" style={{ cursor: 'default' }}>
                  <span className="sl-label">{t('sidebar.noRemoteDevices')}</span>
                </div>
              )}
            </>
          )}

          {/* "Domains"：折叠式域名列表，点击展开显示各主机名及请求计数。 */}
          <div
            className="sl-item"
            onClick={() => setDomainsOpen((v) => !v)}
            role="button"
            aria-expanded={domainsOpen}
          >
            <ChevronRight size={12} className={`chevron${domainsOpen ? ' open' : ''}`} />
            <Globe size={14} />
            <span className="sl-label">{t('sidebar.domains')}</span>
            <span className="sl-count">{hosts.length}</span>
          </div>
          {domainsOpen &&
            // 遍历主机名列表：点击某域名即设置 domainSelection 过滤器。
            hosts.map(([host, count]) => (
              <div
                key={host}
                className={`sl-item domains${domainSelection === host ? ' selected' : ''}`}
                onClick={() => setDomainSelection(host)}
                title={host}
              >
                <span className="sl-label">{host}</span>
                <span className="sl-count">{count}</span>
              </div>
            ))}

          {/* "Favorites"：收藏（固定且未保存）的流量。 */}
          <div
            className={`sl-item${sourceSelection === 'favorites' ? ' selected' : ''}`}
            onClick={() => setSourceSelection('favorites')}
          >
            <Star size={14} />
            <span className="sl-label">{t('sidebar.favorites')}</span>
          </div>

          {/* "Saved"：已保存的流量。 */}
          <div
            className={`sl-item${sourceSelection === 'saved' ? ' selected' : ''}`}
            onClick={() => setSourceSelection('saved')}
          >
            <FolderHeart size={14} />
            <span className="sl-label">{t('sidebar.saved')}</span>
          </div>

          {/* "Pinned"：置顶的流量。 */}
          <div
            className={`sl-item${sourceSelection === 'pinned' ? ' selected' : ''}`}
            onClick={() => setSourceSelection('pinned')}
          >
            <Pin size={14} />
            <span className="sl-label">{t('sidebar.pinned')}</span>
          </div>
        </>
      )}

      {/* Tools 分区 — 暂时隐藏（用户要求）。设为 true 可恢复侧栏工具导航。 */}
      {SHOW_TOOLS && (
        <>
          {/* "Tools" 分区标题：点击折叠/展开。 */}
          <div className="sl-section-title" onClick={() => setToolsOpen((v) => !v)} role="button" aria-expanded={toolsOpen}>
            <ChevronRight size={12} style={chevronStyle(toolsOpen)} />
            <span>{t('sidebar.tools')}</span>
          </div>

          {/* 遍历工具视图清单：点击某工具即切换到对应配置页（activeView）。 */}
          {toolsOpen &&
            TOOL_VIEWS.map((tool) => {
              const Icon = TOOL_ICONS[tool.id] ?? Wrench;
              return (
                <div
                  key={tool.id}
                  className={`sl-item${activeView === tool.id ? ' selected' : ''}`}
                  onClick={() => setActiveView(tool.id)}
                >
                  <Icon size={14} />
                  <span className="sl-label">{tool.label}</span>
                </div>
              );
            })}
        </>
      )}
    </aside>
  );
}
