// SettingsPanel.tsx —璁剧疆寮圭獥缁勪欢
// 鑱岃矗锛氫互妯℃€佹褰㈠紡闆嗕腑绠＄悊搴旂敤璁剧疆锛屽垎涓哄嚑涓尯鍧楋細
//   Proxy锛堜唬鐞嗭細绔彛/鍚仠/绯荤粺浠ｇ悊）€丼SL Proxying锛圫SL 鎷︽埅寮€鍏炽€丆A 璇佷功瀹夎/绉婚櫎/鏌ョ湅）€?//   Capture锛堟崟鑾凤細姝ｆ枃澶у皬涓婇檺）€丏evices（眬鍩熺綉 IP 鍒楄〃锛屼緵鎵嬫満/妯℃嫙鍣ㄩ厤缃唬鐞嗭級。//   Sessions锛堜細璇濓細淇濆瓨/鎵撳紑/瀵煎嚭/瀵煎叆）€乀heme锛堜富棰樺垏鎹級。// 浜や簰妯″潡：//   - store锛歶seApp锛堜唬鐞嗙姸鎬併€丼SL銆佽瘉涔︺€佽缃瓑锛屽惈瀵瑰簲 setter锛夛紱
//   - services/api锛氫唬鐞?璇佷功/璁剧疆/浼氳瘽鐩稿叧鍏ㄩ儴鍚庣鏂规硶。//   - 璇存槑：竷灞€鏍峰紡鍏ㄩ儴澶嶇敤 theme.css 鐨?CSS 鍙橀噺锛堟棤纭紪鐮侀鑹诧級銆
import { useCallback, useEffect, useState } from 'react';
import type { CSSProperties } from 'react';
import { X, ShieldCheck, ShieldAlert, Download, Upload, Save, FolderOpen, MonitorSmartphone, Globe } from 'lucide-react';
import { useApp } from '../store';
import { api } from '../services/api';
import { useI18n } from '../i18n';
import type { ProxyStatus, Settings } from '../types';

// MB —1 MiB 瀛楄妭鏁帮紙姝ｆ枃澶у皬涓婇檺鐨勬崲绠楀熀鍑嗭級銆
const MB = 1048576;

// --- 鍏变韩甯冨眬鍘熻锛堟秷璐?theme.css 鐨?token锛涙棤纭紪鐮侀鑹诧級---

// rowStyle —琛ㄥ崟琛屽竷灞€锛氭按骞虫帓鍒椼€佸彲鎹㈣銆佺粺涓€琛岃窛銆
const rowStyle: CSSProperties = {
  display: 'flex',
  alignItems: 'center',
  gap: 8,
  flexWrap: 'wrap',
  marginBottom: 8,
};

// hintStyle —杈呭姪璇存槑鏂囨湰鏍峰紡锛堢伆鑹插皬瀛楋級銆
const hintStyle: CSSProperties = {
  color: 'var(--text-tertiary)',
  fontSize: 11.5,
  marginTop: 6,
};

// monoStyle —绛夊瀛椾綋鏍峰紡锛堢敤浜?IP 鍦板潃绛夛級銆
const monoStyle: CSSProperties = {
  fontFamily: 'var(--font-mono)',
  fontSize: 12,
};

// statusStyle —鎿嶄綔缁撴灉鎻愮ず鏍峰紡锛氭垚鍔燂紙ok锛夌豢鑹诧紝澶辫触：ok锛夌孩鑹层€
function statusStyle(ok: boolean): CSSProperties {
  return {
    color: ok ? 'var(--ok)' : 'var(--error)',
    fontSize: 12,
    marginTop: 8,
    minHeight: 16,
  };
}

// OpMessage —鎿嶄綔缁撴灉娑堟伅锛歵ext（唴瀹癸級+ ok锛堟槸鍚︽垚鍔燂級锛宯ull 琛ㄧず鏃犳秷鎭€
type OpMessage = { text: string; ok: boolean } | null;

// Props —缁勪欢鍏ュ弬锛歰nClose（叧闂脊绐楃殑鍥炶皟）€
interface Props {
  onClose: () => void;
}

