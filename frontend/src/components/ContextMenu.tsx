// ContextMenu.tsx — 通用右键上下文菜单组件
// 职责：在指定屏幕坐标渲染一个可交互的下拉菜单，支持：
//   - 点击外部 / 按 Escape / 窗口失焦 / 再次触发 contextmenu 时自动关闭；
//   - 方向键（↑/↓）与 Enter/空格 的键盘导航；
//   - 菜单边界自适应（不超出窗口可视区域）；
//   - 分隔符与"危险操作"（红色样式）条目。
// 交互模块：被 FlowTable 等需要右键菜单的组件复用（通过 items 传入菜单项）。
import { useEffect, useLayoutEffect, useMemo, useRef, useState } from 'react';
import type { ReactNode } from 'react';

// ContextMenuItem — 单个菜单项：label（文案）、icon（可选图标）、
// danger（危险操作标记，红色样式）、onClick（点击回调）。
export interface ContextMenuItem {
  label: string;
  icon?: ReactNode;
  danger?: boolean;
  onClick: () => void;
}

// ContextMenuItems — 菜单项列表类型：普通菜单项或 'separator' 分隔符。
export type ContextMenuItems = Array<ContextMenuItem | 'separator'>;

// ContextMenuProps — 组件 props：x/y（菜单锚点坐标）、
// items（菜单项列表）、onClose（关闭回调，点击项或任何关闭时机触发）。
interface ContextMenuProps {
  x: number;
  y: number;
  items: ContextMenuItems;
  onClose: () => void;
}

/**
 * 通用右键菜单组件。
 * 关闭时机：外部 mousedown、Escape、窗口失焦（blur）或再次发生 contextmenu 事件；
 * 支持方向键导航菜单项。
 */
export default function ContextMenu({ x, y, items, onClose }: ContextMenuProps) {
  // 容器 DOM 引用与各菜单项 DOM 引用（用于键盘导航聚焦）。
  const ref = useRef<HTMLDivElement>(null);
  const itemRefs = useRef<(HTMLDivElement | null)[]>([]);
  // 用 ref 持有最新的 onClose，避免全局监听器闭包捕获过期回调。
  const onCloseRef = useRef(onClose);
  onCloseRef.current = onClose;

  // 最终渲染位置（可能被 viewport 限制逻辑修正过）与当前高亮项索引。
  const [pos, setPos] = useState({ x, y });
  const [activeIndex, setActiveIndex] = useState(-1);

  // 只包含"可选择"菜单项的索引列表（跳过分隔符），供方向键导航使用。
  const itemIndices = useMemo(() => {
    const idxs: number[] = [];
    items.forEach((it, i) => {
      if (it !== 'separator') idxs.push(i);
    });
    return idxs;
  }, [items]);

  // 边界自适应：若菜单超出窗口边缘，则将其坐标钳制回可视区域内
  //（保留 8px 边距），避免菜单溢出窗口被裁切。
  useLayoutEffect(() => {
    const el = ref.current;
    if (!el) return;
    const rect = el.getBoundingClientRect();
    const margin = 8;
    const nx = Math.min(x, Math.max(margin, window.innerWidth - rect.width - margin));
    const ny = Math.min(y, Math.max(margin, window.innerHeight - rect.height - margin));
    if (nx !== x || ny !== y) setPos({ x: nx, y: ny });
  }, [x, y]);

  // 全局监听：菜单打开期间，
  // 点击菜单外部、按 Escape、窗口失焦、再次右键都会关闭菜单。
  useEffect(() => {
    const close = () => onCloseRef.current();
    const onMouseDown = (e: MouseEvent) => {
      // 点击落在菜单内部则忽略，否则视为"点击外部"关闭。
      if (ref.current && ref.current.contains(e.target as Node)) return;
      close();
    };
    window.addEventListener('mousedown', onMouseDown);
    window.addEventListener('keydown', onKeyClose);
    window.addEventListener('blur', close);
    window.addEventListener('contextmenu', close);
    // 卸载时移除全部监听器，避免泄漏。
    return () => {
      window.removeEventListener('mousedown', onMouseDown);
      window.removeEventListener('keydown', onKeyClose);
      window.removeEventListener('blur', close);
      window.removeEventListener('contextmenu', close);
    };
    // eslint-disable-next-line react-hooks/exhaustive-deps
  }, []);

  // onKeyClose — 按 Escape 关闭菜单。
  const onKeyClose = (e: KeyboardEvent) => {
    if (e.key === 'Escape') onCloseRef.current();
  };

  // 高亮项变化时，将其聚焦以便键盘继续操作。
  useEffect(() => {
    const el = itemRefs.current[activeIndex];
    if (el) el.focus();
  }, [activeIndex]);

  // handleKeyDown — 菜单内键盘导航：↑/↓ 循环切换高亮项，
  // Enter/空格 触发当前高亮项（分隔符不可触发）。
  const handleKeyDown = (e: React.KeyboardEvent) => {
    const n = itemIndices.length;
    if (n === 0) return;
    if (e.key === 'ArrowDown') {
      e.preventDefault();
      const cur = itemIndices.indexOf(activeIndex);
      setActiveIndex(itemIndices[(cur + 1) % n]);
    } else if (e.key === 'ArrowUp') {
      e.preventDefault();
      const cur = itemIndices.indexOf(activeIndex);
      setActiveIndex(itemIndices[(cur - 1 + n) % n]);
    } else if (e.key === 'Enter' || e.key === ' ') {
      e.preventDefault();
      const item = items[activeIndex];
      if (item && item !== 'separator') {
        item.onClick();
        onClose();
      }
    }
  };

  return (
    <div
      ref={ref}
      className="context-menu"
      style={{ left: pos.x, top: pos.y }}
      role="menu"
      onKeyDown={handleKeyDown}
    >
      {/* 渲染菜单项：分隔符渲染为横线，普通项渲染为可点击条目。 */}
      {items.map((item, i) =>
        item === 'separator' ? (
          <div key={`sep-${i}`} className="cm-sep" />
        ) : (
          <div
            key={`item-${i}`}
            ref={(el) => {
              itemRefs.current[i] = el;
            }}
            role="menuitem"
            tabIndex={-1}
            className={item.danger ? 'cm-item danger' : 'cm-item'}
            onClick={() => {
              item.onClick();
              onClose();
            }}
            onMouseEnter={() => setActiveIndex(i)}
          >
            {item.icon && <span>{item.icon}</span>}
            <span>{item.label}</span>
          </div>
        ),
      )}
    </div>
  );
}
