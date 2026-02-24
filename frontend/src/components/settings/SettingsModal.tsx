import { useState, useEffect, useCallback } from 'react';
import { useAppStore } from '../../store/useAppStore';
import { saveActiveConfig } from '../../services/api';
import { tfLabel, getIndColor, SMA_PALETTE, EMA_PALETTE, SMMA_PALETTE, getEntryColor } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import styles from './Settings.module.css';

interface Props {
    open: boolean;
    onClose: () => void;
}

export function SettingsModal({ open, onClose }: Props) {
    const { config, activeEntries, setActiveEntries } = useAppStore();
    const [draft, setDraft] = useState<IndicatorEntry[]>([]);
    const [indType, setIndType] = useState('SMA');
    const [period, setPeriod] = useState('');
    const [tfAdd, setTfAdd] = useState(config.tfs[0] || 60);
    const [color, setColor] = useState('#6366f1');

    // Sync draft with active entries when opening
    useEffect(() => {
        if (open) {
            setDraft(activeEntries.map((e) => ({ ...e })));
            setTfAdd(config.tfs[0] || 60);
        }
    }, [open, activeEntries, config.tfs]);

    // ESC key
    useEffect(() => {
        const handleEsc = (e: KeyboardEvent) => { if (e.key === 'Escape') onClose(); };
        document.addEventListener('keydown', handleEsc);
        return () => document.removeEventListener('keydown', handleEsc);
    }, [onClose]);

    const removeEntry = useCallback((idx: number) => {
        setDraft((d) => d.filter((_, i) => i !== idx));
    }, []);

    const addEntry = useCallback(() => {
        const p = parseInt(period);
        if (!p || p < 2 || p > 500) return;
        const name = indType + '_' + p;
        const exists = draft.some((e) => e.name === name && e.tf === tfAdd);
        if (exists) return;
        setDraft((d) => [...d, { name, tf: tfAdd, color }]);
        setPeriod('');
        const palettes = [...SMA_PALETTE, ...EMA_PALETTE, ...SMMA_PALETTE];
        setColor(palettes[(draft.length + 1) % palettes.length]);
    }, [indType, period, tfAdd, color, draft]);

    const handleApply = useCallback(async () => {
        try {
            await saveActiveConfig(draft);
            setActiveEntries(draft);
            onClose();
        } catch (e) {
            console.error('[settings] save error:', e);
            alert('Failed to save settings.');
        }
    }, [draft, setActiveEntries, onClose]);

    const handleReset = useCallback(() => {
        const tf = config.tfs[0] || 60;
        const serverInds = (config.indicators || []).filter((n) => !n.startsWith('RSI'));
        setDraft(serverInds.map((name) => ({ name, tf, color: getIndColor(name) })));
    }, [config]);

    // Sort draft
    const sorted = [...draft].sort((a, b) => {
        if (a.name !== b.name) return a.name.localeCompare(b.name);
        return a.tf - b.tf;
    });

    return (
        <div
            className={`${styles.overlay} ${open ? styles.open : ''}`}
            onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
        >
            <div className={styles.modal}>
                <div className={styles.header}>
                    <span className={styles.title}>⚡ Indicator Settings</span>
                    <button className={styles.closeBtn} onClick={onClose}>✕</button>
                </div>

                <div className={styles.body}>
                    {/* Active Indicators */}
                    <div className={styles.section}>
                        <div className={styles.sectionTitle}>
                            <span>📊</span> Active Indicators
                        </div>
                        <div className={styles.pillList}>
                            {sorted.map((entry, idx) => {
                                const type = entry.name.startsWith('SMA')
                                    ? styles.pillSma
                                    : entry.name.startsWith('EMA')
                                        ? styles.pillEma
                                        : entry.name.startsWith('SMMA')
                                            ? styles.pillSmma
                                            : '';
                                const dotColor = getEntryColor(entry);
                                const realIdx = draft.indexOf(entry);
                                return (
                                    <span key={`${entry.name}-${entry.tf}-${idx}`} className={`${styles.pill} ${type}`}>
                                        <span className={styles.pillDot} style={{ background: dotColor }} />
                                        {entry.name} · {tfLabel(entry.tf)}
                                        <button className={styles.pillRemove} onClick={() => removeEntry(realIdx)}>✕</button>
                                    </span>
                                );
                            })}
                        </div>
                    </div>

                    {/* Add Indicator */}
                    <div className={styles.section}>
                        <div className={styles.sectionTitle}>
                            <span>➕</span> Add Indicator
                        </div>
                        <div className={styles.addRow}>
                            <select className={styles.input} style={{ width: 85 }} value={indType} onChange={(e) => setIndType(e.target.value)}>
                                <option value="SMA">SMA</option>
                                <option value="EMA">EMA</option>
                                <option value="SMMA">SMMA</option>
                            </select>
                            <input
                                className={styles.input}
                                type="number" min={2} max={500}
                                placeholder="Period"
                                value={period}
                                onChange={(e) => setPeriod(e.target.value)}
                                onKeyDown={(e) => { if (e.key === 'Enter') addEntry(); }}
                            />
                            <select className={styles.input} style={{ width: 85 }} value={tfAdd} onChange={(e) => setTfAdd(parseInt(e.target.value))}>
                                {config.tfs.map((tf) => (
                                    <option key={tf} value={tf}>{tfLabel(tf)}</option>
                                ))}
                            </select>
                            <input
                                type="color"
                                className={styles.colorPick}
                                value={color}
                                onChange={(e) => setColor(e.target.value)}
                                title="Line color"
                            />
                            <button className={styles.addBtn} onClick={addEntry}>+ Add</button>
                        </div>
                    </div>
                </div>

                <div className={styles.footer}>
                    <button className={styles.resetBtn} onClick={handleReset}>Reset Defaults</button>
                    <button className={styles.applyBtn} onClick={handleApply}>Apply Changes</button>
                </div>
            </div>
        </div>
    );
}
