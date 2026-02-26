import { useEffect, useRef, type MutableRefObject } from 'react';
import {
    createChart, CrosshairMode, PriceScaleMode, ColorType,
    type IChartApi, type ISeriesApi, type CandlestickData, type LineData,
    type MouseEventParams, type LineWidth,
} from 'lightweight-charts';

export interface ChartRefs {
    chartApi: MutableRefObject<IChartApi | null>;
    candleSeries: MutableRefObject<ISeriesApi<'Candlestick'> | null>;
    indLineSeries: MutableRefObject<Record<string, ISeriesApi<'Line'>>>;
    chartContainer: MutableRefObject<HTMLDivElement | null>;
}

/**
 * Creates and manages the lightweight-charts instance.
 * Handles chart creation, theme, resize observer, and cleanup.
 */
export function useChartInit(): ChartRefs {
    const chartContainer = useRef<HTMLDivElement | null>(null);
    const chartApi = useRef<IChartApi | null>(null);
    const candleSeries = useRef<ISeriesApi<'Candlestick'> | null>(null);
    const indLineSeries = useRef<Record<string, ISeriesApi<'Line'>>>({});

    useEffect(() => {
        if (!chartContainer.current) return;

        const chart = createChart(chartContainer.current, {
            layout: {
                background: { type: ColorType.Solid, color: '#0f172a' },
                textColor: '#9ca3af',
                fontFamily: "'Inter', sans-serif",
                fontSize: 11,
            },
            grid: {
                vertLines: { color: 'rgba(99, 102, 241, 0.06)' },
                horzLines: { color: 'rgba(99, 102, 241, 0.06)' },
            },
            crosshair: {
                mode: CrosshairMode.Normal,
                vertLine: { color: 'rgba(99, 102, 241, 0.3)', width: 1, style: 2, labelBackgroundColor: '#6366f1' },
                horzLine: { color: 'rgba(99, 102, 241, 0.3)', width: 1, style: 2, labelBackgroundColor: '#6366f1' },
            },
            rightPriceScale: {
                borderColor: 'rgba(99, 102, 241, 0.15)',
                scaleMargins: { top: 0.1, bottom: 0.1 },
                mode: PriceScaleMode.Normal,
                autoScale: true,
            },
            timeScale: {
                borderColor: 'rgba(99, 102, 241, 0.15)',
                timeVisible: true,
                secondsVisible: false,
                rightOffset: 5,
            },
            handleScroll: { mouseWheel: true, pressedMouseMove: true, horzTouchDrag: true, vertTouchDrag: true },
            handleScale: {
                mouseWheel: true, pinch: true,
                axisPressedMouseMove: { price: true, time: true },
                axisDoubleClickReset: { price: true, time: true },
            },
            width: chartContainer.current.clientWidth,
            height: 460,
        });

        chartApi.current = chart;
        candleSeries.current = chart.addCandlestickSeries({
            upColor: '#10b981', downColor: '#ef4444',
            borderDownColor: '#ef4444', borderUpColor: '#10b981',
            wickDownColor: 'rgba(239, 68, 68, 0.5)', wickUpColor: 'rgba(16, 185, 129, 0.5)',
        });

        // Resize observer
        const el = chartContainer.current;
        const ro = new ResizeObserver(() => {
            if (el) chart.applyOptions({ width: el.clientWidth });
        });
        ro.observe(el);

        return () => {
            ro.disconnect();
            chart.remove();
            chartApi.current = null;
            candleSeries.current = null;
            indLineSeries.current = {};
        };
    }, []);

    return { chartApi, candleSeries, indLineSeries, chartContainer };
}
