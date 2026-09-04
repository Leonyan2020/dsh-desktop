import {useCallback, useEffect, useState} from 'react';
import './App.css';
import {
    BootstrapEnvironment,
    ConsumePendingView,
    DiscoverVersions,
    EnterHarness,
    GetAppInfo,
    GetReadyStatus,
    GetStatus,
    InstallVersion,
    ListLocalVersions,
    OpenConsoleLog,
    RestartHarness,
    StartHarness,
    StopHarness,
    SwitchVersion,
} from '../wailsjs/go/main/App';
import {EventsOn} from '../wailsjs/runtime/runtime';

type Status = {
    running: boolean;
    pid: number;
    port: number;
    version: string;
    runtime: string;
    url: string;
    listening: boolean;
    message: string;
};

type ReadyStatus = {
    hasNode: boolean;
    hasPnpm: boolean;
    hasRuntime: boolean;
    nodePath: string;
    pnpmPath: string;
    activeVersion: string;
    needsSetup: boolean;
    message: string;
};

type VersionInfo = {
    version: string;
    installed: boolean;
    active: boolean;
    legacy: boolean;
    path: string;
    tag: string;
};

type AppInfo = {
    name: string;
    version: string;
    dshHome: string;
    note: string;
};

type Mode = 'boot' | 'manage';

function harnessLabel(version?: string) {
    return version ? `Harness ${version}` : 'Harness —';
}

