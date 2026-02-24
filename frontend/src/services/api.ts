import type { AppConfig, ActiveConfig, CandleOut, IndPoint, IndicatorEntry } from '../types/api';

const BASE = '';

export async function fetchConfig(): Promise<AppConfig> {
    const res = await fetch(`${BASE}/api/config`);
    if (!res.ok) throw new Error('Config fetch failed');
    return res.json();
}

export async function fetchActiveConfig(): Promise<ActiveConfig> {
    const res = await fetch(`${BASE}/api/indicators/active`);
    if (!res.ok) throw new Error('Active config fetch failed');
    return res.json();
}

export async function saveActiveConfig(entries: IndicatorEntry[]): Promise<void> {
    const res = await fetch(`${BASE}/api/indicators/active`, {
        method: 'POST',
        headers: { 'Content-Type': 'application/json' },
        body: JSON.stringify({ entries }),
    });
    if (!res.ok) throw new Error('Failed to save config');
}

export async function fetchCandles(
    tf: number, token: string, limit: number, before?: string
): Promise<CandleOut[]> {
    let url = `${BASE}/api/candles?tf=${tf}&token=${encodeURIComponent(token)}&limit=${limit}`;
    if (before) url += `&before=${encodeURIComponent(before)}`;
    const res = await fetch(url);
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? data : [];
}

export async function fetchIndicatorHistory(
    name: string, tf: number, token: string, limit: number, before?: string
): Promise<IndPoint[]> {
    let url = `${BASE}/api/indicators/history?name=${encodeURIComponent(name)}&tf=${tf}&token=${encodeURIComponent(token)}&limit=${limit}`;
    if (before) url += `&before=${encodeURIComponent(before)}`;
    const res = await fetch(url);
    if (!res.ok) return [];
    const data = await res.json();
    return Array.isArray(data) ? data : [];
}
