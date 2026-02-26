#!/bin/bash
# ═══════════════════════════════════════════════════════════════════
#  FULL PRODUCTION DEPLOY — One command to set up + deploy backend
#  Run FROM YOUR LOCAL MACHINE (not the VM).
#
#  Usage:
#    ./deploy-production.sh <VM_IP> [SSH_USER] [SSH_KEY]
#
#  Examples:
#    ./deploy-production.sh 34.131.10.50
#    ./deploy-production.sh 34.131.10.50 ubuntu ~/.ssh/gcp_key
#
#  What it does:
#    1. Syncs code to VM via rsync
#    2. Installs Go, Redis on VM (if missing)
#    3. Builds 3 Go microservices on VM
#    4. Installs systemd service (auto-start on boot)
#    5. Starts the backend + health check
# ═══════════════════════════════════════════════════════════════════
set -euo pipefail

# ── Args (pre-filled for your EC2 instance) ──
VM_IP="${1:-ec2-13-220-208-254.compute-1.amazonaws.com}"
SSH_USER="${2:-ubuntu}"
SSH_KEY="${3:-RohanKeys.pem}"

SSH_OPTS="-o StrictHostKeyChecking=no -o ConnectTimeout=10"
if [ -n "$SSH_KEY" ]; then
  SSH_OPTS="$SSH_OPTS -i $SSH_KEY"
fi

SSH_CMD="ssh $SSH_OPTS $SSH_USER@$VM_IP"
RSYNC_SSH="ssh $SSH_OPTS"

REPO_ROOT="$(cd "$(dirname "$0")/.." && pwd)"
REMOTE_DIR="/opt/trading-backend"

echo "╔═════════════════════════════════════════════════════════════╗"
echo "║  Production Deploy → $VM_IP"
echo "║  User: $SSH_USER | Dir: $REMOTE_DIR"
echo "╚═════════════════════════════════════════════════════════════╝"
echo ""

# ── Step 1: Check SSH connectivity ──
echo "→ [1/6] Testing SSH connection..."
if ! $SSH_CMD "echo ok" > /dev/null 2>&1; then
  echo "❌ Cannot SSH to $SSH_USER@$VM_IP"
  echo "   Check: IP, username, SSH key, firewall"
  exit 1
fi
echo "  ✅ SSH connected"

# ── Step 2: Sync code to VM ──
echo "→ [2/6] Syncing code to VM..."
$SSH_CMD "mkdir -p $REMOTE_DIR"
rsync -az --delete \
  --exclude '.git' \
  --exclude 'node_modules' \
  --exclude 'frontend' \
  --exclude 'backend/tmp' \
  --exclude 'backend/data/*.db' \
  --exclude '*.db-journal' \
  -e "$RSYNC_SSH" \
  "$REPO_ROOT/backend/" "$SSH_USER@$VM_IP:~/trading-backend/"

# Copy .env file
if [ -f "$REPO_ROOT/.env" ]; then
  rsync -az -e "$RSYNC_SSH" "$REPO_ROOT/.env" "$SSH_USER@$VM_IP:~/trading-backend/.env"
  echo "  ✅ .env synced"
else
  echo "  ⚠️  No .env found at $REPO_ROOT/.env — create one on the VM!"
fi

# Move to /opt with sudo
$SSH_CMD "sudo rm -rf $REMOTE_DIR/cmd $REMOTE_DIR/internal $REMOTE_DIR/pkg $REMOTE_DIR/config $REMOTE_DIR/go.* 2>/dev/null; sudo cp -r ~/trading-backend/* $REMOTE_DIR/ && sudo cp ~/trading-backend/.env $REMOTE_DIR/.env 2>/dev/null; sudo chown -R root:root $REMOTE_DIR"
echo "  ✅ Code synced"

# ── Step 3: Install dependencies on VM ──
echo "→ [3/6] Installing dependencies (Go, Redis)..."
$SSH_CMD "sudo bash -s" << 'SETUP_SCRIPT'
set -euo pipefail
export DEBIAN_FRONTEND=noninteractive

# Install Redis + build tools
if ! command -v redis-server &>/dev/null; then
  apt-get update -qq
  apt-get install -y -qq build-essential redis-server sqlite3 curl
  systemctl enable redis-server
  systemctl start redis-server
  echo "  Installed Redis"
else
  echo "  Redis already installed"
fi

# Install Go
GO_VERSION="1.21.13"
if ! command -v go &>/dev/null; then
  curl -sL "https://go.dev/dl/go${GO_VERSION}.linux-amd64.tar.gz" -o /tmp/go.tar.gz
  rm -rf /usr/local/go
  tar -C /usr/local -xzf /tmp/go.tar.gz
  rm /tmp/go.tar.gz
  echo 'export PATH=$PATH:/usr/local/go/bin' > /etc/profile.d/go.sh
  echo "  Installed Go $GO_VERSION"
