import { create } from 'zustand';
import type { AppConfig, IndicatorEntry } from '../types/api';

interface AppState {
    config: AppConfig;
    selectedToken: string | null;
    selectedTF: number;
    activeEntries: IndicatorEntry[];

    setConfig: (config: AppConfig) => void;
    setSelectedToken: (token: string) => void;
    setSelectedTF: (tf: number) => void;
    setActiveEntries: (entries: IndicatorEntry[]) => void;
}

export const useAppStore = create<AppState>((set) => ({
    config: { tfs: [], tokens: [], indicators: [] },
    selectedToken: null,
    selectedTF: 60,
    activeEntries: [],

    setConfig: (config) => set({ config }),
    setSelectedToken: (token) => set({ selectedToken: token }),
    setSelectedTF: (tf) => set({ selectedTF: tf }),
    setActiveEntries: (entries) => set({ activeEntries: entries }),
}));
