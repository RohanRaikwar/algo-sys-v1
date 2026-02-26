import { create } from 'zustand';
import type { IndicatorState } from '../types/chart';
import { IST_OFFSET, CHART_MAX, CANDLE_MAX } from '../utils/helpers';

// Convert paise (int64 from backend) to rupees (float for display).
// All candle OHLC and indicator values enter the store in paise;
// this single conversion point ensures downstream hooks always get rupees.
const toRupees = (paise: number) => paise / 100;

interface CandleRaw {
    ts: string;
    open: number;
    high: number;
    low: number;
    close: number;
    volume: number;
    count: number;
    forming: boolean;
    exchange: string;
    token: string;
}

interface CandleStore {
    candles: Record<number, CandleRaw[]>; // keyed by TF
    indicators: Record<string, IndicatorState>; // keyed by "NAME:tf"

    // Candle actions
    upsertCandle: (tf: number, candle: CandleRaw) => void;
    aggregateToTF: (activeTFs: number[], data: CandleRaw) => void;
    setCandles: (tf: number, candles: CandleRaw[]) => void;
    mergeCandles: (tf: number, candles: CandleRaw[]) => void;

    // Indicator actions
    updateIndicator: (key: string, name: string, tf: number, value: number, ts: string, ready: boolean, live: boolean, exchange: string, token: string) => void;
    setIndicatorHistory: (key: string, name: string, tf: number, history: Array<{ time: number; value: number }>, exchange?: string, token?: string) => void;
    mergeIndicatorHistory: (key: string, points: Array<{ time: number; value: number }>) => void;

    // Snapshot: bulk set candles + indicators from WS SNAPSHOT
    setSnapshot: (tf: number, candles: CandleRaw[], indicators: Record<string, Array<{ time: number; value: number }>>, token: string, exchange: string) => void;

    // Cleanup: remove indicator entries for a TF that are not in keepNames
    clearIndicatorsForTF: (tf: number, keepNames: string[]) => void;
}

