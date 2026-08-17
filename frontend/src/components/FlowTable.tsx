// FlowTable.tsx — 流量列表（虚拟滚动表格）组件
// 职责：以虚拟化表格展示捕获到的全部 HTTP/HTTPS/WebSocket 流量，
// 支持列排序（方法/状态码）、右键上下文菜单（复制 cURL/Node fetch、置顶、删除）、
// 点击选中并在详情面板展示、以及"跟随新流量"的自动滚动。
// 交互模块：
//   - store：useFlows/filterFlows（读取并筛选流量、选中/删除/置顶）、useApp（待处理断点）；
//   - services/api：generateCurl / generateNodeFetch / setFlowPinned / deleteFlow；
//   - components/ContextMenu：右键菜单（含类型 ContextMenuItems）；
//   - types：formatBytes / formatDuration / formatTime 展示格式化。
import { useEffect, useMemo, useRef, useState } from 'react';
import { useVirtualizer } from '@tanstack/react-virtual';
import {
  AlertTriangle,
  ChevronDown,
  ChevronUp,
  Copy,
  FileCode,
  Globe,
  Lock,
  Pin,
  Send,
  Terminal,
  Trash2,
} from 'lucide-react';
import { useApp, useFlows, filterFlows } from '../store';
import { api } from '../services/api';
import type { Flow } from '../types';
import { formatBytes, formatDuration, formatTime } from '../types';
import ContextMenu from './ContextMenu';
import type { ContextMenuItems } from './ContextMenu';
import { useI18n } from '../i18n';

// ROW_HEIGHT — 每行固定高度（px），用于虚拟滚动估算行高。
const ROW_HEIGHT = 30;

// ColumnDef — 表格列定义：key（标识）、label（表头文字）、
// width（固定像素宽）或 flex（弹性占比）、minWidth（拖拽调整时的最小像素宽下限）、
// defaultWidth（弹性列首次被拖拽时的兜底像素宽，实际以当前计算宽度为准）、
// sortable（是否可排序）。
interface ColumnDef {
  key: string;
  label: string;
  width?: number;
  flex?: number;
  minWidth: number;
  defaultWidth?: number;
  sortable?: boolean;
}

// MAX_COL_WIDTH — 拖拽调整列宽时的最大像素宽上限。
const MAX_COL_WIDTH = 600;

// COLUMNS — 表格列配置：序号、方法（可排序）、主机（弹性）、路径（弹性）、
// 状态（可排序）、大小、耗时、时间。
const COLUMNS: ColumnDef[] = [
  { key: 'index', label: '#', width: 36, minWidth: 36 },
  { key: 'method', label: 'Method', width: 74, minWidth: 56, sortable: true },
  { key: 'host', label: 'Host', flex: 1.2, minWidth: 110, defaultWidth: 180 },
  { key: 'path', label: 'Path', flex: 1.8, minWidth: 120, defaultWidth: 260 },
  { key: 'status', label: 'Status', width: 56, minWidth: 50, sortable: true },
  { key: 'size', label: 'Size', width: 70, minWidth: 60 },
  { key: 'duration', label: 'Duration', width: 70, minWidth: 60 },
  { key: 'time', label: 'Time', width: 76, minWidth: 70 },
];

// COLUMN_BY_KEY — 列 key → 列定义的查找表（行单元格按 key 取列配置）。
const COLUMN_BY_KEY = new Map(COLUMNS.map((c) => [c.key, c]));

// METHOD_COLORS — 有专属配色的 HTTP 方法列表，其余方法统一用 method-other 样式。
const METHOD_COLORS = ['get', 'post', 'put', 'delete', 'patch'];

// methodClass — 根据 HTTP 方法返回对应的 CSS class（用于方法名着色）。
// 参数：method — HTTP 方法字符串；返回值：如 'method-get' 或 'method-other'。
function methodClass(method: string): string {
  const m = method.toLowerCase();
  return METHOD_COLORS.includes(m) ? `method-${m}` : 'method-other';
}

// statusClass — 根据响应状态码返回按百位分组的 CSS class（status-2xx 等）。
// 参数：code — HTTP 状态码；返回值：'status-2xx'；code <= 0（尚未响应）时返回空串。
function statusClass(code: number): string {
  if (code <= 0) return '';
  return `status-${Math.floor(code / 100)}xx`;
}

