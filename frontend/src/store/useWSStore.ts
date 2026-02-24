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

    setConnected: (c: boolean) => void;
    incrementMsg: () => void;
    setLastMsgTS: (ts: string) => void;
    setReconnectAttempts: (n: number) => void;
    setWsDelay: (d: number) => void;
    setMarket: (open: boolean, status: string) => void;
    setMetrics: (m: SystemMetrics) => void;
    setLastUpdateTime: (t: string) => void;
    setLatency: (l: number) => void;
}

export const useWSStore = create<WSState>((set) => ({
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

    setConnected: (connected) => set({ connected }),
    incrementMsg: () => set((s) => ({ msgCount: s.msgCount + 1 })),
    setLastMsgTS: (lastMsgTS) => set({ lastMsgTS }),
    setReconnectAttempts: (reconnectAttempts) => set({ reconnectAttempts }),
    setWsDelay: (wsDelay) => set({ wsDelay }),
    setMarket: (marketOpen, marketStatus) => set({ marketOpen, marketStatus }),
    setMetrics: (metrics) => set({ metrics }),
    setLastUpdateTime: (lastUpdateTime) => set({ lastUpdateTime }),
    setLatency: (latency) => set({ latency }),
}));
