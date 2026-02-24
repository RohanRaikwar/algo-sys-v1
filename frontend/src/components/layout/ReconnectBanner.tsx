import { useWSStore } from '../../store/useWSStore';

export function ReconnectBanner() {
    const { connected, reconnectAttempts } = useWSStore();

    if (connected || reconnectAttempts === 0) return null;

    return (
        <div style={{
            position: 'fixed', top: 0, left: 0, right: 0, zIndex: 200,
            background: 'linear-gradient(90deg, rgba(239,68,68,0.95), rgba(220,38,38,0.95))',
            color: '#fff', textAlign: 'center', padding: '10px 20px',
            fontSize: '0.85rem', fontWeight: 600, letterSpacing: '0.3px',
            backdropFilter: 'blur(8px)',
            animation: 'fadeIn 0.4s ease-out forwards',
        }}>
            ⚡ Connection lost — reconnecting (attempt #{reconnectAttempts})…
        </div>
    );
}
