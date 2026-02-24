import { useEffect, useRef } from 'react';
import { useWSStore } from '../store/useWSStore';
import { useAppStore } from '../store/useAppStore';
import { useCandleStore } from '../store/useCandleStore';
import { parseChannel, RECONNECT_BASE, RECONNECT_MAX, fmtTime } from '../utils/helpers';
import type { WSEnvelope, CandlePayload, IndicatorPayload } from '../types/ws';

export function useWebSocket() {
    const wsRef = useRef<WebSocket | null>(null);
    const pingRef = useRef<ReturnType<typeof setInterval> | null>(null);

    const {
        setConnected, incrementMsg, setLastMsgTS,
        reconnectAttempts, setReconnectAttempts,
        setWsDelay, setMarket, setMetrics,
        setLastUpdateTime, setLatency, lastMsgTS,
    } = useWSStore();

    const { config, setActiveEntries } = useAppStore();
    const { upsertCandle, aggregateToTF, updateIndicator } = useCandleStore();

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

            ws.onopen = () => {
                setConnected(true);
                attempts = 0;
                setReconnectAttempts(0);
            };

            ws.onclose = () => {
                setConnected(false);
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

                        // Config update
                        if (envelope.type === 'config_update' && envelope.entries) {
                            setActiveEntries(envelope.entries);
                            continue;
                        }

                        // Metrics
                        if (envelope.type === 'metrics' && envelope.metrics) {
                            setMetrics(envelope.metrics);
                            setMarket(!!envelope.marketOpen, envelope.marketStatus || '');
                            continue;
                        }

                        // Pong
                        if (envelope.type === 'pong' && envelope.ping) {
                            setWsDelay(Date.now() - envelope.ping);
                            continue;
                        }

                        // Data messages
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
        };
    }, []); // connect once on mount
}
