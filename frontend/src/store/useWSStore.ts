import { create } from 'zustand';
import type { SystemMetrics } from '../types/ws';

interface WSState {
    connected: boolean;
    msgCount: number;
    startTime: number;
    lastMsgTS: string | null;
    reconnectAttempts: number;
    wsDelay: number | null;
    marketOpen: boolean;
    marketStatus: string;
    metrics: SystemMetrics | null;
    lastUpdateTime: string | null;
    latency: number | null;

    // Per-channel sequence tracking for gap detection
    channelSeqs: Record<string, number>;

    // WebSocket instance managed by the store (replaces module-level global)
    wsRef: WebSocket | null;

    setConnected: (c: boolean) => void;
    incrementMsg: () => void;
    setLastMsgTS: (ts: string) => void;
    setReconnectAttempts: (n: number) => void;
    setWsDelay: (d: number) => void;
    setMarket: (open: boolean, status: string) => void;
    setMetrics: (m: SystemMetrics) => void;
    setLastUpdateTime: (t: string) => void;
    setLatency: (l: number) => void;
    setWsRef: (ws: WebSocket | null) => void;
    setChannelSeq: (channel: string, seq: number) => void;
    /** Send a message over the current WebSocket connection */
    send: (data: string) => void;
}

export const useWSStore = create<WSState>((set, get) => ({
    connected: false,
    msgCount: 0,
    startTime: Date.now(),
    lastMsgTS: null,
    reconnectAttempts: 0,
    wsDelay: null,
    marketOpen: false,
    marketStatus: '',
    metrics: null,
    lastUpdateTime: null,
    latency: null,
    wsRef: null,
    channelSeqs: {},

    setConnected: (connected) => set({ connected }),
    incrementMsg: () => set((s) => ({ msgCount: s.msgCount + 1 })),
    setLastMsgTS: (lastMsgTS) => set({ lastMsgTS }),
    setReconnectAttempts: (reconnectAttempts) => set({ reconnectAttempts }),
    setWsDelay: (wsDelay) => set({ wsDelay }),
    setMarket: (marketOpen, marketStatus) => set({ marketOpen, marketStatus }),
    setMetrics: (metrics) => set({ metrics }),
    setLastUpdateTime: (lastUpdateTime) => set({ lastUpdateTime }),
    setLatency: (latency) => set({ latency }),
    setWsRef: (wsRef) => set({ wsRef }),
    setChannelSeq: (channel, seq) => set((s) => ({
        channelSeqs: { ...s.channelSeqs, [channel]: seq },
    })),
    send: (data) => {
        const ws = get().wsRef;
        if (ws && ws.readyState === WebSocket.OPEN) {
            ws.send(data);
        } else {
            console.warn('[ws] cannot send: not connected');
        }
    },
}));

