import { useEffect, useRef } from 'react';
import { sendSubscribe, sendUnsubscribe } from '../../../hooks/useWebSocket';
import { entryKey } from '../../../utils/helpers';
import type { IndicatorEntry } from '../../../types/api';

/**
 * Sends SUBSCRIBE messages over WebSocket when TF, token, or active entries change.
 * Includes deduplication to avoid redundant re-subscribes.
 * Sends UNSUBSCRIBE for the previous subscription when switching symbol/TF.
 */
export function useChartSubscription(
    selectedTF: number,
    selectedToken: string | null,
    activeEntries: IndicatorEntry[],
) {
    const prevTFRef = useRef<number>(0);
    const prevTokenRef = useRef<string>('');
    const prevEntriesRef = useRef<string[]>([]);

    useEffect(() => {
        const tf = selectedTF || 60;
        const token = selectedToken || '';

        const tfOrTokenChanged = tf !== prevTFRef.current || token !== prevTokenRef.current;
        const currentKeys = activeEntries.map(e => entryKey(e));
        const prevKeys = new Set(prevEntriesRef.current);
        const entriesChanged = currentKeys.length !== prevEntriesRef.current.length ||
            currentKeys.some(k => !prevKeys.has(k));

        // Unsubscribe from previous subscription if symbol/TF changed
        if (tfOrTokenChanged && prevTokenRef.current && prevTFRef.current > 0) {
            sendUnsubscribe(prevTokenRef.current, prevTFRef.current);
        }

        prevTFRef.current = tf;
        prevTokenRef.current = token;
        prevEntriesRef.current = currentKeys;

        // Only re-subscribe if something meaningful changed
        if (!tfOrTokenChanged && !entriesChanged) return;

        if (token) {
            sendSubscribe(token, tf, activeEntries);
        }
    }, [selectedTF, selectedToken, activeEntries]);
}