// SettingsPanel —璁剧疆寮圭獥缁勪欢。// 鍙傛暟锛歰nClose —鍏抽棴寮圭獥鍥炶皟锛堢偣鍑婚伄缃?鍏抽棴鎸夐挳/Escape 鏃惰Е鍙戯級銆
export default function SettingsPanel({ onClose }: Props) {
  // 鍥介檯鍖栵細缈昏瘧鍑芥暟 + 褰撳墠璇█ + 璇█鍒囨崲銆
const { t, lang, setLang } = useI18n();
  // 浠庡叏灞€ store 璇诲彇浠ｇ悊鐘舵€併€佽缃€丼SL銆佽瘉涔︾瓑鍙婂搴旀洿鏂板嚱鏁般€
const proxy = useApp((s) => s.proxy);
  const setProxy = useApp((s) => s.setProxy);
  const settings = useApp((s) => s.settings);
  const setSettings = useApp((s) => s.setSettings);
  const sslDefault = useApp((s) => s.sslDefault);
  const sslHosts = useApp((s) => s.sslHosts);
  const setSSL = useApp((s) => s.setSSL);
  const certInstalled = useApp((s) => s.certInstalled);
  const setCertInstalled = useApp((s) => s.setCertInstalled);

  // ---- Proxy 鍖哄潡鐨勬湰鍦扮姸鎬?----
  // 绔彛杈撳叆妗嗭紙鍒濆鍊煎彇鑷?store 璁剧疆）€佹槸鍚﹁鐢ㄦ埛鎵嬪姩鏀硅繃锛坉irty）€佸繖纰屼笌缁撴灉娑堟伅銆
const [portInput, setPortInput] = useState(() => String(useApp.getState().settings?.proxyPort ?? 0));
  const [portDirty, setPortDirty] = useState(false);
  const [proxyBusy, setProxyBusy] = useState(false);
  const [proxyMsg, setProxyMsg] = useState<OpMessage>(null);

  // ---- SSL 鍖哄潡鐨勬湰鍦扮姸鎬?----
  // 璇佷功瀹夎/绉婚櫎鐨勫繖纰屾爣璁般€丆A 璇佷功 PEM 鏂囨湰銆丼SL 鎿嶄綔缁撴灉娑堟伅銆
const [certBusy, setCertBusy] = useState(false);
  const [caPem, setCaPem] = useState('');
  const [sslMsg, setSslMsg] = useState<OpMessage>(null);

  // ---- Capture 鍖哄潡鐨勬湰鍦扮姸鎬?----
  // 姝ｆ枃澶у皬涓婇檺杈撳叆妗嗭紙MB 涓哄崟浣嶏級涓庝繚瀛樼粨鏋滄秷鎭€
const [bodyInput, setBodyInput] = useState(() => String(Math.round((useApp.getState().settings?.maxBodyBytes ?? MB) / MB)));
  const [captureMsg, setCaptureMsg] = useState<OpMessage>(null);

  // ---- Devices 鍖哄潡鐨勬湰鍦扮姸鎬?----
  // 灞€鍩熺綉 IP 鍒楄〃涓庡姞杞藉け璐ユ彁绀恒€
const [devices, setDevices] = useState<string[]>([]);
  const [devicesError, setDevicesError] = useState('');

  // ---- Sessions 鍖哄潡鐨勬湰鍦扮姸鎬?----
  // 浼氳瘽鎿嶄綔蹇欑鏍囪涓庣粨鏋滄秷鎭€
const [sessionBusy, setSessionBusy] = useState(false);
  const [sessionMsg, setSessionMsg] = useState<OpMessage>(null);

  // 纭繚 store 涓凡鏈夎缃紙寮圭獥鍙兘鍦?App.init 瀹屾垚鍓嶅氨琚墦寮€）€
useEffect(() => {
    if (!useApp.getState().settings) {
      api
        .getSettings()
        .then((s) => setSettings(s))
        .catch((err) => console.error('Failed to load settings', err));
    }
  }, [setSettings]);

  // 褰撴寔涔呭寲璁剧疆鍒拌揪鎴栧彉鍖栨椂鍚屾绔彛杈撳叆妗嗭紙浠呭綋鐢ㄦ埛鏈墜鍔ㄤ慨鏀硅繃）€
useEffect(() => {
    if (!portDirty) setPortInput(String(settings?.proxyPort ?? 0));
  }, [settings, portDirty]);

  // 璁剧疆鍙樺寲鏃跺悓姝ユ鏂囧ぇ灏忚緭鍏ユ锛圡B 鏄剧ず）€
useEffect(() => {
    setBodyInput(String(Math.round((settings?.maxBodyBytes ?? MB) / MB)));
  }, [settings]);

  // 鎸傝浇鏃跺姞杞戒竴娆″眬鍩熺綉 IP 鍒楄〃涓?CA 璇佷功 PEM：  // 鍗歌浇鏃堕€氳繃 cancelled 鏍囪閬垮厤寮傛缁撴灉鍐欏叆宸插嵏杞界殑缁勪欢銆
useEffect(() => {
    let cancelled = false;
    api
      .getLANIPs()
      .then((ips) => {
        if (!cancelled) setDevices(ips);
      })
      .catch((err) => {
        console.error('Failed to load LAN IPs', err);
        if (!cancelled) setDevicesError('Failed to load LAN IPs.');
      });
    api
      .getCACertPEM()
      .then((pem) => {
        if (!cancelled) setCaPem(pem);
      })
      .catch((err) => {
        console.error('Failed to load CA certificate', err);
        if (!cancelled) setCaPem('Failed to load certificate.');
      });
    return () => {
      cancelled = true;
    };
  }, []);

  // 鎸?Escape 閿叧闂脊绐椼€
useEffect(() => {
    const onKey = (e: KeyboardEvent) => {
      if (e.key === 'Escape') onClose();
    };
    window.addEventListener('keydown', onKey);
    return () => window.removeEventListener('keydown', onKey);
  }, [onClose]);

  // ---- Proxy 鍖哄潡鎿嶄綔 ----

  // refreshProxy —骞跺彂鏌ヨ浠ｇ悊杩愯鐘舵€?绔彛/绯荤粺浠ｇ悊寮€鍏筹紝
  // 姹囨€讳负 ProxyStatus 骞跺啓鍥?store锛涘け璐ヨ繑鍥?null 骞惰褰曟棩蹇椼€
const refreshProxy = useCallback(async (): Promise<ProxyStatus | null> => {
    try {
      const [running, port, sysProxy] = await Promise.all([
        api.isProxyRunning(),
        api.getProxyPort(),
        api.getSystemProxyEnabled(),
      ]);
      const status: ProxyStatus = { running, port, systemProxy: sysProxy };
      setProxy(status);
      return status;
    } catch (err) {
      console.error('Failed to refresh proxy state', err);
      return null;
    }
  }, [setProxy]);

  // handleStart —鍚姩浠ｇ悊锛氳В鏋愮鍙ｈ緭鍏ワ紙闈炴硶/璐熷€煎洖閫€涓?0 鍗宠嚜鍔ㄥ垎閰嶏級：  // 鍚姩鍚庡埛鏂扮姸鎬佸苟鎻愮ず瀹為檯绔彛銆
const handleStart = async () => {
    setProxyBusy(true);
    setProxyMsg(null);
    try {
      const requested = Number(portInput);
      const port = Number.isFinite(requested) && requested >= 0 ? requested : 0;
      await api.startProxy(port);
      const status = await refreshProxy();
      const actual = status?.port && status.port > 0 ? status.port : port || 0;
      setProxyMsg({ text: actual ? `Proxy started on port ${actual}.` : 'Proxy started (auto-assigned port).', ok: true });
    } catch (err) {
      console.error('Failed to start proxy', err);
      setProxyMsg({ text: t('settings.startFailed'), ok: false });
    } finally {
      setProxyBusy(false);
    }
  };

  // handleStop —鍋滄浠ｇ悊骞跺埛鏂扮姸鎬併€
const handleStop = async () => {
    setProxyBusy(true);
    setProxyMsg(null);
    try {
      await api.stopProxy();
      await refreshProxy();
      setProxyMsg({ text: t('settings.proxyStopped'), ok: true });
    } catch (err) {
      console.error('Failed to stop proxy', err);
      setProxyMsg({ text: t('settings.stopFailed'), ok: false });
    } finally {
      setProxyBusy(false);
    }
  };

  // handleSystemProxy —鍒囨崲绯荤粺浠ｇ悊寮€鍏筹紙鍙栧弽褰撳墠鍊硷級骞跺埛鏂扮姸鎬併€
const handleSystemProxy = async () => {
    setProxyBusy(true);
    setProxyMsg(null);
    try {
      const next = !proxy.systemProxy;
      await api.setSystemProxyEnabled(next);
      await refreshProxy();
      setProxyMsg({ text: next ? t('settings.systemProxyEnabled') : t('settings.systemProxyDisabled'), ok: true });
    } catch (err) {
      console.error('Failed to toggle system proxy', err);
      setProxyMsg({ text: t('settings.updateSystemProxyFailed'), ok: false });
    } finally {
      setProxyBusy(false);
    }
  };

  // ---- SSL 鍖哄潡鎿嶄綔 ----

  // handleSSLDefault —鍒囨崲榛樿 SSL 鎷︽埅寮€鍏筹紙host 涓虹┖涓茶〃绀哄叏灞€锛夛紝
  // 鎴愬姛鍚庡悓姝?store 骞舵彁绀恒€
const handleSSLDefault = async () => {
    const next = !sslDefault;
    try {
      await api.setSSLProxyEnabled('', next);
      setSSL(next, sslHosts);
      setSslMsg({ text: `Default SSL interception ${next ? 'enabled' : 'disabled'}.`, ok: true });
    } catch (err) {
      console.error('Failed to toggle SSL interception', err);
      setSslMsg({ text: t('settings.updateSystemProxyFailed'), ok: false });
    }
  };

  // refreshCert —鏌ヨ璇佷功鏄惁宸插畨瑁呭苟鍚屾 store锛涘け璐ヨ繑鍥?false銆
const refreshCert = useCallback(async (): Promise<boolean> => {
    try {
      const installed = await api.isCertificateInstalled();
      setCertInstalled(installed);
      return installed;
    } catch (err) {
      console.error('Failed to check certificate status', err);
      return false;
    }
  }, [setCertInstalled]);

  // handleInstallCert —瀹夎 CA 璇佷功鍒扮郴缁熶俊浠诲簱锛岄殢鍚庡埛鏂板畨瑁呯姸鎬佸苟鎻愮ず銆
const handleInstallCert = async () => {
    setCertBusy(true);
    setSslMsg(null);
    try {
      await api.installCertificate();
      const installed = await refreshCert();
      setSslMsg({
        text: installed ? 'Certificate installed.' : 'Certificate installation did not complete.',
        ok: installed,
      });
    } catch (err) {
      console.error('Failed to install certificate', err);
      // 显示后端返回的具体错误（如 Linux 的 sudo 指引），而不是通用文案。
      setSslMsg({ text: `${t('settings.installFailed')}\n${String(err)}`, ok: false });
    } finally {
      setCertBusy(false);
    }
  };

  // handleRemoveCert —浠庣郴缁熶俊浠诲簱绉婚櫎 CA 璇佷功锛岄殢鍚庡埛鏂扮姸鎬佸苟鎻愮ず銆
const handleRemoveCert = async () => {
    setCertBusy(true);
    setSslMsg(null);
    try {
      await api.removeCertificate();
      const installed = await refreshCert();
      setSslMsg({
        text: installed ? 'Certificate removal did not complete.' : 'Certificate removed.',
        ok: !installed,
      });
    } catch (err) {
      console.error('Failed to remove certificate', err);
      setSslMsg({ text: 'Failed to remove certificate.', ok: false });
    } finally {
      setCertBusy(false);
    }
  };

  // ---- Capture 鍖哄潡鎿嶄綔 ----

  // handleBodyBlur —姝ｆ枃澶у皬杈撳叆妗嗗け鐒︽椂淇濆瓨：  // 鏍￠獙鏁板€煎悎娉曟€э紝鏈彉鍖栧垯涓嶆彁浜わ紱淇濆瓨鎴愬姛鍚庢洿鏂?store 涓庢彁绀恒€
const handleBodyBlur = async () => {
    const mb = Number(bodyInput);
    // 闈炴硶鎴栬礋鏁拌緭鍏ワ細杩樺師涓哄綋鍓嶈缃€笺€
if (!settings || !Number.isFinite(mb) || mb < 0) {
      setBodyInput(String(Math.round((settings?.maxBodyBytes ?? MB) / MB)));
      return;
    }
    const maxBodyBytes = Math.round(mb * MB);
    // 鏁板€兼湭鍙樺垯璺宠繃銆
if (maxBodyBytes === settings.maxBodyBytes) return;
    try {
      const next: Settings = { ...settings, maxBodyBytes };
      await api.setSettings(next);
      setSettings(next);
      setCaptureMsg({ text: t('settings.maxBodySaved'), ok: true });
    } catch (err) {
      console.error('Failed to save max body size', err);
      setCaptureMsg({ text: t('settings.maxBodySaveFailed'), ok: false });
    }
  };

  // ---- Sessions 鍖哄潡鎿嶄綔 ----

  // doSessionOp —浼氳瘽缁熶竴鎿嶄綔鍏ュ彛：脊绐楄闂枃浠惰矾寰勫悗鎵ц瀵瑰簲鍚庣鎿嶄綔。  // op：save'锛堜繚瀛樹細璇濓級| 'open'锛堟墦寮€浼氳瘽锛墊 'export'（鍑?HAR锛墊 'import'（鍏?HAR）€
const doSessionOp = async (op: 'save' | 'open' | 'export' | 'import') => {
    const path = window.prompt('Enter file path:');
    if (!path) return; // 鐢ㄦ埛鍙栨秷鍒欎笉鍋氫换浣曚簨。    setSessionBusy(true);
    setSessionMsg(null);
    try {
      switch (op) {
        case 'save':
          await api.saveSession(path);
          break;
        case 'open':
          await api.openSession(path);
          break;
        case 'export':
          await api.exportHAR(path, []);
          break;
        case 'import':
          await api.importHAR(path);
          break;
      }
      setSessionMsg({ text: `Success: ${path}`, ok: true });
    } catch (err) {
      console.error(`Failed to ${op} session`, err);
      setSessionMsg({ text: `Failed to ${op} file.`, ok: false });
    } finally {
      setSessionBusy(false);
    }
  };

  // ---- Theme 鍖哄潡鎿嶄綔 ----

  // handleTheme —鍒囨崲涓婚：啓鍏ュ悗绔缃苟鏇存柊 store锛堢浉鍚屼富棰樺垯璺宠繃）€
const handleTheme = async (theme: string) => {
    if (!settings || theme === settings.theme) return;
    try {
      const next: Settings = { ...settings, theme };
      await api.setSettings(next);
      setSettings(next);
    } catch (err) {
      console.error('Failed to save theme', err);
    }
  };

  // 褰撳墠涓婚锛堥粯璁?dark）€
const theme = settings?.theme ?? 'dark';

  return (
    <div
      className="modal-overlay"
      onMouseDown={(e) => {
        // 鐐瑰嚮閬僵锛堥潪寮圭獥鏈綋锛夋椂鍏抽棴銆
if (e.target === e.currentTarget) onClose();
      }}
    >
      <div className="modal" style={{ width: 640 }}>
        <div className="modal-header">
          <span>{t('settings.title')}</span>
          <button type="button" className="tb-btn" onClick={onClose} title="Close settings" aria-label="Close settings">
            <X size={16} />
          </button>
        </div>

        <div className="modal-body">
          {/* ===== Proxy锛堜唬鐞嗭級鍖哄潡 ===== */}
          <div className="tool-card">
            <div className="tc-head">
              <Globe size={14} />
              <span className="tc-title">{t('settings.proxy')}</span>
              <span className={`tag ${proxy.running ? 'green' : 'amber'}`}>
                {proxy.running ? t('settings.runningOn', { port: proxy.port }) : t('settings.notRunning')}
              </span>
            </div>
            <div style={rowStyle}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <span style={{ color: 'var(--text-secondary)' }}>{t('settings.port')}</span>
                {/* 绔彛杈撳叆锛氫慨鏀瑰嵆鏍囪 dirty锛岄伩鍏嶈璁剧疆鍚屾瑕嗙洊。*/}
                <input
                  type="number"
                  className="input"
                  style={{ width: 90 }}
                  min={0}
                  max={65535}
                  value={portInput}
                  onChange={(e) => {
                    setPortDirty(true);
                    setPortInput(e.target.value);
                  }}
                  aria-label="Proxy port"
                />
              </label>
              {/* 鍚姩 / 鍋滄锛堣繍琛屼腑绂佺敤鍚姩锛屾湭杩愯绂佺敤鍋滄）€?*/}
              <button type="button" className="btn primary" onClick={handleStart} disabled={proxyBusy || proxy.running}>
                {t('settings.start')}
              </button>
              <button type="button" className="btn" onClick={handleStop} disabled={proxyBusy || !proxy.running}>
                {t('settings.stop')}
              </button>
              {/* 绯荤粺浠ｇ悊寮€鍏炽€?*/}
              <span style={{ display: 'flex', alignItems: 'center', gap: 6, marginLeft: 'auto' }}>
                <span style={{ color: 'var(--text-secondary)', fontSize: 12 }}>{t('settings.systemProxy')}</span>
                <button
                  type="button"
                  role="switch"
                  aria-checked={proxy.systemProxy}
                  aria-label={t('settings.systemProxy')}
                  className={`switch${proxy.systemProxy ? ' on' : ''}`}
                  onClick={handleSystemProxy}
                  disabled={proxyBusy}
                />
              </span>
            </div>
            <div style={hintStyle}>Port 0 picks an available port automatically.</div>
            {proxyMsg && <div style={statusStyle(proxyMsg.ok)}>{proxyMsg.text}</div>}
          </div>

          {/* ===== SSL Proxying锛圫SL 浠ｇ悊锛夊尯鍧?===== */}
          <div className="tool-card">
            <div className="tc-head">
              {/* 璇佷功瀹夎鐘舵€佸浘鏍囷細宸茶缁胯壊鐩剧墝锛屾湭瑁呴粍鑹茶绀恒€?*/}
              {certInstalled ? (
                <ShieldCheck size={14} style={{ color: 'var(--ok)' }} />
              ) : (
                <ShieldAlert size={14} style={{ color: 'var(--warn)' }} />
              )}
              <span className="tc-title">{t('settings.sslProxying')}</span>
              <span className={`tag ${certInstalled ? 'green' : 'amber'}`}>
                {certInstalled ? t('settings.installed') : t('settings.notInstalled')}
              </span>
            </div>
            {/* 榛樿 SSL 鎷︽埅寮€鍏炽€?*/}
            <div style={rowStyle}>
              <span style={{ color: 'var(--text-secondary)' }}>{t('settings.defaultSSLInterception')}</span>
              <button
                type="button"
                role="switch"
                aria-checked={sslDefault}
                aria-label={t('settings.defaultSSLInterception')}
                className={`switch${sslDefault ? ' on' : ''}`}
                onClick={handleSSLDefault}
              />
            </div>
            {/* 瀹夎 / 绉婚櫎璇佷功锛堟寜褰撳墠鐘舵€佷簰鏂ョ鐢級。*/}
            <div style={rowStyle}>
              <button
                type="button"
                className="btn primary"
                onClick={handleInstallCert}
                disabled={certBusy || certInstalled}
                title={certInstalled ? 'The certificate is already trusted by the system' : 'Install the CA into the system trust store'}
              >
                {certBusy ? t('settings.installing') : certInstalled ? t('settings.installedCheck') : t('settings.installCertificate')}
              </button>
              <button type="button" className="btn" onClick={handleRemoveCert} disabled={certBusy || !certInstalled}>
                Remove
              </button>
            </div>
            {/* 璇佷功鏈畨瑁呮椂鐨勬彁绀猴細HTTPS 娴侀噺浠呴毀閬撻€忎紶锛堜笉瑙ｅ瘑）€?*/}
            {!certInstalled && (
              <div style={{ color: 'var(--text-tertiary)', fontSize: 11.5, marginTop: 4 }}>
                HTTPS traffic is tunnelled (not decrypted) until the certificate is installed.
              </div>
            )}
            {/* 鍙姌鍙犵殑 CA 璇佷功 PEM 鏌ョ湅鍖恒€?*/}
            <details>
              <summary style={{ cursor: 'pointer', color: 'var(--text-secondary)', fontSize: 12.5 }}>{t('settings.viewCACert')}</summary>
              <pre
                style={{
                  maxHeight: 180,
                  overflow: 'auto',
                  marginTop: 8,
                  padding: 10,
                  background: 'var(--bg-input)',
                  border: '1px solid var(--border)',
                  borderRadius: 6,
                  fontFamily: 'var(--font-mono)',
                  fontSize: 11.5,
                  color: 'var(--text-secondary)',
                  whiteSpace: 'pre-wrap',
                  wordBreak: 'break-all',
                }}
              >
                {caPem || 'Loading certificate...'}
              </pre>
            </details>
            {sslMsg && <div style={statusStyle(sslMsg.ok)}>{sslMsg.text}</div>}
          </div>

          {/* ===== Capture锛堟崟鑾凤級鍖哄潡 ===== */}
          <div className="tool-card">
            <div className="tc-head">
              <span className="tc-title">{t('settings.capture')}</span>
            </div>
            {/* 姝ｆ枃鎹曡幏澶у皬涓婇檺锛圡B锛夛紝澶辩劍鏃朵繚瀛樸€?*/}
            <div style={rowStyle}>
              <label style={{ display: 'flex', alignItems: 'center', gap: 6 }}>
                <span style={{ color: 'var(--text-secondary)' }}>{t('settings.maxBodySize')}</span>
                <input
                  type="number"
                  className="input"
                  style={{ width: 90 }}
                  min={0}
                  value={bodyInput}
                  onChange={(e) => setBodyInput(e.target.value)}
                  onBlur={handleBodyBlur}
                  aria-label="Maximum captured body size in MB"
                />
              </label>
              <span style={{ color: 'var(--text-tertiary)' }}>{t('settings.mb')}</span>
            </div>
            {captureMsg && <div style={statusStyle(captureMsg.ok)}>{captureMsg.text}</div>}
          </div>

          {/* ===== Devices锛堣澶囷級鍖哄潡 ===== */}
          <div className="tool-card">
            <div className="tc-head">
              <MonitorSmartphone size={14} />
              <span className="tc-title">{t('settings.devices')}</span>
            </div>
            {/* 灞€鍩熺綉 IP 鍒楄〃锛涘姞杞藉け璐ユ垨涓虹┖鏃舵樉绀哄搴旀彁绀恒€?*/}
            {devices.length > 0 ? (
              <ul style={{ listStyle: 'none', margin: 0, padding: 0 }}>
                {devices.map((ip) => (
                  <li key={ip} style={{ ...monoStyle, padding: '2px 0', color: 'var(--text-primary)' }}>
                    {ip}
                  </li>
                ))}
              </ul>
            ) : (
              <div style={hintStyle}>{devicesError || t('settings.noLanAddresses')}</div>
            )}
            <div style={hintStyle}>{t('settings.devicesHint')}</div>
          </div>

          {/* ===== Sessions（会话）区块 ===== */}
          <div className="tool-card">
            <div className="tc-head">
              <span className="tc-title">{t('settings.sessions')}</span>
            </div>
            {/* 会话操作按钮：保存/打开会话、导出/导入 HAR（均需输入文件路径）。*/}
            <div style={rowStyle}>
              <button type="button" className="btn" onClick={() => doSessionOp('save')} disabled={sessionBusy}>
                <Save size={14} />
                {t('settings.saveSession')}
              </button>
              <button type="button" className="btn" onClick={() => doSessionOp('open')} disabled={sessionBusy}>
                <FolderOpen size={14} />
                {t('settings.openSession')}
              </button>
              <button type="button" className="btn" onClick={() => doSessionOp('export')} disabled={sessionBusy}>
                <Download size={14} />
                {t('settings.exportHAR')}
              </button>
              <button type="button" className="btn" onClick={() => doSessionOp('import')} disabled={sessionBusy}>
                <Upload size={14} />
                {t('settings.importHAR')}
              </button>
            </div>
            {sessionMsg && <div style={statusStyle(sessionMsg.ok)}>{sessionMsg.text}</div>}
          </div>

          {/* ===== Theme（主题）区块 ===== */}
          <div className="tool-card">
            <div className="tc-head">
              <span className="tc-title">{t('settings.theme')}</span>
            </div>
            {/* 主题下拉选择：dark / light，切换即保存。*/}
            <div style={rowStyle}>
              <select className="input" value={theme} onChange={(e) => handleTheme(e.target.value)} aria-label={t('settings.theme')}>
                <option value="dark">{t('settings.dark')}</option>
                <option value="light">{t('settings.light')}</option>
              </select>
            </div>
          </div>

          {/* ===== Language（语言）区块 ===== */}
          <div className="tool-card">
            <div className="tc-head">
              <span className="tc-title">{t('settings.language')}</span>
            </div>
            {/* 语言选择：跟随系统 / 简体中文 / English。*/}
            <div style={rowStyle}>
              <select
                className="input"
                value={lang}
                onChange={(e) => setLang(e.target.value as 'en' | 'zh-CN')}
                aria-label={t('settings.language')}
              >
                <option value="en">{t('settings.langEnglish')}</option>
                <option value="zh-CN">{t('settings.langChinese')}</option>
              </select>
            </div>
          </div>
        </div>

        <div className="modal-footer">
          <button type="button" className="btn" onClick={onClose}>
            {t('settings.close')}
          </button>
        </div>
      </div>
    </div>
  );
}
