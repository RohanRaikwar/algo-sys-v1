import { create } from 'zustand';
import type { AppConfig, IndicatorEntry } from '../types/api';

const STORAGE_KEY = 'indicatorProfilesByTF';

/** Load per-TF profiles from localStorage */
function loadPersistedProfiles(): Record<number, IndicatorEntry[]> {
    try {
        const raw = localStorage.getItem(STORAGE_KEY);
        if (raw) return JSON.parse(raw);
    } catch { /* ignore */ }
    return {};
}

/** Save per-TF profiles to localStorage */
function persistProfiles(profiles: Record<number, IndicatorEntry[]>) {
    try {
        localStorage.setItem(STORAGE_KEY, JSON.stringify(profiles));
    } catch { /* ignore */ }
}

interface AppState {
    config: AppConfig;
    selectedToken: string | null;
    selectedTF: number;

    // Per-TF indicator profiles
    activeEntriesByTF: Record<number, IndicatorEntry[]>;

    setConfig: (config: AppConfig) => void;
    setSelectedToken: (token: string) => void;
    setSelectedTF: (tf: number) => void;

    /** Set indicators for a specific TF */
    setActiveEntriesForTF: (tf: number, entries: IndicatorEntry[]) => void;

    /** Bulk-set all TF profiles (used on initial config_update from server) */
    setAllActiveEntries: (byTF: Record<number, IndicatorEntry[]>) => void;

    /** Legacy: set entries for the currently selected TF */
    setActiveEntries: (entries: IndicatorEntry[]) => void;
}

export const useAppStore = create<AppState>((set, get) => ({
    config: { tfs: [], tokens: [], indicators: [] },
    selectedToken: null,
    selectedTF: 60,
    activeEntriesByTF: loadPersistedProfiles(),

    setConfig: (config) => set({ config }),
    setSelectedToken: (token) => set({ selectedToken: token }),
    setSelectedTF: (tf) => set({ selectedTF: tf }),

    setActiveEntriesForTF: (tf, entries) => set((s) => {
        const updated = { ...s.activeEntriesByTF, [tf]: entries };
        persistProfiles(updated);
        return { activeEntriesByTF: updated };
    }),

    setAllActiveEntries: (byTF) => set(() => {
        // Server config is source of truth for which indicators exist.
        // Only preserve user color overrides from localStorage.
        const persisted = loadPersistedProfiles();
        const merged: Record<number, IndicatorEntry[]> = {};
        for (const tfStr of Object.keys(byTF)) {
            const tf = Number(tfStr);
            const serverEntries = byTF[tf];
            const localEntries = persisted[tf];
            if (!localEntries || localEntries.length === 0) {
                merged[tf] = serverEntries;
            } else {
                // Keep server entries, apply local color overrides
                const colorMap = new Map(localEntries.map(e => [e.name, e.color]));
                merged[tf] = serverEntries.map(e => ({
                    ...e,
                    color: colorMap.get(e.name) || e.color,
                }));
            }
        }
        persistProfiles(merged);
        return { activeEntriesByTF: merged };
    }),

    setActiveEntries: (entries) => set((s) => {
        const updated = { ...s.activeEntriesByTF, [s.selectedTF]: entries };
        persistProfiles(updated);
        return { activeEntriesByTF: updated };
    }),
}));

// Selector: get active entries for a specific TF
export function selectActiveEntries(tf: number) {
    return (s: AppState) => s.activeEntriesByTF[tf] || [];
}
