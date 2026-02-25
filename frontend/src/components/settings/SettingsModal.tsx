import { useState, useEffect, useCallback } from 'react';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { saveActiveConfig } from '../../services/api';
import { sendSubscribe } from '../../hooks/useWebSocket';
import { tfLabel, getIndColor, SMA_PALETTE, EMA_PALETTE, SMMA_PALETTE, getEntryColor } from '../../utils/helpers';
import type { IndicatorEntry } from '../../types/api';
import styles from './Settings.module.css';

interface Props {
    open: boolean;
    onClose: () => void;
}

export function SettingsModal({ open, onClose }: Props) {
    const { config, selectedTF, activeEntriesByTF, setActiveEntriesForTF } = useAppStore();
    const currentEntries = activeEntriesByTF[selectedTF] || [];

    const [draft, setDraft] = useState<IndicatorEntry[]>([]);
    const [indType, setIndType] = useState('SMA');
    const [period, setPeriod] = useState('');
    const [color, setColor] = useState('#6366f1');

    // Sync draft with entries for the selected TF when opening
    useEffect(() => {
        if (open) {
            setDraft(currentEntries.map((e) => ({ ...e })));
        }
    }, [open, selectedTF]); // eslint-disable-line react-hooks/exhaustive-deps

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
        // All entries are for the current TF
        const exists = draft.some((e) => e.name === name);
        if (exists) return;
        setDraft((d) => [...d, { name, tf: selectedTF, color }]);
        setPeriod('');
        const palettes = [...SMA_PALETTE, ...EMA_PALETTE, ...SMMA_PALETTE];
        setColor(palettes[(draft.length + 1) % palettes.length]);
    }, [indType, period, selectedTF, color, draft]);

    const handleApply = useCallback(async () => {
        try {
            // Ensure all draft entries have the correct TF
            const entries = draft.map((e) => ({ ...e, tf: selectedTF }));
            // Save per-TF config to backend
            const allProfiles = { ...activeEntriesByTF, [selectedTF]: entries };
            await saveActiveConfig(allProfiles);

            // Clear removed indicator data from candle store immediately
            // This prevents stale indicator lines from flickering during re-subscribe
            const keepNames = entries.map(e => e.name);
            useCandleStore.getState().clearIndicatorsForTF(selectedTF, keepNames);

            setActiveEntriesForTF(selectedTF, entries);

            // Re-subscribe with updated indicator profile via WS
            const token = useAppStore.getState().selectedToken;
            if (token && entries.length > 0) {
                sendSubscribe(token, selectedTF, entries);
            }

            onClose();
        } catch (e) {
            console.error('[settings] save error:', e);
            alert('Failed to save settings.');
        }
    }, [draft, selectedTF, activeEntriesByTF, setActiveEntriesForTF, onClose]);

    const handleReset = useCallback(() => {
        const serverInds = (config.indicators || []).filter((n) => !n.startsWith('RSI'));
        setDraft(serverInds.map((name) => ({ name, tf: selectedTF, color: getIndColor(name) })));
    }, [config, selectedTF]);

    // Sort draft
    const sorted = [...draft].sort((a, b) => a.name.localeCompare(b.name));

    return (
        <div
            className={`${styles.overlay} ${open ? styles.open : ''}`}
            onClick={(e) => { if (e.target === e.currentTarget) onClose(); }}
        >
            <div className={styles.modal}>
                <div className={styles.header}>
                    <span className={styles.title}>⚡ Indicator Settings — {tfLabel(selectedTF)}</span>
                    <button className={styles.closeBtn} onClick={onClose}>✕</button>
                </div>

                <div className={styles.body}>
                    {/* Active Indicators for this TF */}
                    <div className={styles.section}>
                        <div className={styles.sectionTitle}>
                            <span>📊</span> Active Indicators for {tfLabel(selectedTF)}
                        </div>
                        {sorted.length === 0 && (
                            <div style={{ color: 'rgba(255,255,255,0.4)', fontSize: '0.85rem', padding: '8px 0' }}>
                                No indicators configured for {tfLabel(selectedTF)}. Add one below.
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
                                    <span key={`${entry.name}-${idx}`} className={`${styles.pill} ${type}`}>
                                        <span className={styles.pillDot} style={{ background: dotColor }} />
                                        {entry.name}
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
