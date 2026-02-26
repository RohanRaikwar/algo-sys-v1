import { useState, useEffect, useCallback } from 'react';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { sendSubscribe } from '../../hooks/useWebSocket';
import { tfLabel, getIndColor, SMA_PALETTE, EMA_PALETTE, SMMA_PALETTE, getEntryColor, entryKey } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import styles from './Settings.module.css';

interface Props {
    open: boolean;
    onClose: () => void;
}

export function SettingsModal({ open, onClose }: Props) {
    const { config, selectedTF, activeIndicators, setActiveIndicators } = useAppStore();
    const chartTF = selectedTF || 60;

    const [draft, setDraft] = useState<IndicatorEntry[]>([]);
    const [indType, setIndType] = useState('SMA');
    const [period, setPeriod] = useState('');
    const [indTF, setIndTF] = useState(selectedTF);
    const [color, setColor] = useState('#6366f1');

    // Sync draft with global indicator list when opening
    useEffect(() => {
        if (open) {
            setDraft((activeIndicators || []).map((e) => ({ ...e })));
            setIndTF(chartTF);
        }
    }, [open, chartTF]); // eslint-disable-line react-hooks/exhaustive-deps

    // Clamp selected indicator TF when chart TF changes
    useEffect(() => {
        setIndTF((prev) => (prev <= chartTF ? prev : chartTF));
    }, [chartTF]);

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
        if (indTF > chartTF) return;
        const name = indType + '_' + p;
        // Check duplicates including TF
        const exists = draft.some((e) => e.name === name && e.tf === indTF);
        if (exists) return;
        setDraft((d) => [...d, { name, tf: indTF, color }]);
        setPeriod('');
        const palettes = [...SMA_PALETTE, ...EMA_PALETTE, ...SMMA_PALETTE];
        setColor(palettes[(draft.length + 1) % palettes.length]);
    }, [indType, period, indTF, color, draft, chartTF]);

    const allowedTFs = (config.tfs || [60, 120, 180, 300]).filter((t) => t <= chartTF);

    const handleApply = useCallback(() => {
        const entries = draft.map((e) => ({ ...e }));

        // Clear removed indicator data from candle store
        const keepNames = entries.map(e => e.name);
        // Clear for all TFs that had indicators
        const allTFs = new Set(entries.map(e => e.tf));
        for (const tf of allTFs) {
            useCandleStore.getState().clearIndicatorsForTF(tf, keepNames);
        }

        // Persist in tab-local store only
        setActiveIndicators(entries);

        // Re-subscribe with updated indicator profile via WS for this tab connection
        const token = useAppStore.getState().selectedToken;
        const tf = useAppStore.getState().selectedTF;
        if (token) {
            sendSubscribe(token, tf, entries);
        }

        onClose();
    }, [draft, setActiveIndicators, onClose]);

    const handleReset = useCallback(() => {
        setDraft([]);
    }, []);

    // Sort draft by name then TF
    const sorted = [...draft].sort((a, b) => {
        const cmp = a.name.localeCompare(b.name);
        return cmp !== 0 ? cmp : a.tf - b.tf;
    });

    return (
        <div
            className={`${styles.overlay} ${open ? styles.open : ''}`}
            onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
            role="dialog"
            aria-modal="true"
            aria-label="Indicator Settings"
        >
            <div className={styles.modal}>
                <div className={styles.header}>
                    <span className={styles.title}>⚡ Indicator Settings</span>
                    <button className={styles.closeBtn} onClick={onClose}>✕</button>
                </div>

                <div className={styles.body}>
                    {/* Active Indicators (global) */}
                    <div className={styles.section}>
                        <div className={styles.sectionTitle}>
                            <span>📊</span> Active Indicators
                        </div>
                        {sorted.length === 0 && (
                            <div style={{ color: 'rgba(255,255,255,0.4)', fontSize: '0.85rem', padding: '8px 0' }}>
                                No indicators configured. Add one below.
                            </div>
                        )}
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
                                    <span key={`${entryKey(entry)}-${idx}`} className={`${styles.pill} ${type}`}>
                                        <span className={styles.pillDot} style={{ background: dotColor }} />
                                        {entry.name}
                                        <span style={{ opacity: 0.6, fontSize: '0.75rem', marginLeft: 2 }}>({tfLabel(entry.tf)})</span>
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
                            <select
                                className={styles.input}
                                style={{ width: 70 }}
                                value={indTF}
                                onChange={(e) => setIndTF(Number(e.target.value))}
                                title="Compute timeframe"
                            >
                                {allowedTFs.map((t) => (
                                    <option key={t} value={t}>{tfLabel(t)}</option>
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
