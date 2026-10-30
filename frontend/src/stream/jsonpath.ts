// jsonpath.ts — JSONPath 的常用子集实现（不引第三方依赖）。
//
// 支持：$、.key、["key"]、['key']、[0]、[-1]（从末尾计数）。
// 不支持通配符、递归下降、过滤表达式 —— 提取"增量字段"用不到那些，
// 保持实现小到可以一眼看明白。
//
// 用途：非标 SSE / NDJSON 的自定义解析规则（见 stream/parsers.ts）。

// tokenize 把路径切成段：字符串键或数字下标。
function tokenize(path: string): (string | number)[] {
  const out: (string | number)[] = [];
  let i = 0;
  if (path.startsWith('$')) i = 1;
  while (i < path.length) {
    const ch = path[i];
    if (ch === '.') {
      i++;
      let key = '';
      while (i < path.length && path[i] !== '.' && path[i] !== '[') {
        if (path[i] === '\\' && i + 1 < path.length) {
          key += path[i + 1];
          i += 2;
        } else {
          key += path[i++];
        }
      }
      if (key) out.push(key);
      continue;
    }
    if (ch === '[') {
      const end = path.indexOf(']', i);
      if (end < 0) break;
      let inner = path.slice(i + 1, end).trim();
      if (
        (inner.startsWith('"') && inner.endsWith('"')) ||
        (inner.startsWith("'") && inner.endsWith("'"))
      ) {
        out.push(inner.slice(1, -1));
      } else if (/^-?\d+$/.test(inner)) {
        out.push(Number(inner));
      } else if (inner) {
        out.push(inner); // 裸键名，宽松处理
      }
      i = end + 1;
      continue;
    }
    // 既不是 . 也不是 [：按整段键名处理（宽松）
    let key = '';
    while (i < path.length && path[i] !== '.' && path[i] !== '[') key += path[i++];
    if (key) out.push(key);
  }
  return out;
}

// jsonPathGet 按路径取值；任何一步取不到都返回 undefined。
export function jsonPathGet(root: unknown, path: string): unknown {
  if (!path) return undefined;
  let cur: unknown = root;
  for (const seg of tokenize(path)) {
    if (cur === null || cur === undefined) return undefined;
    if (typeof seg === 'number') {
      if (!Array.isArray(cur)) return undefined;
      const idx = seg < 0 ? cur.length + seg : seg;
      cur = cur[idx];
      continue;
    }
    if (typeof cur !== 'object') return undefined;
    cur = (cur as Record<string, unknown>)[seg];
  }
  return cur;
}

// jsonPathExists 判断路径是否存在（用于规则的协议嗅探）。
export function jsonPathExists(root: unknown, path: string): boolean {
  return jsonPathGet(root, path) !== undefined;
}

// parseJson 宽松解析：失败返回 undefined（SSE 的 data 常常是 [DONE] 或纯文本）。
export function parseJson(data: string): unknown {
  const s = data.trim();
  if (!s || (s[0] !== '{' && s[0] !== '[')) return undefined;
  try {
    return JSON.parse(s);
  } catch {
    return undefined;
  }
}
