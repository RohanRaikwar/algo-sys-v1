import { useEffect, useRef, useCallback, useState } from 'react';
import {
    createChart, CrosshairMode, PriceScaleMode, ColorType,
    type IChartApi, type ISeriesApi, type CandlestickData, type LineData, type MouseEventParams, type LineWidth,
} from 'lightweight-charts';
import { useAppStore } from '../../store/useAppStore';
import { useCandleStore } from '../../store/useCandleStore';
import { tfLabel, IST_OFFSET, FETCH_SIZE, CHART_MAX, entryKey, getEntryColor } from '../../utils/helpers';
import { fetchCandles, fetchIndicatorHistory } from '../../services/api';
import styles from './Chart.module.css';

export function TradingChart() {
    const chartRef = useRef<HTMLDivElement>(null);
    const chartApi = useRef<IChartApi | null>(null);
    const candleSeries = useRef<ISeriesApi<'Candlestick'> | null>(null);
    const indLineSeries = useRef<Record<string, ISeriesApi<'Line'>>>({});
    const priceMargins = useRef({ top: 0.1, bottom: 0.1 });
    const loadingRef = useRef(false);
    const hasMoreRef = useRef(true);
    const oldestTSRef = useRef<string | null>(null);

    const { config, selectedToken, selectedTF, activeEntries, setSelectedTF } = useAppStore();
    const candles = useCandleStore((s) => s.candles);
    const indicators = useCandleStore((s) => s.indicators);
    const mergeCandles = useCandleStore((s) => s.mergeCandles);
    const setIndicatorHistory = useCandleStore((s) => s.setIndicatorHistory);
    const mergeIndicatorHistory = useCandleStore((s) => s.mergeIndicatorHistory);

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
                    const activeConfig = useAppStore.getState().activeEntries;
                    const entry = activeConfig.find((e) => entryKey(e) === compositeKey);
                    const color = entry ? getEntryColor(entry) : '#6366f1';
                    vals.push({ key: compositeKey, label: `${parts[0]}(${tfLabel(parseInt(parts[1]) || 0)})`, value: val, color });
                }
            }
            setIndValues(vals);
        });

        // Lazy scroll loading
        let scrollDebounce: ReturnType<typeof setTimeout> | null = null;
        chart.timeScale().subscribeVisibleLogicalRangeChange((range) => {
            if (!range || loadingRef.current || !hasMoreRef.current) return;
            if (range.from <= 5) {
                if (scrollDebounce) clearTimeout(scrollDebounce);
                scrollDebounce = setTimeout(() => fetchOlderData(), 300);
            }
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

    // Fetch older data on scroll
    const fetchOlderData = useCallback(async () => {
        if (loadingRef.current || !hasMoreRef.current) return;
        loadingRef.current = true;
        try {
            const before = oldestTSRef.current;
            if (!before) { loadingRef.current = false; return; }
            const tf = selectedTF || 60;
            const token = selectedToken || '';

            const candleData = await fetchCandles(tf, token, FETCH_SIZE, before);
            if (candleData.length === 0) { hasMoreRef.current = false; loadingRef.current = false; return; }

            // Price-proximity filter
            const refPrice = candleData[candleData.length - 1].close;
            const threshold = refPrice * 0.20;
            const filtered = candleData.filter(c => c.ts && Math.abs(c.close - refPrice) <= threshold);

            mergeCandles(tf, filtered.map(c => ({
                ts: c.ts, open: c.open, high: c.high, low: c.low, close: c.close,
                volume: c.volume, count: c.count, forming: c.forming,
                exchange: c.exchange, token: c.token,
            })));

            if (filtered.length > 0 && (!oldestTSRef.current || filtered[0].ts < oldestTSRef.current)) {
                oldestTSRef.current = filtered[0].ts;
            }

            // Also fetch older indicators
            const entries = activeEntries.filter(e => e.tf === tf && !e.name.startsWith('RSI'));
            await Promise.all(entries.map(async (entry) => {
                try {
                    const points = await fetchIndicatorHistory(entry.name, tf, token, FETCH_SIZE, before);
                    if (points.length === 0) return;
                    const fullKey = entryKey(entry);
                    const newPts = points.filter(p => p.ts && p.value !== undefined).map(p => ({
                        time: Math.floor(new Date(p.ts).getTime() / 1000) + IST_OFFSET, value: p.value,
                    }));
                    mergeIndicatorHistory(fullKey, newPts);
                } catch { /* ignore */ }
            }));
        } catch (e) {
            console.warn('[fetchOlderData] error:', e);
        } finally {
            loadingRef.current = false;
        }
    }, [selectedTF, selectedToken, activeEntries, mergeCandles, mergeIndicatorHistory]);

    // Load initial historical data
    useEffect(() => {
        hasMoreRef.current = true;
        oldestTSRef.current = null;

        const tf = selectedTF || 60;
        const token = selectedToken || '';

        (async () => {
            try {
                const candleData = await fetchCandles(tf, token, FETCH_SIZE);
                if (candleData.length === 0) return;

                const newest = candleData[candleData.length - 1];
                const refPrice = newest.close;
                const threshold = refPrice * 0.20;
                const filtered = candleData.filter(c => c.ts && Math.abs(c.close - refPrice) <= threshold);
                if (filtered.length === 0) return;

                mergeCandles(tf, filtered.map(c => ({
                    ts: c.ts, open: c.open, high: c.high, low: c.low, close: c.close,
                    volume: c.volume, count: c.count, forming: c.forming,
                    exchange: c.exchange, token: c.token,
                })));
                oldestTSRef.current = filtered[0].ts;

                // Load indicator history for all active entries
                const allEntries = activeEntries.filter(e => !e.name.startsWith('RSI'));
                await Promise.all(allEntries.map(async (entry) => {
                    try {
                        const points = await fetchIndicatorHistory(entry.name, entry.tf, token, FETCH_SIZE);
                        if (points.length === 0) return;
                        const fullKey = entryKey(entry);
                        const allPts = points.filter(p => p.ts && p.value !== undefined).map(p => ({
                            time: Math.floor(new Date(p.ts).getTime() / 1000) + IST_OFFSET, value: p.value,
                        }));
                        const refVal = allPts.length > 0 ? allPts[allPts.length - 1].value : 0;
                        const tol = refVal * 0.30;
                        const newHistory = allPts.filter(h => Math.abs(h.value - refVal) <= tol);
                        setIndicatorHistory(fullKey, entry.name, entry.tf, newHistory);
                    } catch { /* ignore */ }
                }));
            } catch (e) { console.warn('[loadHistorical] error:', e); }
        })();
    }, [selectedTF, selectedToken, activeEntries]);

    // Update chart candles
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

        if (data.length > 1) {
            const refClose = data[data.length - 1].close;
            const tol = refClose * 0.20;
            data = data.filter(d => Math.abs(d.close - refClose) <= tol);
        }

        // Deduplicate by timestamp (lightweight-charts requires strictly ascending times)
        if (data.length > 0) {
            const seen = new Map<number, typeof data[0]>();
            for (const d of data) {
                seen.set(d.time, d); // last-write-wins for same timestamp
            }
            data = Array.from(seen.values()).sort((a, b) => a.time - b.time);
            candleSeries.current.setData(data as CandlestickData[]);
        }
    }, [candles, selectedTF, selectedToken]);

    // Update indicator lines
    useEffect(() => {
        if (!chartApi.current) return;
        const chartTF = selectedTF || 60;

        // Remove stale
        const activeKeys = new Set(activeEntries.map(e => entryKey(e)));
        for (const key of Object.keys(indLineSeries.current)) {
            if (!activeKeys.has(key)) {
                try { chartApi.current.removeSeries(indLineSeries.current[key]); } catch { /* */ }
                delete indLineSeries.current[key];
            }
        }

        // Get reference price
        const raw = candles[chartTF];
        let candleRefPrice = 0;
        if (raw && raw.length > 0) candleRefPrice = raw[0].close / 100;

        for (const entry of activeEntries) {
            if (entry.name.startsWith('RSI')) continue;
            const compositeKey = entryKey(entry);
            const ind = indicators[compositeKey];
            if (!ind || !ind.history || ind.history.length === 0) continue;

            let allPts = ind.history.filter(h => h.time && h.value !== undefined);
            if (allPts.length === 0) continue;

            const refVal = candleRefPrice > 0 ? candleRefPrice : allPts[allPts.length - 1].value;
            const tol = refVal * 0.10;
            let lineData = allPts.filter(h => Math.abs(h.value - refVal) <= tol);

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

            if (lineData.length > 0) {
                // Deduplicate by timestamp
                const seen = new Map<number, typeof lineData[0]>();
                for (const pt of lineData) seen.set(pt.time, pt);
                lineData = Array.from(seen.values()).sort((a, b) => a.time - b.time);

                const color = getEntryColor(entry);
                if (!indLineSeries.current[compositeKey]) {
                    const parts = compositeKey.split(':');
                    const displayTitle = parts[0] + ' (' + tfLabel(parseInt(parts[1]) || 0) + ')';
                    indLineSeries.current[compositeKey] = chartApi.current!.addLineSeries({
                        color, lineWidth: 2 as LineWidth, crosshairMarkerVisible: false,
                        lastValueVisible: true, priceLineVisible: false, title: displayTitle,
                    });
                }
                indLineSeries.current[compositeKey].setData(lineData as LineData[]);
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
