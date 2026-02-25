import type { AppConfig, CandleOut, IndPoint, IndicatorEntry } from '../types/api';

const BASE = '';

export async function fetchConfig(): Promise<AppConfig> {
    const res = await fetch(`${BASE}/api/config`);
    if (!res.ok) throw new Error('Config fetch failed');
    return res.json();
}

/** Fetch per-TF active indicator config from backend */
export async function fetchActiveConfig(): Promise<Record<number, IndicatorEntry[]>> {
    const res = await fetch(`${BASE}/api/indicators/active`);
    if (!res.ok) throw new Error('Active config fetch failed');
    const data = await res.json();
    // Support both old format {entries:[...]} and new format {byTF:{60:[...],...}}
    if (data.byTF) return data.byTF;
    // Legacy: flat entries array — group by TF
    if (data.entries) {
        const byTF: Record<number, IndicatorEntry[]> = {};
        for (const e of data.entries) {
            if (!byTF[e.tf]) byTF[e.tf] = [];
            byTF[e.tf].push(e);
        }
        return byTF;
    }
    return {};
}

/** Save per-TF active indicator config to backend */
export async function saveActiveConfig(byTF: Record<number, IndicatorEntry[]>): Promise<void> {
    // Send as flat entries array for backward compat with current backend
    const entries: IndicatorEntry[] = [];
    for (const tf of Object.keys(byTF)) {
        for (const e of byTF[Number(tf)]) {
            entries.push({ ...e, tf: Number(tf) });
        }
    }
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
