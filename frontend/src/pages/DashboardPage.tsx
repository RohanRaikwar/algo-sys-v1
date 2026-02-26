import { ErrorBoundary } from '../components/ErrorBoundary';
import { TradingChart } from '../components/chart/TradingChart';

export function DashboardPage() {
    return (
        <ErrorBoundary>
            <TradingChart />
        </ErrorBoundary>
    );
}

