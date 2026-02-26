import { useCandleStore } from '../../store/useCandleStore';
import { entryKey, getEntryColor, tfLabel } from '../../utils/helpers';
import type { OHLCData, IndCrosshairValue } from './hooks/useChartInteraction';
import type { IndicatorEntry } from '../../types/api';
import type { ISeriesApi } from 'lightweight-charts';
import styles from './Chart.module.css';

interface ChartLegendProps {
    ohlcData: OHLCData | null;
    indValues: IndCrosshairValue[];
    activeEntries: IndicatorEntry[];
    indLineSeries: Record<string, ISeriesApi<'Line'>>;
    selectedTF: number;
    selectedToken: string | null;
}

/**
 * OHLC display + indicator legend overlay for the chart.
 */
export function ChartLegend({
    ohlcData,
    indValues,
    activeEntries,
    indLineSeries,
    selectedTF,
    selectedToken,
}: ChartLegendProps) {
    const candles = useCandleStore(s => s.candles);

    // Get latest OHLC for when no crosshair is active
    const latestOHLC = (() => {
        if (ohlcData) return ohlcData;
        const tf = selectedTF || 60;
        const raw = candles[tf];
        if (!raw || raw.length === 0) return null;
        const token = selectedToken;
        const filtered = raw.filter(c => !token || (c.exchange + ':' + c.token) === token || c.token === token);
        if (filtered.length === 0) return null;
        const latest = filtered[0];
        return { open: latest.open, high: latest.high, low: latest.low, close: latest.close };
    })();

    const isUp = latestOHLC ? latestOHLC.close >= latestOHLC.open : true;
    const ohlcColor = isUp ? 'var(--green)' : 'var(--red)';
    const fmt = (v: number) => v?.toFixed(2) ?? '--';

    // Legend items from active indicator series
    const legendItems = Object.entries(indLineSeries).map(([key]) => {
        const entry = activeEntries.find(e => entryKey(e) === key);
        const parts = key.split(':');
        return {
            key,
            label: parts[0] + ' (' + tfLabel(parseInt(parts[1]) || 0) + ')',
            color: entry ? getEntryColor(entry) : '#6366f1',
        };
    });

    return (
        <>
            {/* OHLC Overlay */}
            <div className={styles.ohlc}>
                {latestOHLC && (
                    <div className={styles.ohlcRow}>
                        <span style={{ color: 'var(--text-muted)' }}>O</span>
                        <span style={{ color: ohlcColor, fontWeight: 600 }}>{fmt(latestOHLC.open)}</span>
                        <span style={{ color: 'var(--text-muted)' }}>H</span>
                        <span style={{ color: 'var(--green)', fontWeight: 600 }}>{fmt(latestOHLC.high)}</span>
                        <span style={{ color: 'var(--text-muted)' }}>L</span>
                        <span style={{ color: 'var(--red)', fontWeight: 600 }}>{fmt(latestOHLC.low)}</span>
                        <span style={{ color: 'var(--text-muted)' }}>C</span>
                        <span style={{ color: ohlcColor, fontWeight: 600 }}>{fmt(latestOHLC.close)}</span>
                    </div>
                )}
                {indValues.length > 0 && (
                    <div className={styles.indRow}>
                        {indValues.map((iv) => (
                            <span key={iv.key} style={{ color: iv.color, fontWeight: 600 }}>
                                {iv.label}: {iv.value.toFixed(2)}
                            </span>
                        ))}
                    </div>
                )}
            </div>

            {/* Legend */}
            <div className={styles.legend}>
                {legendItems.map((item) => (
                    <div key={item.key} className={styles.legendItem}>
                        <span style={{ width: 10, height: 3, borderRadius: 2, background: item.color, display: 'inline-block' }} />
                        <span style={{ color: item.color, fontWeight: 600 }}>{item.label}</span>
                    </div>
                ))}
            </div>
        </>
    );
}
