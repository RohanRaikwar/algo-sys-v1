// ── WebSocket Message Types ──

export interface WSEnvelope {
    type?: string;        // 'metrics' | 'pong' | 'config_update' | 'SNAPSHOT' | 'LIVE' | 'ERROR'
    channel?: string;     // e.g. 'pub:candle:60s:NSE:99926000'
    data?: unknown;
    ts?: string;
    // metrics fields
    metrics?: SystemMetrics;
    marketOpen?: boolean;
    marketStatus?: string;
    // pong fields
    ping?: number;
    // config_update fields
    entries?: Array<{ name: string; tf: number; color?: string }>;
    // SNAPSHOT fields
    reqId?: string;
    symbol?: string;
    tf?: number;
    candles?: SnapshotCandle[];
    indicators?: Record<string, SnapshotIndPoint[]>;
    // LIVE fields
    candle?: SnapshotCandle;
    // ERROR fields
    error?: string;
    // Sequence fields for gap detection
    seq?: number;
    channel_seq?: number;
}

export interface SystemMetrics {
    cpu_percent: number;
    cpu_cores: number;
    cpu_load_1: number;
    cpu_load_5: number;
    cpu_load_15: number;
    mem_percent: number;
    mem_used_mb: number;
    mem_total_mb: number;
    heap_alloc_mb: number;
    sys_mb: number;
    goroutines: number;
    gc_runs: number;
    uptime_sec: number;
    indicator_compute_ms?: number;
}

export interface ParsedChannel {
    type: 'indicator' | 'candle' | 'tick';
    name?: string;
    tf?: number;
    exchange?: string;
    token?: string;
}

export interface IndicatorPayload {
    name: string;
    tf: number;
    value: number;
    ts: string;
    ready: boolean;
    live?: boolean;
    exchange: string;
    token: string;
}

export interface CandlePayload {
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
    tf: number;
}

export interface TickPayload {
    tick_ts?: string;
    ts?: string;
    price: number;
    qty: number;
    token: string;
    exchange: string;
}

// ── SUBSCRIBE Protocol Types ──

export interface SubscribeMsg {
    type: 'SUBSCRIBE';
    reqId: string;
    symbol: string;
    tf: number;
    history: { candles: number };
    indicators: IndicatorSpecMsg[];
}

export interface IndicatorSpecMsg {
    id: string;      // e.g. "smma", "ema", "sma"
    source: string;  // e.g. "close"
    params: Record<string, number>;  // e.g. { length: 21 }
    tf?: number;     // per-indicator TF override (seconds), omit to use subscription TF
}

export interface SnapshotCandle {
    ts: string;
    open: number;
    high: number;
    low: number;
    close: number;
    volume: number;
    count?: number;
}

export interface SnapshotIndPoint {
    ts: string;
    value: number;
    ready: boolean;
}

export interface UnsubscribeMsg {
    type: 'UNSUBSCRIBE';
    reqId: string;
    symbol: string;
    tf: number;
}
