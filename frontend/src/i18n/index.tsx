// 国际化（i18n）基础设施：轻量自研方案，无第三方依赖。
//
// 能力：
//   - 双语资源（英文 en / 简体中文 zh-CN），结构完全一致；
//   - 语言自动检测：优先读取 localStorage 中用户手动选择（wpx-lang），
//     否则跟随系统语言（navigator.language，如 zh-CN/zh → 中文）；
//   - t(key) 函数：按 "命名空间.键" 点路径查找；中文缺键时回退英文；
//   - React 集成：<LanguageProvider> 包裹应用，useI18n() 提供
//     { t, lang, setLang }。
//
// 使用示例（组件内）：
//   const { t } = useI18n();
//   <span>{t('toolbar.search')}</span>
import { createContext, useCallback, useContext, useEffect, useMemo, useState } from 'react';
import type { ReactNode } from 'react';
import { en } from './en';
import { zhCN } from './zh-CN';

// Dict 是语言包结构：允许任意深度的嵌套对象或字符串。
export interface Dict {
  [key: string]: string | Dict;
}

// 支持的语言（key 与 navigator.language 前缀匹配）。
export type Lang = 'en' | 'zh-CN';

const DICTS: Record<Lang, Dict> = {
  en: en as unknown as Dict,
  'zh-CN': zhCN as unknown as Dict,
};

const STORAGE_KEY = 'wpx-lang';

// detectLang 决定初始语言：localStorage 优先，其次跟随系统。
function detectLang(): Lang {
  try {
    const stored = localStorage.getItem(STORAGE_KEY);
    if (stored === 'en' || stored === 'zh-CN') return stored;
  } catch {
    /* localStorage 不可用时忽略 */
  }
  const sys = (typeof navigator !== 'undefined' ? navigator.language : '') || '';
  return sys.toLowerCase().startsWith('zh') ? 'zh-CN' : 'en';
}

// lookup 按点路径在字典中查找，找不到时回退到英文。
function lookup(dict: Dict, key: string): string {
  let node: string | Dict = dict;
  for (const part of key.split('.')) {
    if (node && typeof node === 'object' && part in node) {
      node = node[part];
    } else {
      return '';
    }
  }
  return typeof node === 'string' ? node : '';
}

function translate(key: string, lang: Lang): string {
  const found = lookup(DICTS[lang], key);
  if (found) return found;
  // 回退英文（英文一定完整）。
  const enFound = lookup(DICTS.en, key);
  return enFound || key;
}

// interpolate 把模板中的 {name} 占位符替换为参数值。
function interpolate(template: string, params?: Record<string, string | number>): string {
  if (!params) return template;
  return template.replace(/\{(\w+)\}/g, (_, name: string) =>
    name in params ? String(params[name]) : `{${name}}`,
  );
}

interface I18nContextValue {
  // t 根据当前语言翻译；支持可选参数插值，例如 t('statusbar.proxyOn', { port: 8080 })。
  t: (key: string, params?: Record<string, string | number>) => string;
  lang: Lang;
  setLang: (lang: Lang) => void;
}

const I18nContext = createContext<I18nContextValue>({
  t: (k: string) => k,
  lang: 'en',
  setLang: () => {},
});

// useI18n 返回翻译函数与语言状态。
export function useI18n(): I18nContextValue {
  return useContext(I18nContext);
}

// LanguageProvider 包裹应用根，提供语言上下文并持久化用户选择。
export function LanguageProvider({ children }: { children: ReactNode }) {
  const [lang, setLangState] = useState<Lang>(detectLang);

  // 切换语言：持久化到 localStorage，并更新文档标题。
  const setLang = useCallback((next: Lang) => {
    setLangState(next);
    try {
      localStorage.setItem(STORAGE_KEY, next);
    } catch {
      /* ignore */
    }
  }, []);

  // 语言变化时更新窗口标题。
  useEffect(() => {
    document.title = translate('app.title', lang);
  }, [lang]);

  const t = useCallback(
    (key: string, params?: Record<string, string | number>) => interpolate(translate(key, lang), params),
    [lang],
  );

  const value = useMemo(() => ({ t, lang, setLang }), [t, lang, setLang]);
  return <I18nContext.Provider value={value}>{children}</I18nContext.Provider>;
}

// 导出翻译函数（非组件场景也可用）。
export function getT(lang: Lang) {
  return (key: string, params?: Record<string, string | number>) => interpolate(translate(key, lang), params);
}
