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
    history: ChartPoint[];
    exchange: string;
    token: string;
}
