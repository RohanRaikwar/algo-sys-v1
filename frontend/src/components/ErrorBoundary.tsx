import { Component, type ReactNode, type ErrorInfo } from 'react';

interface Props { children: ReactNode; fallback?: ReactNode }
interface State { hasError: boolean; error: Error | null }

export class ErrorBoundary extends Component<Props, State> {
    state: State = { hasError: false, error: null };

    static getDerivedStateFromError(error: Error) {
        return { hasError: true, error };
    }

    componentDidCatch(error: Error, info: ErrorInfo) {
        console.error('[ErrorBoundary] caught:', error, info.componentStack);
    }

    render() {
        if (this.state.hasError) {
            return this.props.fallback || (
                <div style={{
                    padding: '24px', margin: '16px', borderRadius: '12px',
                    background: 'rgba(239,68,68,0.1)', border: '1px solid rgba(239,68,68,0.3)',
                    color: '#ef4444', fontFamily: 'monospace', fontSize: 13,
                }}>
                    <strong>Chart Error:</strong> {this.state.error?.message || 'Unknown error'}
                    <br /><br />
                    <button
                        onClick={() => this.setState({ hasError: false, error: null })}
                        style={{ padding: '6px 16px', background: '#6366f1', color: '#fff', border: 'none', borderRadius: 6, cursor: 'pointer' }}
                    >
                        Retry
                    </button>
                </div>
            );
        }
        return this.props.children;
    }
}
