import { useEffect, useRef } from 'react';
import { useWSStore } from '../store/useWSStore';
import { useAppStore } from '../store/useAppStore';
import { useCandleStore } from '../store/useCandleStore';
import { parseChannel, RECONNECT_BASE, RECONNECT_MAX, fmtTime, IST_OFFSET } from '../utils/helpers';
import type {
    WSEnvelope, CandlePayload, IndicatorPayload,
    SubscribeMsg, IndicatorSpecMsg,
} from '../types/ws';

/**
 * Convert an IndicatorEntry to an IndicatorSpec for SUBSCRIBE.
 * E.g. {name:"SMA_9", tf:300} → { id: "sma", source: "close", params: { length: 9 }, tf: 300 }
 */
function entryToSpec(entry: { name: string; tf: number }): IndicatorSpecMsg {
    const parts = entry.name.split('_');
    const id = (parts[0] || 'sma').toLowerCase();
    const length = parseInt(parts[1] || '14', 10) || 14;
    return { id, source: 'close', params: { length }, tf: entry.tf };
}

let subIdCounter = 0;

/**
 * Send a SUBSCRIBE message over the store-managed WebSocket.
 */
export function sendSubscribe(
    symbol: string,
    tf: number,
    entries: Array<{ name: string; tf: number }>,
    candleCount = 500,
) {
    const { send } = useWSStore.getState();

    const indicators = entries.map(e => entryToSpec(e));
    const msg: SubscribeMsg = {
        type: 'SUBSCRIBE',
        reqId: `r${++subIdCounter}`,
        symbol,
        tf,
        history: { candles: candleCount },
        indicators,
    };

    send(JSON.stringify(msg));
    console.log('[ws] SUBSCRIBE sent', msg);
}

/**
 * Send an UNSUBSCRIBE message to drop a prior subscription.
 */
export function sendUnsubscribe(symbol: string, tf: number) {
    const { send } = useWSStore.getState();
    const msg = {
        type: 'UNSUBSCRIBE',
        reqId: `r${++subIdCounter}`,
        symbol,
        tf,
    };
    send(JSON.stringify(msg));
    console.log('[ws] UNSUBSCRIBE sent', msg);
}

export function useWebSocket() {
    const localWsRef = useRef<WebSocket | null>(null);
    const pingRef = useRef<ReturnType<typeof setInterval> | null>(null);
    const subscribedRef = useRef(false);

    // Use atomic selectors — each grabs only its own setter function,
    // preventing unnecessary re-renders from unrelated state changes.
    const setConnected = useWSStore(s => s.setConnected);
    const incrementMsg = useWSStore(s => s.incrementMsg);
    const setLastMsgTS = useWSStore(s => s.setLastMsgTS);
    const setReconnectAttempts = useWSStore(s => s.setReconnectAttempts);
    const setWsDelay = useWSStore(s => s.setWsDelay);
    const setMarket = useWSStore(s => s.setMarket);
    const setMetrics = useWSStore(s => s.setMetrics);
    const setLastUpdateTime = useWSStore(s => s.setLastUpdateTime);
    const setLatency = useWSStore(s => s.setLatency);
    const setWsRef = useWSStore(s => s.setWsRef);

    const setActiveIndicators = useAppStore(s => s.setActiveIndicators);

    const upsertCandle = useCandleStore(s => s.upsertCandle);
    const aggregateToTF = useCandleStore(s => s.aggregateToTF);
    const updateIndicator = useCandleStore(s => s.updateIndicator);
    const setSnapshot = useCandleStore(s => s.setSnapshot);

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
            localWsRef.current = ws;
            setWsRef(ws); // Store-managed WS ref

            ws.onopen = () => {
                setConnected(true);
                attempts = 0;
                setReconnectAttempts(0);
                subscribedRef.current = false;

                // Auto-subscribe with current state (global indicators)
                const state = useAppStore.getState();
                const token = state.selectedToken;
                const tf = state.selectedTF || 60;
                const entries = state.activeIndicators || [];

                if (token && entries.length > 0) {
                    setTimeout(() => sendSubscribe(token, tf, entries), 100);
                    subscribedRef.current = true;
                }
            };

            ws.onclose = () => {
                setConnected(false);
                setWsRef(null);
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

                        // ── Config update — set global indicator list ──
                        if (envelope.type === 'config_update' && envelope.entries) {
                            const entries = envelope.entries as { name: string; tf: number; color?: string }[];
                            // Dedup by name:tf
                            const deduped = [...new Map(entries.map(e => [`${e.name}:${e.tf}`, e])).values()];
                            setActiveIndicators(deduped);
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

                        // ── Gap detection via channel_seq ──
                        if (typeof envelope.channel_seq === 'number') {
                            const { channelSeqs } = useWSStore.getState();
                            const expectedSeq = (channelSeqs[envelope.channel] || 0) + 1;
                            const receivedSeq = envelope.channel_seq;

                            if (expectedSeq > 1 && receivedSeq > expectedSeq) {
                                console.warn(`[ws] gap detected on ${envelope.channel}: expected=${expectedSeq} got=${receivedSeq}`);
                                // Fire-and-forget backfill
                                fetch(`/api/missed?channel=${encodeURIComponent(envelope.channel)}&from_seq=${expectedSeq}&to_seq=${receivedSeq - 1}`)
                                    .then(r => r.json())
                                    .then(data => {
                                        if (data.messages && data.messages.length > 0) {
                                            console.log(`[ws] backfilled ${data.messages.length} missed messages for ${envelope.channel}`);
                                        }
                                    })
                                    .catch(err => console.warn('[ws] gap backfill failed:', err));
                            }
                            useWSStore.getState().setChannelSeq(envelope.channel, receivedSeq);
                        }

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
                        }))
                        .sort((a, b) => a.time - b.time);
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
            const ws = useWSStore.getState().wsRef;
            if (ws?.readyState === WebSocket.OPEN) {
                ws.send(JSON.stringify({ ping: Date.now() }));
            }
        }, 5000);

        return () => {
            if (reconnectTimer) clearTimeout(reconnectTimer);
            if (pingRef.current) clearInterval(pingRef.current);
            if (localWsRef.current) {
                localWsRef.current.onclose = null; // prevent reconnect on unmount
                localWsRef.current.close();
            }
            setWsRef(null);
        };
    }, []); // connect once on mount
}