export default function App() {
    const [mode, setMode] = useState<Mode | null>(null);
    const [status, setStatus] = useState<Status | null>(null);
    const [ready, setReady] = useState<ReadyStatus | null>(null);
    const [local, setLocal] = useState<VersionInfo[]>([]);
    const [remote, setRemote] = useState<VersionInfo[]>([]);
    const [info, setInfo] = useState<AppInfo | null>(null);
    const [busy, setBusy] = useState(false);
    const [log, setLog] = useState('');
    const [err, setErr] = useState('');
    const [tab, setTab] = useState<'local' | 'remote'>('local');
    const [bootMsg, setBootMsg] = useState('正在检查运行环境…');

    const refreshStatus = useCallback(async () => {
        try {
            setStatus((await GetStatus()) as Status);
        } catch (e: any) {
            setErr(String(e));
        }
    }, []);

    const refreshReady = useCallback(async () => {
        try {
            setReady((await GetReadyStatus()) as ReadyStatus);
        } catch (e: any) {
            setErr(String(e));
        }
    }, []);

    const refreshLocal = useCallback(async () => {
        try {
            setLocal(((await ListLocalVersions()) || []) as VersionInfo[]);
        } catch (e: any) {
            setErr(String(e));
        }
    }, []);

    const goHarness = useCallback(async () => {
        setBootMsg('正在本窗口加载 Harness…');
        try {
            await EnterHarness();
        } catch (e: any) {
            setErr(String(e));
            setBootMsg('等待 Harness 就绪…');
        }
    }, []);

    useEffect(() => {
        let cancelled = false;
        (async () => {
            const view = (await ConsumePendingView()) as Mode;
            if (cancelled) return;
            setMode(view === 'manage' ? 'manage' : 'boot');
            GetAppInfo().then((v) => setInfo(v as AppInfo));
            await refreshReady();
            await refreshStatus();
            if (view === 'manage') await refreshLocal();
        })();
        return () => {
            cancelled = true;
        };
    }, [refreshLocal, refreshReady, refreshStatus]);

    // Boot: setup if needed → start → enter
    useEffect(() => {
        if (mode !== 'boot') return;
        let cancelled = false;

        (async () => {
            try {
                const r = (await GetReadyStatus()) as ReadyStatus;
                if (cancelled) return;
                setReady(r);
                if (r.needsSetup) {
                    setBootMsg('首次准备环境（Node / pnpm / Harness）…');
                    setLog('');
                    await BootstrapEnvironment();
                    await refreshReady();
                }
                setBootMsg('正在启动 dsh web…');
                await StartHarness();
            } catch (e: any) {
                if (!cancelled) {
                    setErr(String(e));
                    setBootMsg('环境准备失败，可重试或打开版本管理');
                }
            }
        })();

        const t = setInterval(() => {
            refreshStatus();
            refreshReady();
        }, 2500);
        EventsOn('status', (s: Status) => setStatus(s));
        EventsOn('ready-status', (s: ReadyStatus) => setReady(s));
        EventsOn('harness-ready', (s: Status) => {
            setStatus(s);
            if (!cancelled) goHarness();
        });
        EventsOn('install-progress', (line: string) =>
            setLog((prev) => (prev + line).slice(-12000)),
        );
        EventsOn('toast', (msg: string) => setErr(msg));

        return () => {
            cancelled = true;
            clearInterval(t);
        };
    }, [mode, refreshReady, refreshStatus, goHarness]);

    useEffect(() => {
        if (mode === 'boot' && status?.listening) {
            goHarness();
        }
    }, [mode, status?.listening, goHarness]);

    useEffect(() => {
        if (mode !== 'manage') return;
        const t = setInterval(refreshStatus, 4000);
        EventsOn('status', (s: Status) => setStatus(s));
        EventsOn('install-progress', (line: string) =>
            setLog((prev) => (prev + line).slice(-12000)),
        );
        EventsOn('versions-changed', () => {
            refreshLocal();
            refreshStatus();
            refreshReady();
        });
        EventsOn('toast', (msg: string) => setErr(msg));
        return () => clearInterval(t);
    }, [mode, refreshLocal, refreshReady, refreshStatus]);

    async function run(label: string, fn: () => Promise<any>) {
        setBusy(true);
        setErr('');
        try {
            await fn();
            await refreshStatus();
            await refreshLocal();
            await refreshReady();
        } catch (e: any) {
            setErr(`${label}: ${String(e)}`);
        } finally {
            setBusy(false);
        }
    }

    async function discover() {
        setBusy(true);
        setErr('');
        try {
            setRemote(((await DiscoverVersions()) || []) as VersionInfo[]);
            setTab('remote');
        } catch (e: any) {
            setErr(String(e));
        } finally {
            setBusy(false);
        }
    }

    async function install(v: string, switchAfter: boolean) {
        setLog('');
        await run(`安装 ${v}`, () => InstallVersion(v, switchAfter));
        await discover();
    }

    const verTitle = harnessLabel(status?.version || ready?.activeVersion);

    if (!mode) {
        return (
            <div className="boot">
                <div className="boot-card">
                    <div className="ver-hero">{verTitle}</div>
                    <p>准备中…</p>
                </div>
            </div>
        );
    }

    if (mode === 'boot') {
        return (
            <div className="boot">
                <div className="boot-card">
                    <div className="ver-hero">{verTitle}</div>
                    <p className="boot-msg">{bootMsg}</p>
                    <ul className="checklist">
                        <li className={ready?.hasNode ? 'ok' : ''}>Node {ready?.hasNode ? '就绪' : '待准备'}</li>
                        <li className={ready?.hasPnpm ? 'ok' : ''}>pnpm {ready?.hasPnpm ? '就绪' : '待准备'}</li>
                        <li className={ready?.hasRuntime ? 'ok' : ''}>
                            Harness {ready?.hasRuntime ? ready.activeVersion : '待安装'}
                        </li>
                    </ul>
                    <div className="boot-meta">
                        <span className={`dot ${status?.listening ? 'on' : status?.running ? 'warm' : 'off'}`} />
                        {status?.listening
                            ? `已就绪 · 端口 ${status.port}`
                            : status?.running
                              ? `热身中（约 1–3 分钟）· 端口 ${status.port}`
                              : ready?.message || '未运行'}
                    </div>
                    {err ? <div className="banner err">{err}</div> : null}
                    {log ? <pre className="boot-log">{log}</pre> : null}
                    <div className="boot-actions">
                        <button className="primary" disabled={busy} onClick={() => goHarness()}>
                            进入 Harness
                        </button>
                        <button
                            disabled={busy}
                            onClick={() =>
                                run('重新准备环境', async () => {
                                    setLog('');
                                    setBootMsg('重新准备环境…');
                                    await BootstrapEnvironment();
                                    await StartHarness();
                                })
                            }
                        >
                            重新准备环境
                        </button>
                        <button disabled={busy} onClick={() => setMode('manage')}>
                            版本管理
                        </button>
                        <button className="ghost" disabled={busy} onClick={() => run('日志', OpenConsoleLog)}>
                            日志
                        </button>
                    </div>
                </div>
            </div>
        );
    }

    const list = tab === 'local' ? local : remote.slice(0, 40);

    return (
        <div className="shell">
            <header className="top">
                <div>
                    <div className="ver-hero">{verTitle}</div>
                    <p className="sub">
                        壳 {info?.version || '…'} · {info?.note}
                    </p>
                </div>
                <div className="actions">
                    <button className="primary" disabled={busy} onClick={() => goHarness()}>
                        进入 Harness
                    </button>
                </div>
            </header>

            <section className="panel status">
                <div className="row">
                    <div>
                        <div className="label">运行状态</div>
                        <div className="value">
                            <span className={`dot ${status?.listening ? 'on' : status?.running ? 'warm' : 'off'}`} />
                            {status?.listening ? '监听中' : status?.running ? '启动中' : '已停止'}
                        </div>
                        <div className="detail">
                            版本 {status?.version || '—'} · 端口 {status?.port || 3080}
                            {status?.pid ? ` · PID ${status.pid}` : ''}
                        </div>
                        <div className="detail">
                            Node {ready?.hasNode ? '✓' : '✗'} · pnpm {ready?.hasPnpm ? '✓' : '✗'} · 运行时{' '}
                            {ready?.hasRuntime ? '✓' : '✗'}
                        </div>
                        {status?.message ? <div className="hint">{status.message}</div> : null}
                    </div>
                    <div className="actions">
                        <button disabled={busy} onClick={() => run('启动', StartHarness)}>启动</button>
                        <button disabled={busy} onClick={() => run('停止', StopHarness)}>停止</button>
                        <button disabled={busy} onClick={() => run('重启', RestartHarness)}>重启</button>
                        <button
                            disabled={busy}
                            className="ghost"
                            onClick={() =>
                                run('准备环境', async () => {
                                    setLog('');
                                    await BootstrapEnvironment();
                                })
                            }
                        >
                            准备环境
                        </button>
                        <button disabled={busy} className="ghost" onClick={() => run('日志', OpenConsoleLog)}>
                            日志
                        </button>
                    </div>
                </div>
            </section>

            <section className="panel versions">
                <div className="tabs">
                    <button className={tab === 'local' ? 'active' : ''} onClick={() => setTab('local')}>
                        已安装 ({local.length})
                    </button>
                    <button className={tab === 'remote' ? 'active' : ''} onClick={() => setTab('remote')}>
                        npm 发现
                    </button>
                    <div className="spacer" />
                    <button disabled={busy} onClick={discover}>刷新 npm</button>
                    <button disabled={busy} className="ghost" onClick={refreshLocal}>刷新本地</button>
                </div>

                <div className="table">
                    <div className="thead">
                        <span>版本</span>
                        <span>标签</span>
                        <span>状态</span>
                        <span>操作</span>
                    </div>
                    {list.length === 0 ? (
                        <div className="empty">
                            {tab === 'local'
                                ? '尚无本地运行时。首次启动会自动安装，也可从 npm 手动安装。'
                                : '点击「刷新 npm」拉取 @deepseek-ai/dsh 发布列表。'}
                        </div>
                    ) : (
                        list.map((v) => (
                            <div className="trow" key={`${tab}-${v.version}-${v.path}`}>
                                <span className="ver">
                                    {v.version}
                                    {v.legacy ? <em>旧目录</em> : null}
                                    {v.active ? <em className="act">当前</em> : null}
                                </span>
                                <span className={`tag ${v.tag}`}>{v.tag}</span>
                                <span>{v.installed ? '已安装' : '未安装'}</span>
                                <span className="ops">
                                    {!v.installed ? (
                                        <>
                                            <button disabled={busy} onClick={() => install(v.version, false)}>安装</button>
                                            <button disabled={busy} className="primary" onClick={() => install(v.version, true)}>
                                                安装并切换
                                            </button>
                                        </>
                                    ) : !v.active ? (
                                        <button
                                            disabled={busy}
                                            className="primary"
                                            onClick={() => run(`切换 ${v.version}`, () => SwitchVersion(v.version))}
                                        >
                                            切换
                                        </button>
                                    ) : (
                                        <span className="muted">使用中</span>
                                    )}
                                </span>
                            </div>
                        ))
                    )}
                </div>
            </section>

            {err ? <div className="banner err">{err}</div> : null}
            {log ? (
                <section className="panel log">
                    <div className="label">准备 / 安装输出</div>
                    <pre>{log}</pre>
                </section>
            ) : null}

            <p className="foot">发给他人：用 Setup 安装包；对方无需预装 Node。首次需联网下载 Harness。</p>
        </div>
    );
}
