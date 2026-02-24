// ── Chart Data Structures ──

export interface ChartCandle {
    time: number;  // unix seconds (IST-adjusted)
    open: number;
    high: number;
    low: number;
    close: number;
}

export interface ChartPoint {
    time: number;
    value: number;
}

export interface IndicatorState {
    name: string;
    tf: number;
    value: number | null;
    prevValue: number | null;
    ts: string | null;
    ready: boolean;
    history: ChartPoint[];         // confirmed indicator points only
    liveValue: number | null;      // current live/peek value (forming candle)
    liveTime: number | null;       // timestamp for live point (unix seconds IST)
    exchange: string;
    token: string;
}
