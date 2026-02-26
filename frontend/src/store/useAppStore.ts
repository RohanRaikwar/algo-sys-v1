import { create } from 'zustand';
import type { AppConfig, IndicatorEntry } from '../types/api';

const STORAGE_KEY = 'activeIndicators_v2';
const LEGACY_KEY = 'indicatorProfilesByTF';

/** Load global indicator list from localStorage */
function loadPersistedIndicators(): IndicatorEntry[] {
    try {
        // Try new key first
        const raw = localStorage.getItem(STORAGE_KEY);
        if (raw) return JSON.parse(raw);

        // Migrate from legacy per-TF format
        const legacy = localStorage.getItem(LEGACY_KEY);
        if (legacy) {
            const byTF: Record<number, IndicatorEntry[]> = JSON.parse(legacy);
            const flat = Object.values(byTF).flat();
            // Dedup by name:tf
            const deduped = [...new Map(flat.map(e => [`${e.name}:${e.tf}`, e])).values()];
            if (deduped.length > 0) {
                localStorage.setItem(STORAGE_KEY, JSON.stringify(deduped));
                localStorage.removeItem(LEGACY_KEY);
                return deduped;
            }
        }
    } catch { /* ignore */ }
    return [];
}

/** Persist global indicator list to localStorage */
function persistIndicators(indicators: IndicatorEntry[]) {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(indicators));
    } catch { /* ignore */ }
}

interface AppState {
    config: AppConfig;
    selectedToken: string | null;
    selectedTF: number;

    // Global indicator list (persists across TF changes)
    activeIndicators: IndicatorEntry[];

    setConfig: (config: AppConfig) => void;
    setSelectedToken: (token: string) => void;
    setSelectedTF: (tf: number) => void;

    /** Set the global indicator list */
    setActiveIndicators: (entries: IndicatorEntry[]) => void;

    /** Bulk-set from server per-TF format — flattens into global list (migration compat) */
    setAllActiveEntries: (byTF: Record<number, IndicatorEntry[]>) => void;
}

export const useAppStore = create<AppState>((set) => ({
    config: { tfs: [], tokens: [], indicators: [] },
    selectedToken: null,
    selectedTF: 60,
    activeIndicators: loadPersistedIndicators(),

    setConfig: (config) => set({ config }),
    setSelectedToken: (token) => set({ selectedToken: token }),
    setSelectedTF: (tf) => set({ selectedTF: tf }),

    setActiveIndicators: (entries) => set(() => {
        persistIndicators(entries);
        return { activeIndicators: entries };
    }),

    setAllActiveEntries: (byTF) => set(() => {
        // Flatten all TF entries into one global list
        const flat = Object.values(byTF).flat();
        // Dedup by name:tf
        const deduped = [...new Map(flat.map(e => [`${e.name}:${e.tf}`, e])).values()];

        // Prefer existing localStorage indicators if they exist
        const persisted = loadPersistedIndicators();
        if (persisted.length > 0) {
            persistIndicators(persisted);
            return { activeIndicators: persisted };
        }

        persistIndicators(deduped);
        return { activeIndicators: deduped };
    }),
}));
