// ── WebSocket Message Types ──

export interface WSEnvelope {
    type?: string;        // 'metrics' | 'pong' | 'config_update' | undefined
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
