import { useState, useEffect } from 'react';
import { QueryClient, QueryClientProvider } from '@tanstack/react-query';
import { useAppStore } from './store/useAppStore';
import { useWebSocket } from './hooks/useWebSocket';
import { fetchConfig, fetchActiveConfig } from './services/api';
import { Header } from './components/layout/Header';
import { StatusBar } from './components/layout/StatusBar';
import { ReconnectBanner } from './components/layout/ReconnectBanner';
import { TradingChart } from './components/chart/TradingChart';
import { SystemHealth } from './components/health/SystemHealth';
import { SettingsModal } from './components/settings/SettingsModal';
import { ErrorBoundary } from './components/ErrorBoundary';

const queryClient = new QueryClient({
    defaultOptions: { queries: { refetchOnWindowFocus: false } },
});

function Dashboard() {
    const { setConfig, setSelectedToken, setSelectedTF, setActiveEntries } = useAppStore();
    const [settingsOpen, setSettingsOpen] = useState(false);
    const [ready, setReady] = useState(false);

    // Connect WebSocket
    useWebSocket();

    // Load config on mount
    useEffect(() => {
        (async () => {
            try {
                const cfg = await fetchConfig();
                setConfig(cfg);
                if (cfg.tokens.length > 0) setSelectedToken(cfg.tokens[0]);
                if (cfg.tfs.length > 0) setSelectedTF(cfg.tfs[0]);

                // Load active indicator config
                try {
                    const saved = await fetchActiveConfig();
                    if (saved?.entries?.length > 0) {
                        setActiveEntries(saved.entries);
                    } else {
                        throw new Error('empty');
                    }
                } catch {
                    // Auto-populate from server config
                    const defaultTF = cfg.tfs[0] || 60;
                    const serverInds = (cfg.indicators || []).filter((n) => !n.startsWith('RSI'));
                    setActiveEntries(serverInds.map((name) => ({ name, tf: defaultTF })));
                }

                setReady(true);
            } catch (e) {
                console.warn('Config fetch failed, using defaults', e);
                setConfig({ tfs: [60, 300, 900], tokens: ['NSE:99926000'], indicators: ['SMA_20', 'SMA_50', 'SMA_200', 'EMA_9', 'EMA_21'] });
                setReady(true);
            }
        })();
    }, []);

    if (!ready) {
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
            <main style={{ padding: '24px 28px', maxWidth: 1600, margin: '0 auto', paddingBottom: 60 }}>
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
