# VM Deployment Guide — Trading System v1

Separate deployment for **backend** and **frontend** — same VM or different VMs.

## Architecture

```
┌─── Frontend VM ────────────────┐     ┌─── Backend VM ──────────────────┐
│                                │     │                                  │
│  serve (:80)                   │     │  systemd (trading-backend)       │
│    └── static dist/            │     │    ├── mdengine   (Angel feed)   │
│                                │     │    ├── indengine  (indicators)   │
│  JS connects directly ────────┼────→│    └── api_gateway (:9090)       │
│  to backend API + WS          │     │                                  │
│                                │     │  Redis (:6379) + SQLite          │
└────────────────────────────────┘     └──────────────────────────────────┘
```

---

## Backend

### Push code + setup (one-time)
```bash
rsync -avz --exclude node_modules --exclude tmp --exclude data \
  ~/Desktop/trading-systemv1/backend/ user@BACKEND_VM:/opt/trading-backend/

scp deploy/backend/*.sh user@BACKEND_VM:/opt/trading-backend/
scp .env user@BACKEND_VM:/opt/trading-backend/.env

ssh user@BACKEND_VM "cd /opt/trading-backend && sudo ./setup.sh"
```

### Deploy
```bash
ssh user@BACKEND_VM "cd /opt/trading-backend && sudo ./deploy.sh"
```

### Manage
```bash
sudo systemctl status trading-backend
sudo journalctl -u trading-backend -f
sudo systemctl restart trading-backend
```

---

## Frontend

### Push code + setup (one-time)
```bash
rsync -avz --exclude node_modules \
  ~/Desktop/trading-systemv1/frontend/ user@FRONTEND_VM:/opt/trading-frontend/

scp deploy/frontend/*.sh user@FRONTEND_VM:/opt/trading-frontend/

ssh user@FRONTEND_VM "cd /opt/trading-frontend && sudo ./setup.sh"
```

### Deploy (pass backend URL)
```bash
ssh user@FRONTEND_VM "cd /opt/trading-frontend && sudo ./deploy.sh http://BACKEND_IP:9090"

# Optional: custom port (default 80)
sudo ./deploy.sh http://BACKEND_IP:9090 3000
```

### Manage
```bash
sudo systemctl status trading-frontend
sudo journalctl -u trading-frontend -f
sudo systemctl restart trading-frontend
```

---

## Same VM?

```bash
sudo /opt/trading-backend/deploy.sh
sudo /opt/trading-frontend/deploy.sh http://127.0.0.1:9090
```

## Ports

| Port | Service | VM |
|------|---------|----|
| 80   | serve (frontend) | Frontend |
| 9090 | api_gateway | Backend |
| 9091 | Metrics | Backend |
| 9095 | indengine API | Backend |
| 6379 | Redis | Backend |
