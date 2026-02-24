import { create } from 'zustand';
import type { IndicatorState } from '../types/chart';
import { IST_OFFSET, CHART_MAX, CANDLE_MAX } from '../utils/helpers';

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
}

export const useCandleStore = create<CandleStore>((set) => ({
    candles: {},
    indicators: {},

    upsertCandle: (tf, candle) => set((s) => {
        const arr = [...(s.candles[tf] || [])];
        const idx = arr.findIndex(c => c.ts === candle.ts && c.token === candle.token);
        if (idx >= 0) {
            arr[idx] = candle;
        } else {
            arr.unshift(candle);
        }
        if (arr.length > CANDLE_MAX) arr.pop();
        return { candles: { ...s.candles, [tf]: arr } };
    }),

    aggregateToTF: (activeTFs, data) => set((s) => {
        const candleTS = new Date(data.ts).getTime();
        const newCandles = { ...s.candles };

        for (const activeTF of activeTFs) {
            const tfMs = activeTF * 1000;
            const bucketMs = Math.floor(candleTS / tfMs) * tfMs;
            const bucketISO = new Date(bucketMs).toISOString();

            const tfArr = [...(newCandles[activeTF] || [])];
            const existIdx = tfArr.findIndex(c => c.ts === bucketISO && c.token === data.token);

            if (existIdx >= 0) {
                const existing = { ...tfArr[existIdx] };
                existing.high = Math.max(existing.high, data.high);
                existing.low = Math.min(existing.low, data.low);
                existing.close = data.close;
                existing.volume = (existing.volume || 0) + (data.volume || 0);
                existing.count = (existing.count || 0) + (data.count || 0);
                existing.forming = true;
                tfArr[existIdx] = existing;
            } else {
                tfArr.unshift({
                    ts: bucketISO,
                    open: data.open,
                    high: data.high,
                    low: data.low,
                    close: data.close,
                    volume: data.volume || 0,
                    count: data.count || 0,
                    forming: true,
                    exchange: data.exchange,
                    token: data.token,
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
            return {
                indicators: {
                    ...s.indicators,
                    [key]: {
                        ...existing,
                        prevValue: existing.value,
                        value,
                        ts,
                        ready,
                        exchange,
                        token,
                        liveValue: value,
                        liveTime: tsSec,
                    },
                },
            };
        }

        // Confirmed candle: add/update in history, clear live state
        const history = [...existing.history];
        const existIdx = history.findIndex(h => h.time === tsSec);
        if (existIdx >= 0) {
            history[existIdx] = { time: tsSec, value };
        } else if (ready) {
            history.push({ time: tsSec, value });
            history.sort((a, b) => a.time - b.time);
        }
        if (history.length > CHART_MAX) history.splice(0, history.length - CHART_MAX);

        return {
            indicators: {
                ...s.indicators,
                [key]: {
                    ...existing,
                    prevValue: existing.value,
                    value,
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
}));
