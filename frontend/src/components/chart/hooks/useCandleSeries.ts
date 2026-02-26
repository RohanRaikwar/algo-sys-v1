import { useEffect, useRef, type MutableRefObject } from 'react';
import type { ISeriesApi, CandlestickData } from 'lightweight-charts';
import { useCandleStore } from '../../../store/useCandleStore';
import { IST_OFFSET } from '../../../utils/helpers';

/**
 * Manages candle data rendering on the chart.
 * Deduplicates by timestamp and uses incremental update() when possible.
 * Values are already in rupees from the store.
 */
export function useCandleSeries(
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>,
    selectedTF: number,
    selectedToken: string | null,
) {
    const candles = useCandleStore(s => s.candles);
    const lastCandleFingerprint = useRef<string>('');

    useEffect(() => {
        if (!candleSeries.current) return;
        const tf = selectedTF || 60;
        const token = selectedToken;
        const raw = candles[tf];
        if (!raw || raw.length === 0) return;

        let data = raw
            .filter(c => !token || (c.exchange + ':' + c.token) === token || c.token === token)
            .map(c => {
                const tsSec = (typeof c.ts === 'string' ? Math.floor(new Date(c.ts).getTime() / 1000) : 0) + IST_OFFSET;
                return { time: tsSec as number, open: c.open, high: c.high, low: c.low, close: c.close };
            })
            .sort((a, b) => a.time - b.time);

        // Deduplicate by timestamp (lightweight-charts requires strictly ascending times)
        if (data.length === 0) return;
        const seen = new Map<number, typeof data[0]>();
        for (const d of data) {
            seen.set(d.time, d); // last-write-wins for same timestamp
        }
        data = Array.from(seen.values()).sort((a, b) => a.time - b.time);

        // Fingerprint for structural change detection
        const firstTime = data[0].time;
        const fingerprint = `${data.length}:${firstTime}:${tf}:${token || ''}`;

        if (fingerprint !== lastCandleFingerprint.current) {
            // Structure changed → full setData()
            candleSeries.current.setData(data as CandlestickData[]);
            lastCandleFingerprint.current = fingerprint;
        } else {
            // Same structure → efficient update()
            const last = data[data.length - 1];
            candleSeries.current.update(last as CandlestickData);
        }
    }, [candles, selectedTF, selectedToken, candleSeries]);
}
