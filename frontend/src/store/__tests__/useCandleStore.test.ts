import { describe, it, expect, beforeEach } from 'vitest';
import { useCandleStore } from '../useCandleStore';

// Reset zustand store state before each test
beforeEach(() => {
    useCandleStore.setState({ candles: {}, indicators: {} });
});

const makeCandle = (ts: string, ohlc: number, token = '99926000', exchange = 'NSE') => ({
    ts, open: ohlc, high: ohlc + 200, low: ohlc - 100, close: ohlc + 100,
    volume: 1000, count: 50, forming: false, exchange, token,
});

describe('useCandleStore', () => {
    // ── Snapshot Application ──

    describe('setSnapshot', () => {
        it('stores candles and converts paise→rupees', () => {
            const candles = [
                makeCandle('2026-02-25T10:00:00Z', 10000),
                makeCandle('2026-02-25T10:01:00Z', 10200),
            ];
            useCandleStore.getState().setSnapshot(60, candles, {}, '99926000', 'NSE');

            const stored = useCandleStore.getState().candles[60];
            expect(stored).toHaveLength(2);
            // 10000 paise → 100 rupees
            expect(stored[0].open).toBe(100);
            expect(stored[0].high).toBe(102);  // (10000 + 200) / 100
            expect(stored[0].low).toBe(99);    // (10000 - 100) / 100
            expect(stored[0].close).toBe(101); // (10000 + 100) / 100
        });

        it('stores indicator history from snapshot', () => {
            const indicators = {
                'SMA_20:300': [
                    { time: 1000, value: 10050 },
                    { time: 1300, value: 10100 },
                ],
            };
            useCandleStore.getState().setSnapshot(300, [], indicators, '99926000', 'NSE');

            const ind = useCandleStore.getState().indicators['SMA_20:300'];
            expect(ind).toBeDefined();
            expect(ind.name).toBe('SMA_20');
            expect(ind.tf).toBe(300);
            expect(ind.history).toHaveLength(2);
            // Indicator values also get paise→rupees conversion
            expect(ind.history[0].value).toBe(100.5);  // 10050/100
            expect(ind.history[1].value).toBe(101);     // 10100/100
            expect(ind.value).toBe(101); // last value
            expect(ind.ready).toBe(true);
        });

        it('parses composite indicator key NAME:TF correctly', () => {
            const indicators = {
                'RSI_14:120': [{ time: 500, value: 6500 }],
                'EMA_21:60': [{ time: 600, value: 5000 }],
            };
            useCandleStore.getState().setSnapshot(60, [], indicators, '99926000', 'NSE');

            const rsi = useCandleStore.getState().indicators['RSI_14:120'];
            expect(rsi.name).toBe('RSI_14');
            expect(rsi.tf).toBe(120);

            const ema = useCandleStore.getState().indicators['EMA_21:60'];
            expect(ema.name).toBe('EMA_21');
            expect(ema.tf).toBe(60);
        });
    });

    // ── Indicator Merge / Deduplication ──

    describe('mergeIndicatorHistory', () => {
        it('deduplicates by time', () => {
            // Seed with initial history
            useCandleStore.getState().setIndicatorHistory(
                'SMA_9:60', 'SMA_9', 60,
                [{ time: 100, value: 10 }, { time: 200, value: 20 }]
            );

            // Merge with overlapping + new points
            useCandleStore.getState().mergeIndicatorHistory(
                'SMA_9:60',
                [{ time: 200, value: 20 }, { time: 300, value: 30 }]
            );

            const ind = useCandleStore.getState().indicators['SMA_9:60'];
            expect(ind.history).toHaveLength(3); // 100, 200, 300 — no duplicate at 200
            expect(ind.history.map(h => h.time)).toEqual([100, 200, 300]);
        });

        it('keeps history sorted by time', () => {
            useCandleStore.getState().setIndicatorHistory(
                'EMA_21:60', 'EMA_21', 60,
                [{ time: 300, value: 30 }]
            );

            useCandleStore.getState().mergeIndicatorHistory(
                'EMA_21:60',
                [{ time: 100, value: 10 }, { time: 500, value: 50 }]
            );

            const ind = useCandleStore.getState().indicators['EMA_21:60'];
            expect(ind.history.map(h => h.time)).toEqual([100, 300, 500]);
        });

        it('updates value to latest after merge', () => {
            useCandleStore.getState().setIndicatorHistory(
                'SMA_9:60', 'SMA_9', 60,
                [{ time: 100, value: 10 }]
            );
            useCandleStore.getState().mergeIndicatorHistory(
                'SMA_9:60',
                [{ time: 200, value: 25 }]
            );

            const ind = useCandleStore.getState().indicators['SMA_9:60'];
            expect(ind.value).toBe(25);
        });

        it('ignores merge on nonexistent key', () => {
            const before = useCandleStore.getState();
            useCandleStore.getState().mergeIndicatorHistory(
                'NONEXISTENT:60',
                [{ time: 100, value: 10 }]
            );
            expect(useCandleStore.getState().indicators).toEqual(before.indicators);
        });
    });

    // ── Candle Upsert ──

    describe('upsertCandle', () => {
        it('inserts new candle and converts paise→rupees', () => {
            const candle = makeCandle('2026-02-25T10:00:00Z', 5000);
            useCandleStore.getState().upsertCandle(60, candle);

            const stored = useCandleStore.getState().candles[60];
            expect(stored).toHaveLength(1);
            expect(stored[0].open).toBe(50);    // 5000/100
            expect(stored[0].close).toBe(51);   // 5100/100
        });

        it('updates existing candle by ts+token', () => {
            const c1 = makeCandle('2026-02-25T10:00:00Z', 5000);
            const c2 = makeCandle('2026-02-25T10:00:00Z', 5500);
            useCandleStore.getState().upsertCandle(60, c1);
            useCandleStore.getState().upsertCandle(60, c2);

            const stored = useCandleStore.getState().candles[60];
            expect(stored).toHaveLength(1); // same ts+token = update
            expect(stored[0].open).toBe(55); // updated to 5500/100
        });
    });

    // ── Indicator Update (live vs confirmed) ──

    describe('updateIndicator', () => {
        it('live peek only updates liveValue', () => {
            useCandleStore.getState().updateIndicator(
                'SMA_9:60', 'SMA_9', 60, 10000,
                '2026-02-25T10:00:00Z', true, true, 'NSE', '99926000'
            );

            const ind = useCandleStore.getState().indicators['SMA_9:60'];
            expect(ind.liveValue).toBe(100); // 10000/100
            expect(ind.history).toHaveLength(0); // live doesn't touch history
        });

        it('confirmed update adds to history and clears live state', () => {
            // First a live peek
            useCandleStore.getState().updateIndicator(
                'SMA_9:60', 'SMA_9', 60, 10000,
                '2026-02-25T10:00:00Z', true, true, 'NSE', '99926000'
            );
            // Then a confirmed update
            useCandleStore.getState().updateIndicator(
                'SMA_9:60', 'SMA_9', 60, 10200,
                '2026-02-25T10:00:00Z', true, false, 'NSE', '99926000'
            );

            const ind = useCandleStore.getState().indicators['SMA_9:60'];
            expect(ind.liveValue).toBeNull(); // cleared
            expect(ind.liveTime).toBeNull();  // cleared
            expect(ind.history).toHaveLength(1); // added to history
            expect(ind.history[0].value).toBe(102); // 10200/100
        });
    });

    // ── Clear Indicators ──

    describe('clearIndicatorsForTF', () => {
        it('removes indicators not in keep list', () => {
            useCandleStore.getState().setIndicatorHistory(
                'SMA_9:60', 'SMA_9', 60, [{ time: 100, value: 10 }]
            );
            useCandleStore.getState().setIndicatorHistory(
                'EMA_21:60', 'EMA_21', 60, [{ time: 100, value: 20 }]
            );
            useCandleStore.getState().setIndicatorHistory(
                'RSI_14:300', 'RSI_14', 300, [{ time: 100, value: 70 }]
            );

            useCandleStore.getState().clearIndicatorsForTF(60, ['SMA_9']);

            const inds = useCandleStore.getState().indicators;
            expect(inds['SMA_9:60']).toBeDefined();   // kept
            expect(inds['EMA_21:60']).toBeUndefined(); // removed (TF=60, not in keep)
            expect(inds['RSI_14:300']).toBeDefined();  // kept (different TF)
        });
    });
});
