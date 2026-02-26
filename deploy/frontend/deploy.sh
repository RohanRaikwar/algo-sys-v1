#!/bin/bash
# ═══════════════════════════════════════════════════════════
#  Frontend Deploy — builds React app + runs with 'serve'
#  No Nginx — uses 'serve' (Node static server)
#  Usage: sudo ./deploy.sh <BACKEND_URL>
#  Example: sudo ./deploy.sh http://10.0.0.5:9090
# ═══════════════════════════════════════════════════════════
set -euo pipefail

BACKEND_URL="${1:-}"
FRONTEND_DIR="/opt/trading-frontend"
PORT="${2:-80}"

echo "╔═══════════════════════════════════════════════╗"
echo "║  Frontend Deploy (no Nginx)                  ║"
echo "╚═══════════════════════════════════════════════╝"

if [ -z "$BACKEND_URL" ]; then
  echo "❌ Usage: sudo ./deploy.sh <BACKEND_URL> [PORT]"
  echo "   Example: sudo ./deploy.sh http://10.0.0.5:9090"
  echo "   Example: sudo ./deploy.sh http://10.0.0.5:9090 3000"
  exit 1
fi

echo "  Backend URL: $BACKEND_URL"
echo "  Serve port:  $PORT"

# ── 1. Build frontend ──
echo "→ Building frontend..."
cd "$FRONTEND_DIR"

# Set the backend API URL for the production build
echo "VITE_API_URL=$BACKEND_URL" > .env.production.local

npm ci --silent
npm run build
echo "  ✅ Vite build complete"

# ── 2. Install systemd service using 'serve' ──
echo "→ Installing systemd service..."
SERVE_PATH=$(which serve)

cat > /etc/systemd/system/trading-frontend.service << EOF
[Unit]
Description=Trading Frontend (serve)
After=network.target

[Service]
Type=simple
ExecStart=$SERVE_PATH -s $FRONTEND_DIR/dist -l $PORT
Restart=on-failure
RestartSec=3
StandardOutput=journal
StandardError=journal
SyslogIdentifier=trading-frontend

[Install]
WantedBy=multi-user.target
EOF

systemctl daemon-reload
systemctl enable trading-frontend
systemctl restart trading-frontend
sleep 1

# ── 3. Health check ──
if curl -sf http://localhost:$PORT > /dev/null 2>&1; then
  echo "  ✅ Frontend serving on :$PORT"
else
  echo "  ⚠️  Not responding yet (check: journalctl -u trading-frontend -f)"
fi

echo ""
echo "╔═══════════════════════════════════════════════╗"
echo "║  ✅ Frontend deployed!                       ║"
echo "║  URL:     http://<THIS_VM_IP>:$PORT"
echo "║  Backend: $BACKEND_URL"
echo "║  Logs:    journalctl -u trading-frontend -f  ║"
echo "╚═══════════════════════════════════════════════╝"
