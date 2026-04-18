# Deployment Guide

## Architecture

```
Machine (systemd):  cloudflared tunnel
Docker Compose:     spread-arbitrage + VictoriaLogs + Promtail + Grafana
```

Cloudflared runs on the host (not in Docker) so you can still SSH in if Docker crashes.

## Prerequisites

- A server with Docker + Docker Compose
- A Cloudflare account with a domain

## 1. Install Cloudflared on Host

```bash
# Ubuntu/Debian
curl -L https://github.com/cloudflare/cloudflared/releases/latest/download/cloudflared-linux-amd64.deb -o cloudflared.deb
sudo dpkg -i cloudflared.deb

# Login and create tunnel
cloudflared tunnel login
cloudflared tunnel create spread-arb

# Copy config
sudo mkdir -p /etc/cloudflared
sudo cp ~/.cloudflared/<TUNNEL_ID>.json /etc/cloudflared/credentials.json
sudo cp deploy/cloudflared/config.yml /etc/cloudflared/config.yml
# Edit /etc/cloudflared/config.yml — fill in YOUR_TUNNEL_ID and YOUR_DOMAIN

# Add DNS route
cloudflared tunnel route dns spread-arb trade.example.com

# Install as systemd service
sudo cloudflared service install
sudo systemctl enable cloudflared
sudo systemctl start cloudflared
```

## 2. Setup Cloudflare Access (OAuth)

1. Go to Cloudflare Zero Trust Dashboard → Access → Applications
2. Add application → Self-hosted
3. Set domain to `trade.example.com`
4. Add authentication policy (e.g., allow specific email addresses)

## 3. Configure API Keys

Edit `spread-arbitrage/.env` with your exchange API keys.

## 4. Deploy

```bash
cd deploy
docker compose up -d --build
```

## Access

- Trading UI: `https://trade.example.com/`
- Grafana Logs: `https://trade.example.com/grafana/` (default: admin/admin)
- Both ports (18527, 3000) are bound to 127.0.0.1 only — not exposed to internet

## Logs

Via Grafana → Explore → VictoriaLogs datasource.

Via CLI:
```bash
docker compose logs -f spread-arbitrage
docker compose logs -f --tail=100 spread-arbitrage | grep "REBALANCE"
```

## Update

```bash
cd deploy
git pull
docker compose up -d --build
```
