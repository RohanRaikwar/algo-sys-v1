#!/bin/bash
# ═══════════════════════════════════════════════════════════
#  Frontend VM Setup — ONE-TIME (no Nginx)
#  Installs: Node.js 20 + serve (static file server)
#  Usage: sudo ./setup.sh
# ═══════════════════════════════════════════════════════════
set -euo pipefail

echo "╔═══════════════════════════════════════════════╗"
echo "║  Frontend VM Setup                           ║"
echo "╚═══════════════════════════════════════════════╝"

# ── 1. Install Node.js 20 LTS ──
if ! command -v node &>/dev/null; then
  echo "→ Installing Node.js 20 LTS..."
  curl -fsSL https://deb.nodesource.com/setup_20.x | bash -
  apt-get install -y -qq nodejs
fi
echo "  Node: $(node -v)"
echo "  npm:  $(npm -v)"

# ── 2. Install 'serve' globally (lightweight static server) ──
npm install -g serve
echo "  serve: $(serve --version)"

# ── 3. Create app directory ──
mkdir -p /opt/trading-frontend

echo ""
echo "✅ Frontend VM setup complete!"
echo "   Next: copy frontend code → /opt/trading-frontend, then run deploy.sh"