export const useCandleStore = create<CandleStore>((set) => ({
    candles: {},
    indicators: {},

    upsertCandle: (tf, candle) => set((s) => {
        // Convert OHLC from paise→rupees on ingestion
        const converted = {
            ...candle,
            open: toRupees(candle.open),
            high: toRupees(candle.high),
            low: toRupees(candle.low),
            close: toRupees(candle.close),
        };
        const arr = [...(s.candles[tf] || [])];
        const idx = arr.findIndex(c => c.ts === converted.ts && c.token === converted.token);
        if (idx >= 0) {
            arr[idx] = converted;
        } else {
            arr.unshift(converted);
        }
        if (arr.length > CANDLE_MAX) arr.pop();
        return { candles: { ...s.candles, [tf]: arr } };
    }),

    aggregateToTF: (activeTFs, data) => set((s) => {
        // Convert incoming 1s candle OHLC from paise→rupees
        const dataR = {
            ...data,
            open: toRupees(data.open),
            high: toRupees(data.high),
            low: toRupees(data.low),
            close: toRupees(data.close),
        };
        const candleTS = new Date(dataR.ts).getTime();
        const newCandles = { ...s.candles };

        for (const activeTF of activeTFs) {
            const tfMs = activeTF * 1000;
            const bucketMs = Math.floor(candleTS / tfMs) * tfMs;
            const bucketISO = new Date(bucketMs).toISOString();

            const tfArr = [...(newCandles[activeTF] || [])];
            const existIdx = tfArr.findIndex(c => c.ts === bucketISO && c.token === dataR.token);

            if (existIdx >= 0) {
                const existing = { ...tfArr[existIdx] };
                existing.high = Math.max(existing.high, dataR.high);
                existing.low = Math.min(existing.low, dataR.low);
                existing.close = dataR.close;
                existing.volume = (existing.volume || 0) + (dataR.volume || 0);
                existing.count = (existing.count || 0) + (dataR.count || 0);
                existing.forming = true;
                tfArr[existIdx] = existing;
            } else {
                tfArr.unshift({
                    ts: bucketISO,
                    open: dataR.open,
                    high: dataR.high,
                    low: dataR.low,
                    close: dataR.close,
                    volume: dataR.volume || 0,
                    count: dataR.count || 0,
                    forming: true,
                    exchange: dataR.exchange,
                    token: dataR.token,
                });
                if (tfArr.length > CANDLE_MAX) tfArr.pop();
            }
            newCandles[activeTF] = tfArr;
        }

        return { candles: newCandles };
    }),

    setCandles: (tf, candles) => set((s) => ({
        candles: { ...s.candles, [tf]: candles },
    })),

    mergeCandles: (tf, newCandles) => set((s) => {
        const arr = [...(s.candles[tf] || [])];
        for (const c of newCandles) {
            const idx = arr.findIndex(x => x.ts === c.ts && x.token === c.token);
            if (idx >= 0) {
                arr[idx] = c;
            } else {
                arr.push(c);
            }
        }
        if (arr.length > CHART_MAX) arr.splice(0, arr.length - CHART_MAX);
        return { candles: { ...s.candles, [tf]: arr } };
    }),

    updateIndicator: (key, name, tf, value, ts, ready, live, exchange, token) => set((s) => {
        const existing = s.indicators[key] || {
            name, tf, value: null, prevValue: null, ts: null, ready: false,
            history: [], liveValue: null, liveTime: null, exchange: '', token: '',
        };

        const tsSec = Math.floor(new Date(ts).getTime() / 1000) + IST_OFFSET;

        if (live) {
            // Live/peek update: only update liveValue — don't touch history
            // Convert indicator value from paise→rupees
            const rupeeValue = toRupees(value);
            return {
                indicators: {
                    ...s.indicators,
                    [key]: {
                        ...existing,
                        prevValue: existing.value,
                        value: rupeeValue,
                        ts,
                        ready,
                        exchange,
                        token,
                        liveValue: rupeeValue,
                        liveTime: tsSec,
                    },
                },
            };
        }

        // Confirmed candle: add/update in history, clear live state
        // Convert indicator value from paise→rupees
        const rupeeValue = toRupees(value);
        const history = [...existing.history];
        const existIdx = history.findIndex(h => h.time === tsSec);
        if (existIdx >= 0) {
            history[existIdx] = { time: tsSec, value: rupeeValue };
        } else if (ready) {
            history.push({ time: tsSec, value: rupeeValue });
            history.sort((a, b) => a.time - b.time);
        }
        if (history.length > CHART_MAX) history.splice(0, history.length - CHART_MAX);

        return {
            indicators: {
                ...s.indicators,
                [key]: {
                    ...existing,
                    prevValue: existing.value,
                    value: rupeeValue,
                    ts,
                    ready,
                    exchange,
                    token,
                    history,
                    liveValue: null,
                    liveTime: null,
                },
            },
        };
    }),

    setIndicatorHistory: (key, name, tf, history, exchange = '', token = '') => set((s) => ({
        indicators: {
            ...s.indicators,
            [key]: {
                name, tf,
                value: history.length > 0 ? history[history.length - 1].value : null,
                prevValue: null,
                ts: null,
                ready: history.length > 0,
                history,
                liveValue: null,
                liveTime: null,
                exchange,
                token,
            },
        },
    })),

    mergeIndicatorHistory: (key, points) => set((s) => {
        const existing = s.indicators[key];
        if (!existing) return s;

        const history = [...existing.history];
        const timeSet = new Set(history.map(h => h.time));
        for (const pt of points) {
            if (!timeSet.has(pt.time)) {
                history.push(pt);
                timeSet.add(pt.time);
            }
        }
        history.sort((a, b) => a.time - b.time);
        if (history.length > CHART_MAX) history.splice(0, history.length - CHART_MAX);

        return {
            indicators: {
                ...s.indicators,
                [key]: {
                    ...existing,
                    history,
                    value: history.length > 0 ? history[history.length - 1].value : existing.value,
                    ready: true,
                },
            },
        };
    }),

    // Bulk set candles + indicators from WS SNAPSHOT
    // Indicator keys come as "NAME:TF" (e.g., "SMA_20:300") from the backend
    // Replaces all indicators to prevent stale cross-symbol data bleed
    setSnapshot: (tf, candles, indicators, token, exchange) => set((s) => {
        // Convert candle OHLC from paise→rupees
        const convertedCandles = candles.map(c => ({
            ...c,
            open: toRupees(c.open),
            high: toRupees(c.high),
            low: toRupees(c.low),
            close: toRupees(c.close),
        }));

        // Build fresh indicators map (don't merge with old state)
        const newIndicators: Record<string, IndicatorState> = {};
        for (const [compositeKey, history] of Object.entries(indicators)) {
            // compositeKey is already "NAME:TF" — parse name and tf from it
            const colonIdx = compositeKey.lastIndexOf(':');
            const name = colonIdx > 0 ? compositeKey.substring(0, colonIdx) : compositeKey;
            const indTF = colonIdx > 0 ? parseInt(compositeKey.substring(colonIdx + 1)) || tf : tf;
            // Convert indicator values from paise→rupees
            const convertedHistory = history.map(h => ({ time: h.time, value: toRupees(h.value) }));
            newIndicators[compositeKey] = {
                name,
                tf: indTF,
                value: convertedHistory.length > 0 ? convertedHistory[convertedHistory.length - 1].value : null,
                prevValue: null,
                ts: null,
                ready: convertedHistory.length > 0,
                history: convertedHistory,
                liveValue: null,
                liveTime: null,
                exchange,
                token,
            };
        }
        return {
            candles: { ...s.candles, [tf]: convertedCandles },
            indicators: newIndicators,
        };
    }),

    // Remove indicator entries for a given TF not in keepNames
    clearIndicatorsForTF: (tf, keepNames) => set((s) => {
        const keep = new Set(keepNames.map(n => `${n}:${tf}`));
        const newIndicators: Record<string, IndicatorState> = {};
        for (const [key, val] of Object.entries(s.indicators)) {
            // Keep if not for this TF, or if in the keep set
            if (val.tf !== tf || keep.has(key)) {
                newIndicators[key] = val;
            }
        }
        return { indicators: newIndicators };
    }),
}));

