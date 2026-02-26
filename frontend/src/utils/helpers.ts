// ── Color helpers & constants ──

export const SMA_PALETTE = ['#6366f1', '#8b5cf6', '#a78bfa', '#c4b5fd', '#7c3aed', '#4f46e5', '#4338ca'];
export const EMA_PALETTE = ['#06b6d4', '#22d3ee', '#67e8f9', '#0891b2', '#0e7490', '#155e75', '#164e63'];
export const SMMA_PALETTE = ['#10b981', '#34d399', '#6ee7b7', '#059669', '#047857', '#065f46', '#064e3b'];

export const IND_COLORS: Record<string, string> = {
    'SMA_9': '#6366f1',
    'SMA_20': '#8b5cf6',
    'SMA_50': '#a78bfa',
    'SMA_100': '#c4b5fd',
    'SMA_200': '#7c3aed',
    'EMA_9': '#06b6d4',
    'EMA_21': '#22d3ee',
    'EMA_50': '#67e8f9',
    'EMA_100': '#0891b2',
    'EMA_200': '#0e7490',
    'SMMA_20': '#10b981',
    'SMMA_50': '#34d399',
    'SMMA_100': '#6ee7b7',
    'SMMA_200': '#059669',
    'RSI_14': '#f59e0b',
};

export function getIndColor(name: string): string {
    if (IND_COLORS[name]) return IND_COLORS[name];
    if (name.startsWith('SMA')) {
        const idx = Object.keys(IND_COLORS).filter(k => k.startsWith('SMA')).length;
        return SMA_PALETTE[idx % SMA_PALETTE.length];
    }
    if (name.startsWith('EMA')) {
        const idx = Object.keys(IND_COLORS).filter(k => k.startsWith('EMA')).length;
        return EMA_PALETTE[idx % EMA_PALETTE.length];
    }
    if (name.startsWith('SMMA')) {
        const idx = Object.keys(IND_COLORS).filter(k => k.startsWith('SMMA')).length;
        return SMMA_PALETTE[idx % SMMA_PALETTE.length];
    }
    return '#6366f1';
}

export function getEntryColor(entry: { name: string; color?: string }): string {
    return entry.color || getIndColor(entry.name);
}

export function entryKey(entry: { name: string; tf: number }): string {
    return entry.name + ':' + entry.tf;
}

export const IST_OFFSET = 5.5 * 3600; // +5:30 IST offset in seconds

export function tfLabel(tf: number): string {
    if (tf < 60) return tf + 's';
    if (tf < 3600) return (tf / 60) + 'm';
    return (tf / 3600) + 'h';
}

export function fmtTime(ts: string): string {
    const d = new Date(ts);
    return d.toLocaleTimeString('en-IN', { hour12: false });
}

/** Format a numeric value for display. Values are expected in rupees. */
export function fmtValue(v: number | null | undefined): string {
    if (v === undefined || v === null) return '--';
    return v.toFixed(2);
}

export function fmtUptime(sec: number): string {
    const h = Math.floor(sec / 3600);
    const m = Math.floor((sec % 3600) / 60);
    const s = sec % 60;
    const parts: string[] = [];
    if (h > 0) parts.push(h + 'h');
    if (m > 0 || h > 0) parts.push(m + 'm');
    parts.push(s + 's');
    return parts.join(' ');
}

export function parseChannel(ch: string) {
    const p = ch.split(':');
    if (p[1] === 'ind') {
        return { type: 'indicator' as const, name: p[2], tf: parseInt(p[3]), exchange: p[4], token: p[5] };
    }
    if (p[1] === 'candle') {
        return { type: 'candle' as const, tf: parseInt(p[2]), exchange: p[3], token: p[4] };
    }
    if (p[1] === 'tick') {
        return { type: 'tick' as const, exchange: p[2], token: p[3] };
    }
    return null;
}

// Constants
export const CHART_MAX = 1000;
export const FETCH_SIZE = 500;
export const CANDLE_MAX = 1000;
export const RECONNECT_BASE = 1000;
export const RECONNECT_MAX = 10000;
