import { useEffect, useRef, useCallback } from 'react';
import { useWSStore } from '../store/useWSStore';
import { useAppStore } from '../store/useAppStore';
import { useCandleStore } from '../store/useCandleStore';
import { parseChannel, RECONNECT_BASE, RECONNECT_MAX, fmtTime, IST_OFFSET } from '../utils/helpers';
import type {
    WSEnvelope, CandlePayload, IndicatorPayload,
    SubscribeMsg, IndicatorSpecMsg,
} from '../types/ws';

// Global WS ref so sendSubscribe can be called from outside the hook
let globalWs: WebSocket | null = null;

/**
 * Convert activeEntries (name like "SMA_9") to IndicatorSpec for SUBSCRIBE.
 * E.g. "SMA_9" → { id: "sma", source: "close", params: { length: 9 } }
 */
function entryToSpec(name: string): IndicatorSpecMsg {
    const parts = name.split('_');
    const id = (parts[0] || 'sma').toLowerCase();
    const length = parseInt(parts[1] || '14', 10) || 14;
    return { id, source: 'close', params: { length } };
}

let subIdCounter = 0;

/**
 * Send a SUBSCRIBE message over WebSocket.
 */
export function sendSubscribe(
    symbol: string,
    tf: number,
    entries: Array<{ name: string; tf: number }>,
    candleCount = 500,
) {
    if (!globalWs || globalWs.readyState !== WebSocket.OPEN) {
        console.warn('[ws] cannot SUBSCRIBE: not connected');
        return;
    }

    const indicators = entries.map(e => entryToSpec(e.name));
    const msg: SubscribeMsg = {
        type: 'SUBSCRIBE',
        reqId: `r${++subIdCounter}`,
        symbol,
        tf,
        history: { candles: candleCount },
        indicators,
    };

    globalWs.send(JSON.stringify(msg));
    console.log('[ws] SUBSCRIBE sent', msg);
}

