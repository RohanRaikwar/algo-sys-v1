// ── API Response Types ──

export interface AppConfig {
    tfs: number[];
    tokens: string[];
    indicators: string[];
}

export interface IndicatorEntry {
    name: string;   // e.g. "SMA_9", "EMA_4"
    tf: number;     // timeframe in seconds
    color?: string; // hex color
}

export interface ActiveConfig {
    entries: IndicatorEntry[];
}

export interface CandleOut {
    ts: string;
    open: number;
    high: number;
    low: number;
    close: number;
    volume: number;
    count: number;
    token: string;
    exchange: string;
    tf: number;
    forming: boolean;
}

export interface IndPoint {
    value: number;
    ts: string;
    ready: boolean;
}
