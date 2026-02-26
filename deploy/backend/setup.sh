#!/bin/bash
# ═══════════════════════════════════════════════════════════
#  Backend VM Setup — ONE-TIME
#  Installs: Go, Redis, creates app user + directories
#  Usage: sudo ./setup.sh
# ═══════════════════════════════════════════════════════════
set -euo pipefail

echo "╔═══════════════════════════════════════════════╗"
echo "║  Backend VM Setup                            ║"
echo "╚═══════════════════════════════════════════════╝"

# ── System packages ──
echo "→ Installing system dependencies..."
apt-get update -qq
apt-get install -y -qq build-essential git curl redis-server sqlite3

# ── Install Go 1.21 ──
GO_VERSION="1.21.13"
if ! command -v go &>/dev/null || [[ "$(go version)" != *"$GO_VERSION"* ]]; then
  echo "→ Installing Go $GO_VERSION..."
  curl -sL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  export PATH=$PATH:/usr/local/go/bin
fi
echo "  Go: $(go version)"

# ── Enable Redis ──
systemctl enable redis-server
systemctl start redis-server
echo "  Redis: $(redis-cli ping)"

# ── Create app user & directories ──
APP_USER="trading"
APP_DIR="/opt/trading-backend"

if ! id "$APP_USER" &>/dev/null; then
  useradd -r -s /bin/bash -m "$APP_USER"
  echo "  Created user: $APP_USER"
fi

mkdir -p "$APP_DIR"/{bin,data,logs}
chown -R "$APP_USER:$APP_USER" "$APP_DIR"

echo ""
echo "✅ Backend VM setup complete!"
echo "   Next: copy backend code + .env → $APP_DIR, then run deploy.sh"
