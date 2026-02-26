import { useMemo } from 'react';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { tfLabel, entryKey } from '../../utils/helpers';
import { useChartInit } from './hooks/useChartInit';
import { useCandleSeries } from './hooks/useCandleSeries';
import { useIndicatorLines } from './hooks/useIndicatorLines';
import { useChartInteraction } from './hooks/useChartInteraction';
import { useChartSubscription } from './hooks/useChartSubscription';
import { ChartLegend } from './ChartLegend';
import styles from './Chart.module.css';

export function TradingChart() {
    // App state (atomic selectors)
    const config = useAppStore(s => s.config);
    const selectedToken = useAppStore(s => s.selectedToken);
    const selectedTF = useAppStore(s => s.selectedTF);
    const setSelectedTF = useAppStore(s => s.setSelectedTF);
    const activeIndicators = useAppStore(s => s.activeIndicators);

    // Show only indicators whose TF is <= chart TF (e.g. allow 3m on 5m, block >5m)
    const activeEntries = useMemo(
        () => {
            const chartTF = selectedTF || 60;
            return (activeIndicators || []).filter((e) => e.tf <= chartTF);
        },
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [selectedTF, JSON.stringify((activeIndicators || []).map(e => entryKey(e)))]
    );

    // Check if we have data to show
    const candles = useCandleStore(s => s.candles);
    const hasCandles = !!(candles[selectedTF || 60] && candles[selectedTF || 60].length > 0);

    // Chart hooks
    const { chartApi, candleSeries, indLineSeries, chartContainer } = useChartInit();
    useCandleSeries(candleSeries, selectedTF, selectedToken);
    useIndicatorLines(chartApi, indLineSeries, activeEntries, selectedTF, selectedToken);
    const { ohlcData, indValues } = useChartInteraction(chartApi, candleSeries, indLineSeries, chartContainer);
    useChartSubscription(selectedTF, selectedToken, activeEntries);

    return (
        <div className={styles.chartSection}>
            <div className={styles.sectionTitle}>📈 Price Chart</div>
            <div className={styles.chartCard}>
                {/* TF Bar */}
                <div className={styles.tfBar}>
                    <span className={styles.tfLabel}>Timeframe</span>
                    {config.tfs.map((tf) => (
                        <button
                            key={tf}
                            className={`${styles.tfPill} ${tf === selectedTF ? styles.active : ''}`}
                            onClick={() => setSelectedTF(tf)}
                        >
                            {tfLabel(tf)}
                        </button>
                    ))}
                </div>

                {/* Chart container */}
                <div ref={chartContainer} className={styles.chartContainer} />

                {/* Empty state */}
                {!hasCandles && (
                    <div className={styles.emptyState}>
                        Waiting for market data…
                    </div>
                )}

                {/* OHLC + Indicator Legend */}
                <ChartLegend
                    ohlcData={ohlcData}
                    indValues={indValues}
                    activeEntries={activeEntries}
                    indLineSeries={indLineSeries.current}
                    selectedTF={selectedTF}
                    selectedToken={selectedToken}
                />
            </div>
        </div>
    );
}