// queryText — 规范化查询串的显示：若 query 不以 '?' 开头则补上前缀。
// 参数：f — 流量记录；返回值：如 '?foo=1' 或空串。
function queryText(f: Flow): string {
  if (!f.query) return '';
  return f.query.startsWith('?') ? f.query : `?${f.query}`;
}

// FlowTable — 流量列表组件（无 props，数据与操作全部来自全局 store）。
export default function FlowTable() {
  const { t } = useI18n();
  // 流量列表与各类筛选/选中状态（来自 store）。
  const flows = useFlows((s) => s.flows);
  const selectedId = useFlows((s) => s.selectedId);
  const searchQuery = useFlows((s) => s.searchQuery);
  const sourceSelection = useFlows((s) => s.sourceSelection);
  const domainSelection = useFlows((s) => s.domainSelection);
  const showOnlyWS = useFlows((s) => s.showOnlyWS);
  const showOnlyFailed = useFlows((s) => s.showOnlyFailed);
  const setSelected = useFlows((s) => s.setSelected);
  const togglePinned = useFlows((s) => s.togglePinned);
  const removeFlow = useFlows((s) => s.removeFlow);
  // 待处理断点集合（暂停等待决策的流量 id 列表）。
  const pendingBreakpoints = useApp((s) => s.pendingBreakpoints);

  // 本地状态：当前排序（方法或状态码 + 升降序）、右键菜单位置（坐标 + 流量 id）。
  const [sort, setSort] = useState<{ key: 'method' | 'status'; asc: boolean }>({ key: 'method', asc: true });
  const [menu, setMenu] = useState<{ x: number; y: number; flowId: string } | null>(null);
  // 列宽状态：仅包含用户手动拖拽过的列（key → 像素宽，覆盖默认 width/flex）。
  const [colWidths, setColWidths] = useState<Record<string, number>>({});
  // 正在拖拽调整宽度的列 key（用于手柄 .active 高亮）。
  const [resizing, setResizing] = useState<string | null>(null);
  // 滚动容器引用、是否接近底部标记、上一次行数（用于判断是否有新行到来）。
  const parentRef = useRef<HTMLDivElement>(null);
  const nearBottomRef = useRef(true);
  const prevLenRef = useRef(0);
  // 拖拽结束的清理函数（组件卸载时兜底调用）与点击抑制标记（拖拽后忽略随后 click，避免误触发排序）。
  const resizeCleanupRef = useRef<(() => void) | null>(null);
  const suppressClickRef = useRef(false);

  // 计算筛选 + 排序后的行。注意先拷贝再排序：filterFlows 在无筛选条件时
  // 可能直接返回 store 数组引用，直接 sort 会污染 store 数据。
  const rows = useMemo(() => {
    const base = [...filterFlows(useFlows.getState())];
    if (sort.key === 'method') {
      // 按方法名字典序排序（升降序可切换）。
      base.sort((a, b) => {
        const cmp = a.method.localeCompare(b.method);
        return sort.asc ? cmp : -cmp;
      });
    } else {
      // 按响应状态码数值排序（升降序可切换）。
      base.sort((a, b) => (a.responseStatus - b.responseStatus) * (sort.asc ? 1 : -1));
    }
    return base;
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [flows, searchQuery, sourceSelection, domainSelection, showOnlyWS, showOnlyFailed, sort]);

  // 虚拟滚动器：只渲染可视区域内的行（overscan 为上下各多渲染 14 行缓冲）。
  const rowVirtualizer = useVirtualizer({
    count: rows.length,
    getScrollElement: () => parentRef.current,
    estimateSize: () => ROW_HEIGHT,
    overscan: 14,
  });

  // 跟随新流量：当列表变长且用户原本就停留在底部附近时，
  // 自动滚动到最后一行（align: 'end'），否则不打扰用户浏览位置。
  useEffect(() => {
    if (rows.length > prevLenRef.current && nearBottomRef.current) {
      rowVirtualizer.scrollToIndex(rows.length - 1, { align: 'end' });
    }
    prevLenRef.current = rows.length;
  }, [rows.length, rowVirtualizer]);

  // 组件卸载时清理可能残留的列宽拖拽监听。
  useEffect(() => () => resizeCleanupRef.current?.(), []);

  // handleScroll — 滚动事件处理：实时判断用户是否接近列表底部
  //（距底部不足 200px 视为"接近底部"，用于决定是否自动跟随新流量）。
  const handleScroll = () => {
    const el = parentRef.current;
    if (!el) return;
    nearBottomRef.current = el.scrollHeight - el.scrollTop - el.clientHeight < 200;
  };

  // toggleSort — 切换排序：同一列再次点击则反转升降序，否则切换到新列（默认升序）。
  const toggleSort = (key: 'method' | 'status') => {
    setSort((s) => (s.key === key ? { key, asc: !s.asc } : { key, asc: true }));
  };

  // copyText — 将文本复制到剪贴板（剪贴板不可用时静默忽略）。
  const copyText = async (text: string) => {
    try {
      await navigator.clipboard.writeText(text);
    } catch {
      /* clipboard unavailable */
    }
  };

  // 生成并复制 cURL / Node fetch 命令文本。
  const copyCurl = async (flow: Flow) => copyText(await api.generateCurl(flow.id));
  const copyNodeFetch = async (flow: Flow) => copyText(await api.generateNodeFetch(flow.id));

  // togglePin — 切换流量置顶：先调后端持久化，成功后同步本地 store。
  const togglePin = async (flow: Flow) => {
    try {
      await api.setFlowPinned(flow.id, !flow.isPinned);
      togglePinned(flow.id);
    } catch {
      /* ignore */
    }
  };

  // deleteFlow — 删除流量：先调后端删除，成功后同步本地 store。
  const deleteFlow = async (flow: Flow) => {
    try {
      await api.deleteFlow(flow.id);
      removeFlow(flow.id);
    } catch {
      /* ignore */
    }
  };

  // openMenu — 右键事件处理：阻止浏览器默认菜单，记录点击位置与目标流量。
  const openMenu = (e: React.MouseEvent, flow: Flow) => {
    e.preventDefault();
    setMenu({ x: e.clientX, y: e.clientY, flowId: flow.id });
  };

  // menuItems — 根据右键目标流量构建上下文菜单项列表（惰性计算，仅在有菜单时有效）。
  const menuItems = useMemo<ContextMenuItems>(() => {
    const flow = rows.find((f) => f.id === menu?.flowId);
    if (!flow) return [];
    return [
      { label: t('flowtable.copyCurl'), icon: <Terminal size={13} />, onClick: () => void copyCurl(flow) },
      { label: t('flowtable.copyNodeFetch'), icon: <FileCode size={13} />, onClick: () => void copyNodeFetch(flow) },
      'separator',
      // 置顶/取消置顶（根据当前状态切换文案）。
      {
        label: flow.isPinned ? t('flowtable.unpin') : t('flowtable.pin'),
        icon: <Pin size={13} />,
        onClick: () => void togglePin(flow),
      },
      // 删除（危险操作，红色样式）。
      { label: t('flowtable.delete'), icon: <Trash2 size={13} />, danger: true, onClick: () => void deleteFlow(flow) },
      'separator',
      // 发送到 Compose（当前实现为复制 cURL，占位行为）。
      { label: t('flowtable.sendToCompose'), icon: <Send size={13} />, onClick: () => void copyCurl(flow) },
    ];
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, [rows, menu]);

  // cellStyle — 计算单元格样式（表头与行单元格通用）：
  // 用户拖拽过的列 → 固定像素宽（flexShrink:0，覆盖默认 width/flex）；
  // 定宽列 → width + flexShrink:0；其余弹性列 → flex 占比。
  const cellStyle = (col: ColumnDef): React.CSSProperties => {
    const w = colWidths[col.key];
    if (w !== undefined) return { width: w, flexShrink: 0 };
    if (col.width !== undefined) return { width: col.width, flexShrink: 0 };
    return { flex: col.flex, minWidth: 0 };
  };

  // handleResizeStart — 列宽拖拽开始：记录起始 X 与起始像素宽，挂载 window 级
  // pointermove/pointerup 监听，拖拽期间禁用文本选择与自定义光标。
  // 弹性列（host/path）首次被拖拽时，以当前实际渲染像素宽作为种子写入 colWidths，
  // 此后该列转为固定像素宽（flexShrink:0）。宽度被 clamp 在 [minWidth, MAX_COL_WIDTH]。
  const handleResizeStart = (e: React.PointerEvent<HTMLDivElement>, col: ColumnDef) => {
    e.preventDefault();
    e.stopPropagation();
    suppressClickRef.current = true;
    const cellEl = e.currentTarget.parentElement as HTMLElement | null;
    const startX = e.clientX;
    const recorded = colWidths[col.key];
    const measured = cellEl ? Math.round(cellEl.getBoundingClientRect().width) : 0;
    const startWidth = recorded ?? (measured > 0 ? measured : col.defaultWidth ?? col.minWidth);
    if (recorded === undefined) {
      setColWidths((prev) => ({ ...prev, [col.key]: startWidth }));
    }
    setResizing(col.key);
    document.body.style.userSelect = 'none';
    document.body.style.cursor = 'col-resize';
    const onMove = (ev: PointerEvent) => {
      const next = Math.min(MAX_COL_WIDTH, Math.max(col.minWidth, startWidth + (ev.clientX - startX)));
      setColWidths((prev) => ({ ...prev, [col.key]: next }));
    };
    const onUp = () => {
      window.removeEventListener('pointermove', onMove);
      window.removeEventListener('pointerup', onUp);
      document.body.style.userSelect = '';
      document.body.style.cursor = '';
      setResizing(null);
      resizeCleanupRef.current = null;
    };
    resizeCleanupRef.current = onUp;
    window.addEventListener('pointermove', onMove);
    window.addEventListener('pointerup', onUp);
  };

  return (
    <div className="table-wrap">
      {/* 虚拟滚动容器：负责捕获滚动事件并禁止默认右键菜单。 */}
      <div
        ref={parentRef}
        className="flow-table"
        tabIndex={0}
        onScroll={handleScroll}
        onContextMenu={(e) => e.preventDefault()}
      >
        {rows.length === 0 ? (
          // 空列表占位提示。
          <div
            style={{
              height: '100%',
              display: 'flex',
              alignItems: 'center',
              justifyContent: 'center',
              color: 'var(--text-tertiary)',
              fontSize: 13,
            }}
          >
            {t('flowtable.empty')}
          </div>
        ) : (
          <>
            {/* 表头：可排序列点击切换排序，并显示升降序箭头；右缘带列宽拖拽手柄。 */}
            <div className="ft-header">
              {COLUMNS.map((col) => (
                <div
                  key={col.key}
                  className="ft-col"
                  style={cellStyle(col)}
                  onClick={(e) => {
                    // 拖拽结束后紧随的 click 不触发排序。
                    if (suppressClickRef.current) {
                      suppressClickRef.current = false;
                      return;
                    }
                    if (col.sortable) toggleSort(col.key as 'method' | 'status');
                  }}
                >
                  {t(`flowtable.${col.key}`)}
                  {col.sortable && sort.key === col.key && (
                    <span style={{ verticalAlign: 'middle', marginLeft: 4 }}>
                      {sort.asc ? <ChevronUp size={11} /> : <ChevronDown size={11} />}
                    </span>
                  )}
                  {/* 列宽拖拽手柄：停靠在单元格右缘，pointerdown/click 均不冒泡以免触发排序。 */}
                  <div
                    className={`ft-resize${resizing === col.key ? ' active' : ''}`}
                    data-resize-key={col.key}
                    onPointerDown={(e) => handleResizeStart(e, col)}
                    onClick={(e) => e.stopPropagation()}
                  />
                </div>
              ))}
            </div>
            {/* 虚拟化行容器：高度为全部行总高，行通过 translateY 绝对定位到各自位置。 */}
            <div
              style={{
                height: rowVirtualizer.getTotalSize(),
                width: '100%',
                position: 'relative',
              }}
            >
              {/* 只渲染可视区域内的行。 */}
              {rowVirtualizer.getVirtualItems().map((vi) => {
                const flow = rows[vi.index];
                const isSelected = flow.id === selectedId;
                // 断点暂停的流量在行左侧显示黄色警示条。
                const isPaused = pendingBreakpoints.includes(flow.id);
                return (
                  <div
                    key={flow.id}
                    ref={rowVirtualizer.measureElement}
                    data-index={vi.index}
                    className={`ft-row${isSelected ? ' selected' : ''}${flow.isWebSocket ? ' ws' : ''}`}
                    style={{
                      position: 'absolute',
                      top: 0,
                      left: 0,
                      width: '100%',
                      transform: `translateY(${vi.start}px)`,
                      boxShadow: isPaused ? 'inset 3px 0 0 var(--warn)' : undefined,
                    }}
                    onClick={() => setSelected(flow.id)}
                    onDoubleClick={() => void copyCurl(flow)}
                    onContextMenu={(e) => openMenu(e, flow)}
                    role="row"
                    aria-selected={isSelected}
                  >
                    {/* 序号列：展示 TLS 锁图标 / 置顶图标 / 错误警示图标。 */}
                    <div className="ft-col" style={{ ...cellStyle(COLUMN_BY_KEY.get('index')!), padding: '0 4px', overflow: 'visible', display: 'flex', alignItems: 'center', gap: 2 }}>
                      {flow.tls ? <Lock size={13} style={{ color: 'var(--text-tertiary)', flexShrink: 0 }} /> : <Globe size={13} style={{ color: 'var(--text-tertiary)', flexShrink: 0 }} />}
                      {flow.isPinned && <Pin size={12} style={{ color: 'var(--warn)', flexShrink: 0 }} />}
                      {flow.error && <AlertTriangle size={12} style={{ color: 'var(--error)', flexShrink: 0 }} />}
                    </div>
                    {/* 方法列：按方法着色，WebSocket 流量追加 WS 标签。 */}
                    <div className={`ft-col method ${methodClass(flow.method)}`} style={cellStyle(COLUMN_BY_KEY.get('method')!)}>
                      {flow.method}
                      {flow.isWebSocket && (
                        <span className="tag green" style={{ marginLeft: 4, padding: '0 5px', fontSize: 10 }}>
                          {t('flowtable.ws')}
                        </span>
                      )}
                    </div>
                    {/* 主机列（hover 显示完整主机名）。 */}
                    <div className="ft-col host" style={cellStyle(COLUMN_BY_KEY.get('host')!)} title={flow.host}>
                      {flow.host}
                    </div>
                    {/* 路径列：路径 + 查询串（hover 显示完整 URL）。 */}
                    <div className="ft-col path" style={cellStyle(COLUMN_BY_KEY.get('path')!)} title={flow.fullUrl}>
                      {flow.path}
                      {flow.query && <span style={{ color: 'var(--text-tertiary)' }}>{queryText(flow)}</span>}
                    </div>
                    {/* 状态列：按百位分组着色，未响应时显示破折号。 */}
                    <div className={`ft-col status ${statusClass(flow.responseStatus)}`} style={cellStyle(COLUMN_BY_KEY.get('status')!)}>
                      {flow.responseStatus > 0 ? flow.responseStatus : '—'}
                    </div>
                    {/* 大小 / 耗时 / 时间列：使用格式化工具展示。 */}
                    <div className="ft-col" style={cellStyle(COLUMN_BY_KEY.get('size')!)}>
                      {formatBytes(flow.responseSize)}
                    </div>
                    <div className="ft-col time" style={cellStyle(COLUMN_BY_KEY.get('duration')!)}>
                      {formatDuration(flow.duration)}
                    </div>
                    <div className="ft-col time" style={cellStyle(COLUMN_BY_KEY.get('time')!)}>
                      {formatTime(flow.startedAt)}
                    </div>
                  </div>
                );
              })}
            </div>
          </>
        )}
      </div>
      {/* 右键菜单：有菜单状态时在指定坐标渲染，关闭时清空状态。 */}
      {menu && (
        <ContextMenu x={menu.x} y={menu.y} items={menuItems} onClose={() => setMenu(null)} />
      )}
    </div>
  );
}