export function useWebSocket() {
    const wsRef = useRef<WebSocket | null>(null);
    const pingRef = useRef<ReturnType<typeof setInterval> | null>(null);
    const subscribedRef = useRef(false);

    const {
        setConnected, incrementMsg, setLastMsgTS,
        reconnectAttempts, setReconnectAttempts,
        setWsDelay, setMarket, setMetrics,
        setLastUpdateTime, setLatency, lastMsgTS,
    } = useWSStore();

    const { config, setAllActiveEntries } = useAppStore();
    const { upsertCandle, aggregateToTF, updateIndicator, setSnapshot } = useCandleStore();

    useEffect(() => {
        let reconnectTimer: ReturnType<typeof setTimeout> | null = null;
        let attempts = 0;

        function connect() {
            const proto = location.protocol === 'https:' ? 'wss:' : 'ws:';
            let url = `${proto}//${location.host}/ws`;
            const currentLastTS = useWSStore.getState().lastMsgTS;
            if (currentLastTS) {
                url += `?last_ts=${encodeURIComponent(currentLastTS)}`;
            }

            const ws = new WebSocket(url);
            wsRef.current = ws;
            globalWs = ws;

            ws.onopen = () => {
                setConnected(true);
                attempts = 0;
                setReconnectAttempts(0);
                subscribedRef.current = false;

                // Auto-subscribe with current state
                const state = useAppStore.getState();
                const token = state.selectedToken;
                const tf = state.selectedTF || 60;
                const entries = state.activeEntriesByTF[tf] || [];

                if (token && entries.length > 0) {
                    setTimeout(() => sendSubscribe(token, tf, entries), 100);
                    subscribedRef.current = true;
                }
            };

            ws.onclose = () => {
                setConnected(false);
                globalWs = null;
                subscribedRef.current = false;
                attempts++;
                setReconnectAttempts(attempts);
                const delay = Math.min(RECONNECT_BASE * Math.pow(2, attempts - 1), RECONNECT_MAX);
                reconnectTimer = setTimeout(connect, delay);
            };

            ws.onerror = () => {
                // onclose will handle reconnection
            };

            ws.onmessage = (evt) => {
                // Server may batch multiple JSON objects with newline separators
                const rawData: string = evt.data;
                const lines = rawData.indexOf('\n') >= 0 ? rawData.split('\n') : [rawData];

                for (const line of lines) {
                    if (!line) continue;

                    incrementMsg();
                    try {
                        const envelope: WSEnvelope = JSON.parse(line);
                        if (envelope.ts) {
                            setLastMsgTS(envelope.ts);
                            setLastUpdateTime(fmtTime(envelope.ts));
                            const serverMs = new Date(envelope.ts).getTime();
                            const lat = Date.now() - serverMs;
                            if (!isNaN(lat) && lat >= 0 && lat < 30000) {
                                setLatency(lat);
                            }
                        }

                        // ── SNAPSHOT response ──
                        if (envelope.type === 'SNAPSHOT') {
                            handleSnapshot(envelope);
                            continue;
                        }

                        // ── ERROR response ──
                        if (envelope.type === 'ERROR') {
                            console.error('[ws] server error:', envelope.error, 'reqId:', envelope.reqId);
                            continue;
                        }

                        // ── Config update — group entries by TF ──
                        if (envelope.type === 'config_update' && envelope.entries) {
                            const byTF: Record<number, { name: string; tf: number; color?: string }[]> = {};
                            for (const e of envelope.entries as { name: string; tf: number; color?: string }[]) {
                                if (!byTF[e.tf]) byTF[e.tf] = [];
                                byTF[e.tf].push(e);
                            }
                            setAllActiveEntries(byTF);
                            continue;
                        }

                        // ── Metrics ──
                        if (envelope.type === 'metrics' && envelope.metrics) {
                            setMetrics(envelope.metrics);
                            setMarket(!!envelope.marketOpen, envelope.marketStatus || '');
                            continue;
                        }

                        // ── Pong ──
                        if (envelope.type === 'pong' && envelope.ping) {
                            setWsDelay(Date.now() - envelope.ping);
                            continue;
                        }

                        // ── Data messages (legacy channel-based + new protocol) ──
                        if (!envelope.channel) continue;
                        const parsed = parseChannel(envelope.channel);
                        if (!parsed) continue;

                        let payload: Record<string, unknown>;
                        if (typeof envelope.data === 'string') {
                            try { payload = JSON.parse(envelope.data as string); } catch { payload = envelope.data as unknown as Record<string, unknown>; }
                        } else {
                            payload = envelope.data as Record<string, unknown>;
                        }

                        if (parsed.type === 'candle') {
                            const d = payload as unknown as CandlePayload;
                            const tf = d.tf || parsed.tf || 0;
                            upsertCandle(tf, {
                                ts: d.ts, open: d.open, high: d.high, low: d.low,
                                close: d.close, volume: d.volume, count: d.count,
                                forming: d.forming, exchange: d.exchange, token: d.token,
                            });
                            // Aggregate 1s candles into other TFs
                            if (tf === 1 || parsed.tf === 1) {
                                const activeTFs = useAppStore.getState().config.tfs || [60];
                                aggregateToTF(activeTFs, {
                                    ts: d.ts, open: d.open, high: d.high, low: d.low,
                                    close: d.close, volume: d.volume, count: d.count,
                                    forming: true, exchange: d.exchange, token: d.token,
                                });
                            }
                        }

                        if (parsed.type === 'indicator') {
                            const d = payload as unknown as IndicatorPayload;
                            const key = (d.name || parsed.name || '') + ':' + (d.tf || parsed.tf || 0);
                            updateIndicator(
                                key, d.name || parsed.name || '', d.tf || parsed.tf || 0,
                                d.value, d.ts, d.ready, !!d.live, d.exchange, d.token
                            );
                        }
                    } catch (e) {
                        console.warn('[ws] parse error', e);
                    }
                }
            };
        }

        /**
         * Handle SNAPSHOT response: bulk-set candles + indicator histories.
         */
        function handleSnapshot(envelope: WSEnvelope) {
            const tf = envelope.tf || 60;
            const symbol = envelope.symbol || '';
            const parts = symbol.split(':');
            const exchange = parts[0] || '';
            const token = parts[1] || '';

            // Convert snapshot candles to CandleRaw format
            const candles = (envelope.candles || []).map(c => ({
                ts: c.ts,
                open: c.open,
                high: c.high,
                low: c.low,
                close: c.close,
                volume: c.volume,
                count: c.count || 0,
                forming: false,
                exchange,
                token,
            }));

            // Convert snapshot indicator points to { time, value } arrays
            const indHistories: Record<string, Array<{ time: number; value: number }>> = {};
            if (envelope.indicators) {
                for (const [name, points] of Object.entries(envelope.indicators)) {
                    indHistories[name] = (points || [])
                        .filter(p => p.ready && p.ts)
                        .map(p => ({
                            time: Math.floor(new Date(p.ts).getTime() / 1000) + IST_OFFSET,
                            value: p.value,
                        }));
                }
            }

            setSnapshot(tf, candles, indHistories, token, exchange);
            console.log(
                `[ws] SNAPSHOT applied: tf=${tf} candles=${candles.length} indicators=${Object.keys(indHistories).length}`,
            );
        }

        connect();

        // Ping every 5s
        pingRef.current = setInterval(() => {
            if (wsRef.current?.readyState === WebSocket.OPEN) {
                wsRef.current.send(JSON.stringify({ ping: Date.now() }));
            }
        }, 5000);

        return () => {
            if (reconnectTimer) clearTimeout(reconnectTimer);
            if (pingRef.current) clearInterval(pingRef.current);
            if (wsRef.current) {
                wsRef.current.onclose = null; // prevent reconnect on unmount
                wsRef.current.close();
            }
            globalWs = null;
        };
    }, []); // connect once on mount
}
