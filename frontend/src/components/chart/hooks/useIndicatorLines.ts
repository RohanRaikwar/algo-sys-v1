import { useEffect, useRef, type MutableRefObject } from 'react';
import type { IChartApi, ISeriesApi, LineData, LineWidth } from 'lightweight-charts';
import { useCandleStore } from '../../../store/useCandleStore';
import { IST_OFFSET, entryKey, getEntryColor, tfLabel } from '../../../utils/helpers';
import type { IndicatorEntry } from '../../../types/api';

/**
 * Manages indicator line series on the chart.
 * Creates/removes series, applies band filter, resamples across TFs,
 * and merges live peek values. Values are already in rupees from the store.
 */
export function useIndicatorLines(
    chartApi: MutableRefObject<IChartApi | null>,
    indLineSeries: MutableRefObject<Record<string, ISeriesApi<'Line'>>>,
    activeEntries: IndicatorEntry[],
    selectedTF: number,
    selectedToken: string | null,
) {
    const candles = useCandleStore(s => s.candles);
    const indicators = useCandleStore(s => s.indicators);
    const lastHistoryFingerprint = useRef<Record<string, string>>({});

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
                if (c.low < bandLo) bandLo = c.low;
                if (c.high > bandHi) bandHi = c.high;
            }
            const margin = (bandHi - bandLo) * 0.05;
            bandLo -= margin;
            bandHi += margin;
        }

        for (const entry of activeEntries) {
            if (entry.name.startsWith('RSI')) continue;
            const compositeKey = entryKey(entry);
            const ind = indicators[compositeKey];
            if (!ind || !ind.history || ind.history.length === 0) continue;

            // Filter warmup artifacts (both candle band and indicator values are in paise)
            let lineData = ind.history.filter(h =>
                h.time && h.value !== undefined && h.value > 0 &&
                h.value >= bandLo && h.value <= bandHi
            ).map(h => ({ time: h.time, value: h.value }));
            if (lineData.length === 0) continue;

            // Resample finer TF → snap to chart TF buckets
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

            // Step-fill coarser TF → hold value across chart candles
            if (entry.tf > chartTF && lineData.length > 0) {
                const chartCandles = candles[chartTF];
                if (chartCandles && chartCandles.length > 0) {
                    const sorted = [...chartCandles].sort((a, b) => {
                        const ta = new Date(a.ts).getTime();
                        const tb = new Date(b.ts).getTime();
                        return ta - tb;
                    });
                    const filled: typeof lineData = [];
                    let indIdx = 0;
                    for (const c of sorted) {
                        const cTime = (typeof c.ts === 'string' ? Math.floor(new Date(c.ts).getTime() / 1000) : 0) + IST_OFFSET;
                        while (indIdx < lineData.length - 1 && lineData[indIdx + 1].time <= cTime) {
                            indIdx++;
                        }
                        if (lineData[indIdx].time <= cTime) {
                            filled.push({ time: cTime, value: lineData[indIdx].value });
                        }
                    }
                    lineData = filled;
                }
            }

            if (lineData.length === 0) continue;

            // Deduplicate by timestamp
            const seen = new Map<number, typeof lineData[0]>();
            for (const pt of lineData) seen.set(pt.time, pt);
            lineData = Array.from(seen.values()).sort((a, b) => a.time - b.time);

            // Merge live peek value
            if (ind.liveValue !== null && ind.liveTime !== null &&
                ind.liveValue >= bandLo && ind.liveValue <= bandHi) {
                const lastConfirmedTime = lineData[lineData.length - 1].time;
                // Snap live time to current chart TF bucket so lower-TF live points
                // do not extend the line to the right on higher-TF charts.
                const snappedLiveT = Math.floor(ind.liveTime / chartTF) * chartTF;
                const liveT = snappedLiveT >= lastConfirmedTime ? snappedLiveT : lastConfirmedTime;
                const liveRupees = ind.liveValue;
                const liveIdx = lineData.findIndex(p => p.time === liveT);
                if (liveIdx >= 0) {
                    lineData[liveIdx] = { time: liveT, value: liveRupees };
                } else {
                    lineData.push({ time: liveT, value: liveRupees });
                    lineData.sort((a, b) => a.time - b.time);
                }
            }

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

            // Fingerprint comparison
            const lastPt = lineData[lineData.length - 1];
            const liveFingerprint = ind.liveValue !== null ? `:${ind.liveValue.toFixed(2)}` : '';
            const fingerprint = `${lineData.length}:${lastPt.time}:${lastPt.value.toFixed(4)}${liveFingerprint}`;
            const prevFingerprint = lastHistoryFingerprint.current[compositeKey];

            if (fingerprint !== prevFingerprint) {
                indLineSeries.current[compositeKey].setData(lineData as LineData[]);
                lastHistoryFingerprint.current[compositeKey] = fingerprint;
            }
        }
    }, [indicators, activeEntries, selectedTF, candles, selectedToken, chartApi, indLineSeries]);
}
