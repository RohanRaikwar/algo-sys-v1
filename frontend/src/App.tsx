import { useState, useEffect } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useAppStore } from './store/useAppStore';
import { useWebSocket } from './hooks/useWebSocket';
import { useConfigQuery } from './hooks/useConfigQuery';
import { Header } from './components/layout/Header';
import { StatusBar } from './components/layout/StatusBar';
import { ReconnectBanner } from './components/layout/ReconnectBanner';
import { TradingChart } from './components/chart/TradingChart';
import { SystemHealth } from './components/health/SystemHealth';
import { SettingsModal } from './components/settings/SettingsModal';
import { ErrorBoundary } from './components/ErrorBoundary';
import styles from './App.module.css';

const queryClient = new QueryClient({
    defaultOptions: { queries: { refetchOnWindowFocus: false } },
});

function Dashboard() {
    const setConfig = useAppStore(s => s.setConfig);
    const setSelectedToken = useAppStore(s => s.setSelectedToken);
    const setSelectedTF = useAppStore(s => s.setSelectedTF);
    const [settingsOpen, setSettingsOpen] = useState(false);

    // Connect WebSocket
    useWebSocket();

    // Fetch config via React Query
    const { data: cfg, isLoading: cfgLoading, isError: cfgError } = useConfigQuery();

    // Apply config when loaded
    useEffect(() => {
        if (!cfg) return;
        setConfig(cfg);
        if (cfg.tokens.length > 0) setSelectedToken(cfg.tokens[0]);
        if (cfg.tfs.length > 0) setSelectedTF(cfg.tfs[0]);
    }, [cfg, setConfig, setSelectedToken, setSelectedTF]);

    // Apply defaults on config fetch error
    useEffect(() => {
        if (cfgError && !cfg) {
            const defaults = { tfs: [60, 300, 900], tokens: ['NSE:99926000'], indicators: ['SMA_20', 'SMA_50', 'SMA_200', 'EMA_9', 'EMA_21'] };
            setConfig(defaults);
        }
    }, [cfgError, cfg, setConfig]);

    // Loading state — all hooks must be declared ABOVE this early return
    if (cfgLoading) {
        return (
            <div style={{
                display: 'flex', alignItems: 'center', justifyContent: 'center',
                height: '100vh', color: 'var(--text-muted)', fontFamily: 'var(--font)',
            }}>
                Loading…
            </div>
        );
    }

    return (
        <>
            <ReconnectBanner />
            <Header onOpenSettings={() => setSettingsOpen(true)} />
            <main className={styles.main}>
                <ErrorBoundary>
                    <TradingChart />
                </ErrorBoundary>
                <SystemHealth />
            </main>
            <StatusBar />
            <SettingsModal open={settingsOpen} onClose={() => setSettingsOpen(false)} />
        </>
    );
}

export default function App() {
    return (
        <QueryClientProvider client={queryClient}>
            <Dashboard />
        </QueryClientProvider>
    );
}
