import { Settings } from 'lucide-react';
import { useAppStore } from '../../store/useAppStore';
import { useWSStore } from '../../store/useWSStore';
import { tfLabel } from '../../utils/helpers';
import styles from './Header.module.css';

interface Props {
    onOpenSettings: () => void;
}

export function Header({ onOpenSettings }: Props) {
    const config = useAppStore(s => s.config);
    const selectedToken = useAppStore(s => s.selectedToken);
    const selectedTF = useAppStore(s => s.selectedTF);
    const setSelectedToken = useAppStore(s => s.setSelectedToken);
    const setSelectedTF = useAppStore(s => s.setSelectedTF);
    const connected = useWSStore(s => s.connected);
    const marketOpen = useWSStore(s => s.marketOpen);
    const marketStatus = useWSStore(s => s.marketStatus);

    return (
        <header className={styles.header}>
            <div className={styles.headerLeft}>
                <span className={styles.logo}>⚡ TradingPulse</span>
                <span className={styles.logoSub}>Real-Time Indicators</span>
            </div>
            <div className={styles.headerRight}>
                <select
                    className={styles.select}
                    value={selectedToken || ''}
                    onChange={(e) => setSelectedToken(e.target.value)}
                >
                    {config.tokens.map((t) => (
                        <option key={t} value={t}>{t}</option>
                    ))}
                </select>

                <select
                    className={styles.select}
                    value={selectedTF}
                    onChange={(e) => setSelectedTF(parseInt(e.target.value))}
                >
                    {config.tfs.map((tf) => (
                        <option key={tf} value={tf}>{tfLabel(tf)}</option>
                    ))}
                </select>

                <button className={styles.settingsBtn} onClick={onOpenSettings} title="Indicator Settings" aria-label="Indicator Settings">
                    <Settings size={16} />
                </button>

                <div className={`${styles.badge} ${marketOpen ? styles.connected : styles.disconnected}`} title={marketStatus}>
                    <span className={styles.dot} />
                    <span>{marketOpen ? 'Market Open' : 'Market Closed'}</span>
                </div>

                <div className={`${styles.badge} ${connected ? styles.connected : styles.disconnected}`}>
                    <span className={styles.dot} />
                    <span>{connected ? 'Live' : 'Reconnecting…'}</span>
                </div>
            </div>
        </header>
    );
}