else
  echo "  Go already installed: $(go version)"
fi

# Create app user
if ! id trading &>/dev/null; then
  useradd -r -s /bin/bash -m trading
  echo "  Created user: trading"
fi

mkdir -p /opt/trading-backend/{bin,data,logs}
chown -R trading:trading /opt/trading-backend
SETUP_SCRIPT
echo "  ✅ Dependencies ready"

# ── Step 4: Build microservices ──
echo "→ [4/6] Building Go microservices on VM..."
$SSH_CMD "sudo bash -s" << 'BUILD_SCRIPT'
set -euo pipefail
export PATH=$PATH:/usr/local/go/bin
cd /opt/trading-backend

CGO_ENABLED=1 go build -o bin/mdengine    ./cmd/mdengine/
CGO_ENABLED=1 go build -o bin/indengine   ./cmd/indengine/
CGO_ENABLED=1 go build -o bin/api_gateway ./cmd/api_gateway/

echo "  Built: mdengine, indengine, api_gateway"
BUILD_SCRIPT
echo "  ✅ Binaries built"

# ── Step 5: Install systemd service ──
echo "→ [5/6] Installing systemd service..."
$SSH_CMD "sudo bash -s" << 'SERVICE_SCRIPT'
set -euo pipefail
APP_DIR="/opt/trading-backend"

# Create run script
cat > "$APP_DIR/bin/run-backend.sh" << 'RUN'
#!/bin/bash
set -a
source /opt/trading-backend/.env
set +a
export STAGING_MODE=false

trap 'kill $(jobs -p) 2>/dev/null; exit 0' SIGINT SIGTERM

/opt/trading-backend/bin/mdengine    2>&1 | sed 's/^/[mdengine]    /' &
/opt/trading-backend/bin/indengine   2>&1 | sed 's/^/[indengine]   /' &
/opt/trading-backend/bin/api_gateway 2>&1 | sed 's/^/[api_gateway] /' &

wait
RUN
chmod +x "$APP_DIR/bin/run-backend.sh"

# systemd unit
cat > /etc/systemd/system/trading-backend.service << UNIT
[Unit]
Description=Trading System Backend (mdengine + indengine + api_gateway)
After=network.target redis-server.service
Requires=redis-server.service

[Service]
Type=simple
User=trading
Group=trading
WorkingDirectory=/opt/trading-backend
EnvironmentFile=/opt/trading-backend/.env
ExecStart=/opt/trading-backend/bin/run-backend.sh
Restart=on-failure
RestartSec=5
StandardOutput=journal
StandardError=journal
SyslogIdentifier=trading-backend
NoNewPrivileges=true
ProtectSystem=strict
ReadWritePaths=/opt/trading-backend/data /opt/trading-backend/logs

[Install]
WantedBy=multi-user.target
UNIT

systemctl daemon-reload
systemctl enable trading-backend
SERVICE_SCRIPT
echo "  ✅ systemd service installed"

# ── Step 6: Start + health check ──
echo "→ [6/6] Starting backend..."
$SSH_CMD "sudo systemctl restart trading-backend && sleep 3"

HEALTH=$($SSH_CMD "curl -sf http://localhost:9090/api/config 2>&1" || echo "FAIL")
if [ "$HEALTH" != "FAIL" ]; then
  echo "  ✅ API Gateway responding on :9090"
else
  echo "  ⚠️  API Gateway not ready yet — check logs:"
  echo "     ssh $SSH_USER@$VM_IP journalctl -u trading-backend -f"
fi

echo ""
echo "╔═════════════════════════════════════════════════════════════╗"
echo "║  ✅ Production backend deployed!                          ║"
echo "║                                                            ║"
echo "║  API:     http://$VM_IP:9090                      ║"
echo "║  WS:      ws://$VM_IP:9090/ws                     ║"
echo "║  Metrics: http://$VM_IP:9091/metrics              ║"
echo "║  Health:  http://$VM_IP:9091/healthz              ║"
echo "║                                                            ║"
echo "║  Commands:                                                 ║"
echo "║    Status:  ssh -i $SSH_KEY $SSH_USER@$VM_IP sudo systemctl status trading-backend  ║"
echo "║    Logs:    ssh -i $SSH_KEY $SSH_USER@$VM_IP sudo journalctl -u trading-backend -f  ║"
echo "║    Restart: ssh -i $SSH_KEY $SSH_USER@$VM_IP sudo systemctl restart trading-backend ║"
echo "╚═════════════════════════════════════════════════════════════╝"
