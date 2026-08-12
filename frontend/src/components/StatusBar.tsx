// StatusBar.tsx — 底部状态栏组件
// 职责：展示代理运行状态（端口）、系统代理开关、流量总数、
// 证书未安装警告，以及当前选中流量的摘要信息。
// 交互模块：store（useApp：代理/证书状态；useFlows：流量列表与选中 id）。
import { Activity, Globe, ListFilter, ShieldAlert } from 'lucide-react';
import { useApp, useFlows } from '../store';
import { api } from '../services/api';
import type { Flow } from '../types';
import { useI18n } from '../i18n';

// StatusBar — 底部状态栏（无 props，全部数据来自全局 store）。
export default function StatusBar() {
  const { t } = useI18n();
  // 代理状态、证书是否已安装、全部流量与当前选中 id。
  const proxy = useApp((s) => s.proxy);
  const certInstalled = useApp((s) => s.certInstalled);
  const setCertInstalled = useApp((s) => s.setCertInstalled);
  const flows = useFlows((s) => s.flows);
  const selectedId = useFlows((s) => s.selectedId);

  // 根据 selectedId 从流量列表中取出选中的那条流量（无选中时为 undefined）。
  const selected: Flow | undefined = selectedId ? flows.find((f) => f.id === selectedId) : undefined;

  // 点击警告：立即向后端重新检测证书状态（安装授权可能刚完成）。
  const recheckCert = async () => {
    try {
      const ok = await api.isCertificateInstalled();
      setCertInstalled(ok);
    } catch {
      /* ignore */
    }
  };

  return (
    <div className="statusbar">
      {/* 代理运行状态：圆点高亮 + 端口信息。 */}
      <span className="sb-item">
        <span className={`dot${proxy.running ? ' on' : ''}`} />
        <span>{proxy.running ? t('statusbar.proxyOn', { port: proxy.port }) : t('statusbar.proxyOff')}</span>
      </span>

      {/* 系统代理开关状态。 */}
      <span className="sb-item">
        <Globe size={12} />
        <span>{proxy.systemProxy ? t('statusbar.systemProxyOn') : t('statusbar.systemProxyOff')}</span>
      </span>

      {/* 已捕获的请求总数（单复数显示）。 */}
      <span className="sb-item">
        <ListFilter size={12} />
        <span>
          {t(flows.length === 1 ? 'statusbar.request' : 'statusbar.requests', { count: flows.length })}
        </span>
      </span>

      {/* 证书未安装时的警告提示：点击可重新检测（安装授权可能刚完成）。 */}
      {!certInstalled && (
        <span className="sb-item" style={{ color: 'var(--warn)', cursor: 'pointer' }} title={t('statusbar.installCertTitle')} onClick={recheckCert}>
          <ShieldAlert size={12} />
          <span>{t('statusbar.httpsNotDecrypted')}</span>
        </span>
      )}

      {/* 有选中流量时，显示其方法、主机与响应状态码摘要。 */}
      {selected && (
        <span className="sb-item">
          <Activity size={12} />
          <span>
            {selected.method} {selected.host}
            {selected.responseStatus > 0 ? ` ${selected.responseStatus}` : ''}
          </span>
        </span>
      )}
    </div>
  );
}
