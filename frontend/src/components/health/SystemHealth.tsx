import { useWSStore } from '../../store/useWSStore';
import { fmtUptime } from '../../utils/helpers';
import styles from './Health.module.css';

const CIRC = 201.1; // 2*π*32
const CPU_CORES = navigator.hardwareConcurrency || 4;

function loadColor(load: number): string {
    const ratio = load / CPU_CORES;
    if (ratio < 0.5) return 'var(--green)';
    if (ratio < 0.85) return 'var(--yellow)';
    return 'var(--red)';
}

function pctColor(pct: number, threshLow = 50, threshHigh = 80, lowColor = 'var(--green)', midColor = 'var(--yellow)', highColor = 'var(--red)'): string {
    if (pct < threshLow) return lowColor;
    if (pct < threshHigh) return midColor;
    return highColor;
}

export function SystemHealth() {
    const { metrics, wsDelay } = useWSStore();
    const m = metrics;

    const cpuPct = m?.cpu_percent || 0;
    const cpuOffset = CIRC * (1 - cpuPct / 100);
    const memPct = m?.mem_percent || 0;
    const memOffset = CIRC * (1 - memPct / 100);

    const setLoad = (load: number) => ({
        width: `${Math.min(100, (load / CPU_CORES) * 100)}%`,
        background: loadColor(load),
    });

    return (
        <div className={styles.section}>
            <div className={styles.sectionTitle}>🖥 System Health</div>
            <div className={styles.grid}>

                {/* CPU Usage */}
                <div className={styles.card}>
                    <div className={styles.cardTitle}>🔥 CPU Usage</div>
                    <div className={styles.ringWrap}>
                        <svg className={styles.ringSvg} viewBox="0 0 80 80" style={{ transform: 'rotate(-90deg)' }}>
                            <circle className={styles.ringBg} cx="40" cy="40" r="32" />
                            <circle className={styles.ringFill} cx="40" cy="40" r="32"
                                strokeDasharray={CIRC} strokeDashoffset={cpuOffset}
                                style={{ stroke: pctColor(cpuPct), transform: 'none' }} />
                        </svg>
                        <div className={styles.ringStats}>
                            <div className={styles.ringPercent} style={{ color: pctColor(cpuPct) }}>
                                {cpuPct.toFixed(1)}%
                            </div>
                            <div className={styles.detail}>Cores: {m?.cpu_cores || '—'}</div>
                            <div className={styles.detail}>Load 1m: {(m?.cpu_load_1 || 0).toFixed(2)}</div>
                        </div>
                    </div>
                </div>

                {/* CPU Load */}
                <div className={styles.card}>
                    <div className={styles.cardTitle}>⚡ CPU Load Average</div>
                    {[
                        { label: '1m', load: m?.cpu_load_1 || 0 },
                        { label: '5m', load: m?.cpu_load_5 || 0 },
                        { label: '15m', load: m?.cpu_load_15 || 0 },
                    ].map(({ label, load }) => (
                        <div key={label} className={styles.loadRow}>
                            <span className={styles.loadLabel}>{label}</span>
                            <div className={styles.loadBarWrap}>
                                <div className={styles.loadBarFill} style={setLoad(load)} />
                            </div>
                            <span className={styles.loadVal} style={{ color: loadColor(load) }}>
                                {load.toFixed(2)}
                            </span>
                        </div>
                    ))}
                </div>

                {/* Memory */}
                <div className={styles.card}>
                    <div className={styles.cardTitle}>💾 System Memory</div>
                    <div className={styles.ringWrap}>
                        <svg className={styles.ringSvg} viewBox="0 0 80 80">
                            <circle className={styles.ringBg} cx="40" cy="40" r="32" />
                            <circle className={styles.ringFill} cx="40" cy="40" r="32"
                                strokeDasharray={CIRC} strokeDashoffset={memOffset}
                                style={{ stroke: pctColor(memPct, 60, 85, 'var(--cyan)', 'var(--yellow)', 'var(--red)') }} />
                        </svg>
                        <div className={styles.ringStats}>
                            <div className={styles.ringPercent} style={{ color: pctColor(memPct, 60, 85, 'var(--cyan)', 'var(--yellow)', 'var(--red)') }}>
                                {memPct.toFixed(1)}%
                            </div>
                            <div className={styles.detail}>Used: {(m?.mem_used_mb || 0).toFixed(0)} MB</div>
                            <div className={styles.detail}>Total: {(m?.mem_total_mb || 0).toFixed(0)} MB</div>
                            <div className={styles.detail}>Heap: {(m?.heap_alloc_mb || 0).toFixed(2)} MB</div>
                        </div>
                    </div>
                </div>

                {/* WS Delay */}
                <div className={styles.card}>
                    <div className={styles.cardTitle}>📡 WS Round-Trip Delay</div>
                    <div style={{ display: 'flex', alignItems: 'baseline', gap: 4, marginBottom: 10 }}>
                        <span className={styles.delayVal} style={{
                            color: wsDelay === null ? 'var(--text-muted)' : wsDelay < 50 ? 'var(--green)' : wsDelay < 200 ? 'var(--yellow)' : 'var(--red)',
                        }}>
                            {wsDelay ?? '—'}
                        </span>
                        <span className={styles.delayUnit}>ms</span>
                    </div>
                    <div className={styles.detail}>
                        {wsDelay !== null ? `Last ping: ${new Date().toLocaleTimeString('en-IN', { hour12: false })}` : 'Waiting for ping…'}
                    </div>
                </div>

                {/* Process Info */}
                <div className={styles.card}>
                    <div className={styles.cardTitle}>🔧 Process Info</div>
                    <div className={styles.procList}>
                        {[
                            { key: 'Goroutines', val: m?.goroutines ?? '—' },
                            { key: 'Heap Alloc', val: `${(m?.heap_alloc_mb || 0).toFixed(2)} MB` },
                            { key: 'Sys Memory', val: `${(m?.sys_mb || 0).toFixed(1)} MB` },
                            { key: 'GC Runs', val: m?.gc_runs ?? 0 },
                            { key: 'Server Uptime', val: fmtUptime(m?.uptime_sec || 0) },
                        ].map(({ key, val }) => (
                            <div key={key} className={styles.procRow}>
                                <span className={styles.procKey}>{key}</span>
                                <span className={styles.procVal}>{val}</span>
                            </div>
                        ))}
                    </div>
                </div>
            </div>
        </div>
    );
}
