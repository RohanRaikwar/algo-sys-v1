import { defineConfig } from 'vite';
import react from '@vitejs/plugin-react';
import path from 'path';

export default defineConfig({
    plugins: [react()],
    resolve: {
        alias: {
            '@': path.resolve(__dirname, './src'),
        },
    },
    server: {
        port: 5173,
        proxy: {
            '/api': {
                target: 'http://localhost:9090',
                changeOrigin: true,
                configure: (proxy) => {
                    proxy.on('error', () => { });
                },
            },
            '/ws': {
                target: 'ws://localhost:9090',
                ws: true,
                configure: (proxy) => {
                    proxy.on('error', () => { });
                    proxy.on('proxyReqWs', (_proxyReq, _req, socket) => {
                        socket.on('error', () => { });
                    });
                },
            },
        },
    },
    build: {
        outDir: 'dist',
        sourcemap: false,
    },
});
