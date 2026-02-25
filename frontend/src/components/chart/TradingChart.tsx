import { useEffect, useRef, useCallback, useState, useMemo } from 'react';
import {
    createChart, CrosshairMode, PriceScaleMode, ColorType,
    type IChartApi, type ISeriesApi, type CandlestickData, type LineData, type MouseEventParams, type LineWidth,
} from 'lightweight-charts';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { tfLabel, IST_OFFSET, CHART_MAX, entryKey, getEntryColor } from '../../utils/helpers';
import { sendSubscribe } from '../../hooks/useWebSocket';
import styles from './Chart.module.css';

export function TradingChart() {
    const chartRef = useRef<HTMLDivElement>(null);
    const chartApi = useRef<IChartApi | null>(null);
    const candleSeries = useRef<ISeriesApi<'Candlestick'> | null>(null);
    const indLineSeries = useRef<Record<string, ISeriesApi<'Line'>>>({});
    const priceMargins = useRef({ top: 0.1, bottom: 0.1 });
    // Track last confirmed history fingerprint per indicator to skip unnecessary setData()
    const lastHistoryFingerprint = useRef<Record<string, string>>({});
    // Track candle count for incremental vs full updates
    const lastCandleFingerprint = useRef<string>('');

    const { config, selectedToken, selectedTF, activeEntriesByTF, setSelectedTF } = useAppStore();
    const activeEntriesRaw = activeEntriesByTF[selectedTF];
    const activeEntries = useMemo(
        () => activeEntriesRaw || [],
        // Only recompute when the serialized entry keys actually change
        // eslint-disable-next-line react-hooks/exhaustive-deps
        [JSON.stringify((activeEntriesRaw || []).map(e => entryKey(e)))]
    );
    const candles = useCandleStore((s) => s.candles);
    const indicators = useCandleStore((s) => s.indicators);

    const [ohlcData, setOhlcData] = useState<{ open: number; high: number; low: number; close: number } | null>(null);
    const [indValues, setIndValues] = useState<Array<{ key: string; label: string; value: number; color: string }>>([]);

    // Initialize chart
    useEffect(() => {
        if (!chartRef.current) return;

        const chart = createChart(chartRef.current, {
            layout: {
                background: { type: ColorType.Solid, color: '#0f172a' },
                textColor: '#9ca3af',
                fontFamily: "'Inter', sans-serif",
                fontSize: 11,
            },
            grid: {
                vertLines: { color: 'rgba(99, 102, 241, 0.06)' },
                horzLines: { color: 'rgba(99, 102, 241, 0.06)' },
            },
            crosshair: {
                mode: CrosshairMode.Normal,
                vertLine: { color: 'rgba(99, 102, 241, 0.3)', width: 1, style: 2, labelBackgroundColor: '#6366f1' },
                horzLine: { color: 'rgba(99, 102, 241, 0.3)', width: 1, style: 2, labelBackgroundColor: '#6366f1' },
            },
            rightPriceScale: {
                borderColor: 'rgba(99, 102, 241, 0.15)',
                scaleMargins: { top: 0.1, bottom: 0.1 },
                mode: PriceScaleMode.Normal,
                autoScale: true,
            },
            timeScale: {
                borderColor: 'rgba(99, 102, 241, 0.15)',
                timeVisible: true,
                secondsVisible: false,
                rightOffset: 5,
            },
            handleScroll: { mouseWheel: true, pressedMouseMove: true, horzTouchDrag: true, vertTouchDrag: true },
            handleScale: {
                mouseWheel: true, pinch: true,
                axisPressedMouseMove: { price: true, time: true },
                axisDoubleClickReset: { price: true, time: true },
            },
            width: chartRef.current.clientWidth,
            height: 460,
        });

        chartApi.current = chart;
        candleSeries.current = chart.addCandlestickSeries({
            upColor: '#10b981', downColor: '#ef4444',
            borderDownColor: '#ef4444', borderUpColor: '#10b981',
            wickDownColor: 'rgba(239, 68, 68, 0.5)', wickUpColor: 'rgba(16, 185, 129, 0.5)',
        });

        // Resize
        const ro = new ResizeObserver(() => {
            if (chartRef.current) chart.applyOptions({ width: chartRef.current.clientWidth });
        });
        ro.observe(chartRef.current);

        // Crosshair
        chart.subscribeCrosshairMove((param: MouseEventParams) => {
            if (param.time && param.seriesData && candleSeries.current) {
                const d = param.seriesData.get(candleSeries.current) as CandlestickData | undefined;
                if (d) setOhlcData({ open: d.open, high: d.high, low: d.low, close: d.close });
            } else {
                setOhlcData(null);
            }

            // Indicator values
            const vals: Array<{ key: string; label: string; value: number; color: string }> = [];
            for (const [compositeKey, series] of Object.entries(indLineSeries.current)) {
                let val: number | null = null;
                if (param.time && param.seriesData) {
                    const d = param.seriesData.get(series) as LineData | undefined;
                    if (d?.value !== undefined) val = d.value;
                }
                if (val !== null) {
                    const parts = compositeKey.split(':');
                    const activeState = useAppStore.getState();
                    const activeConfig = activeState.activeEntriesByTF[activeState.selectedTF] || [];
                    const entry = activeConfig.find((e: { name: string; tf: number }) => entryKey(e) === compositeKey);
                    const color = entry ? getEntryColor(entry) : '#6366f1';
                    vals.push({ key: compositeKey, label: `${parts[0]}(${tfLabel(parseInt(parts[1]) || 0)})`, value: val, color });
                }
            }
            setIndValues(vals);
        });

        // Y-axis wheel zoom
        const el = chartRef.current;
        const handleWheel = (e: WheelEvent) => {
            const rect = el.getBoundingClientRect();
            const mouseX = e.clientX - rect.left;
            if (mouseX >= rect.width - 65) {
                e.preventDefault();
                e.stopPropagation();
                const step = 0.02;
                const dir = e.deltaY > 0 ? 1 : -1;
                priceMargins.current.top = Math.max(0.01, Math.min(0.45, priceMargins.current.top + dir * step));
                priceMargins.current.bottom = Math.max(0.01, Math.min(0.45, priceMargins.current.bottom + dir * step));
                chart.applyOptions({ rightPriceScale: { autoScale: false, scaleMargins: { ...priceMargins.current } } });
            }
        };
        el.addEventListener('wheel', handleWheel, { passive: false });

        // Double-click reset
        const handleDblClick = (e: MouseEvent) => {
            const rect = el.getBoundingClientRect();
            if (e.clientX - rect.left >= rect.width - 65) {
                priceMargins.current = { top: 0.1, bottom: 0.1 };
                chart.applyOptions({ rightPriceScale: { autoScale: true, scaleMargins: { ...priceMargins.current } } });
            }
        };
        el.addEventListener('dblclick', handleDblClick);

        return () => {
            el.removeEventListener('wheel', handleWheel);
            el.removeEventListener('dblclick', handleDblClick);
            ro.disconnect();
            chart.remove();
            chartApi.current = null;
            candleSeries.current = null;
            indLineSeries.current = {};
        };
    }, []);

    // Re-subscribe via WS when TF, token, or active entries change
    const prevTFRef = useRef<number>(0);
    const prevTokenRef = useRef<string>('');
    const prevEntriesRef = useRef<string[]>([]);
    useEffect(() => {
        const tf = selectedTF || 60;
        const token = selectedToken || '';

        const tfOrTokenChanged = tf !== prevTFRef.current || token !== prevTokenRef.current;
        const currentKeys = activeEntries.map(e => entryKey(e));
        const prevKeys = new Set(prevEntriesRef.current);
        const entriesChanged = currentKeys.length !== prevEntriesRef.current.length ||
            currentKeys.some(k => !prevKeys.has(k));

        prevTFRef.current = tf;
        prevTokenRef.current = token;
        prevEntriesRef.current = currentKeys;

        // Only re-subscribe if something meaningful changed
        if (!tfOrTokenChanged && !entriesChanged) return;

        if (token && activeEntries.length > 0) {
            sendSubscribe(token, tf, activeEntries);
        }
    }, [selectedTF, selectedToken, activeEntries]);

    // Update chart candles — uses incremental update() when possible, full setData() only when structure changes
    useEffect(() => {
        if (!chartApi.current || !candleSeries.current) return;
        const tf = selectedTF || 60;
        const token = selectedToken;
        const raw = candles[tf];
        if (!raw || raw.length === 0) return;

        let data = raw
            .filter(c => !token || (c.exchange + ':' + c.token) === token || c.token === token)
            .map(c => {
                const tsSec = (typeof c.ts === 'string' ? Math.floor(new Date(c.ts).getTime() / 1000) : 0) + IST_OFFSET;
                return { time: tsSec as number, open: c.open / 100, high: c.high / 100, low: c.low / 100, close: c.close / 100 };
            })
            .sort((a, b) => a.time - b.time);

        // Deduplicate by timestamp (lightweight-charts requires strictly ascending times)
        if (data.length === 0) return;
        const seen = new Map<number, typeof data[0]>();
        for (const d of data) {
            seen.set(d.time, d); // last-write-wins for same timestamp
        }
        data = Array.from(seen.values()).sort((a, b) => a.time - b.time);

        // Build a fingerprint based on count + first candle time to detect structural changes
        // (new candle added, TF changed, snapshot received, etc.)
        const firstTime = data[0].time;
        const fingerprint = `${data.length}:${firstTime}:${tf}`;

        if (fingerprint !== lastCandleFingerprint.current) {
            // Structure changed (new candle count, TF switch, snapshot) → full setData()
            candleSeries.current.setData(data as CandlestickData[]);
            lastCandleFingerprint.current = fingerprint;
        } else {
            // Same structure, just a tick update on the last candle → efficient update()
            const last = data[data.length - 1];
            candleSeries.current.update(last as CandlestickData);
        }
    }, [candles, selectedTF, selectedToken]);

    // Update indicator lines — confirmed history via setData(), live peek via update()
    useEffect(() => {
        if (!chartApi.current) return;
        const chartTF = selectedTF || 60;

        // Remove stale series
        const activeKeys = new Set(activeEntries.map(e => entryKey(e)));
        for (const key of Object.keys(indLineSeries.current)) {
            if (!activeKeys.has(key)) {
                try { chartApi.current.removeSeries(indLineSeries.current[key]); } catch { /* */ }
                delete indLineSeries.current[key];
                delete lastHistoryFingerprint.current[key];
            }
        }

        // Compute candle price band for filtering warmup artifacts
        const raw = candles[chartTF];
        let bandLo = 0, bandHi = Infinity;
        if (raw && raw.length > 0) {
            bandLo = Infinity;
            bandHi = 0;
            for (const c of raw) {
                const lo = c.low / 100;
                const hi = c.high / 100;
                if (lo < bandLo) bandLo = lo;
                if (hi > bandHi) bandHi = hi;
            }
            // Add 5% margin to the band
            const margin = (bandHi - bandLo) * 0.05;
            bandLo -= margin;
            bandHi += margin;
        }

        for (const entry of activeEntries) {
            if (entry.name.startsWith('RSI')) continue;
            const compositeKey = entryKey(entry);
            const ind = indicators[compositeKey];
            if (!ind || !ind.history || ind.history.length === 0) continue;

            // Filter warmup artifacts: keep only points within the candle price band
            let lineData = ind.history.filter(h =>
                h.time && h.value !== undefined && h.value > 0 &&
                h.value >= bandLo && h.value <= bandHi
            );
            if (lineData.length === 0) continue;

            // Resample finer TF
            if (entry.tf < chartTF && lineData.length > 0) {
                const buckets = new Map<number, number>();
                for (const pt of lineData) {
                    const snapped = Math.floor(pt.time / chartTF) * chartTF;
                    buckets.set(snapped, pt.value);
                }
                lineData = Array.from(buckets.entries())
                    .map(([t, v]) => ({ time: t, value: v }))
                    .sort((a, b) => a.time - b.time);
            }

            if (lineData.length === 0) continue;

            // Deduplicate by timestamp
            const seen = new Map<number, typeof lineData[0]>();
            for (const pt of lineData) seen.set(pt.time, pt);
            lineData = Array.from(seen.values()).sort((a, b) => a.time - b.time);

            const color = getEntryColor(entry);

            // Create series if needed
            if (!indLineSeries.current[compositeKey]) {
                const parts = compositeKey.split(':');
                const displayTitle = parts[0] + ' (' + tfLabel(parseInt(parts[1]) || 0) + ')';
                indLineSeries.current[compositeKey] = chartApi.current!.addLineSeries({
                    color, lineWidth: 1 as LineWidth, crosshairMarkerVisible: false,
                    lastValueVisible: true, priceLineVisible: false, title: displayTitle,
                });
            }

            // Compute fingerprint of confirmed history to avoid unnecessary setData()
            const lastPt = lineData[lineData.length - 1];
            const fingerprint = `${lineData.length}:${lastPt.time}:${lastPt.value.toFixed(4)}`;
            const prevFingerprint = lastHistoryFingerprint.current[compositeKey];

            if (fingerprint !== prevFingerprint) {
                // Confirmed history changed → full setData()
                indLineSeries.current[compositeKey].setData(lineData as LineData[]);
                lastHistoryFingerprint.current[compositeKey] = fingerprint;
            }

            // Live peek value → efficient update() (no full redraw)
            if (ind.liveValue !== null && ind.liveTime !== null &&
                ind.liveValue >= bandLo && ind.liveValue <= bandHi) {
                const lastConfirmedTime = lineData[lineData.length - 1].time;
                const liveT = ind.liveTime >= lastConfirmedTime ? ind.liveTime : lastConfirmedTime;
                indLineSeries.current[compositeKey].update({
                    time: liveT as number,
                    value: ind.liveValue,
                } as LineData);
            }
        }
    }, [indicators, activeEntries, selectedTF, candles, selectedToken]);

    // Get latest OHLC for when no crosshair
    const latestOHLC = (() => {
        if (ohlcData) return ohlcData;
        const tf = selectedTF || 60;
        const raw = candles[tf];
        if (!raw || raw.length === 0) return null;
        const token = selectedToken;
        const filtered = raw.filter(c => !token || (c.exchange + ':' + c.token) === token || c.token === token);
        if (filtered.length === 0) return null;
        const latest = filtered[0];
        return { open: latest.open / 100, high: latest.high / 100, low: latest.low / 100, close: latest.close / 100 };
    })();

    const isUp = latestOHLC ? latestOHLC.close >= latestOHLC.open : true;
    const ohlcColor = isUp ? 'var(--green)' : 'var(--red)';
    const fmt = (v: number) => v?.toFixed(2) ?? '--';

    // Legend data
    const legendItems = Object.entries(indLineSeries.current).map(([key]) => {
        const entry = activeEntries.find(e => entryKey(e) === key);
        const parts = key.split(':');
        return {
            key,
            label: parts[0] + ' (' + tfLabel(parseInt(parts[1]) || 0) + ')',
            color: entry ? getEntryColor(entry) : '#6366f1',
        };
    });

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

                {/* Chart */}
                <div ref={chartRef} className={styles.chartContainer} />

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
            </div>
        </div>
    );
}
