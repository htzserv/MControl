# MControl v1.5.2 — Central Server, Tunnel, Cloud & DNS Management Panel (MDesign)

[![Release](https://img.shields.io/github/v/release/htzserv/MControl?style=flat-square&color=6366f1)](https://github.com/htzserv/MControl/releases)
[![Architecture](https://img.shields.io/badge/architecture-Go%20Single%20Binary-22d3ee?style=flat-square)](#)
[![Agent](https://img.shields.io/badge/Peer%20Agent-v4%20Go%20Static%20%2B%20TLS%20Pinning-10b981?style=flat-square)](#)

[🇮🇷 نسخه فارسی (Persian README)](README.fa.md)

**MControl** is a standalone, single-binary Go management panel for multi-node infrastructure (Iran & Foreign servers). Built on the **MDesign** theme engine with an embedded Web UI, PWA support, and AES-256 encrypted SQLite database, it unifies **Peer Agent v4**, **2-Sided Tunnel Orchestration**, **Datacenter Cloud Hub**, **Master Outbound (Xray Core)**, **Cloudflare & ArvanCloud DNS**, **Uptime Monitoring**, **Local & Remote Web Terminal + File Manager**, and a **Native Telegram Bot & Auto-Backuper**.

---

## Quick Install

### Stable Release (`v1.5.2`)
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash
```

With custom port (default `2053`):
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- 2053
```

### Rolling Beta Channel
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- --beta
```

---

## Key Features

### 1. Peer Agent v4 Ultimate & Multi-Source Ping
* **Zero-Dependency Go Agent (`cmd/mcontrol-agent`):** Static Go binary with automatic Python v4 fallback, TLS certificate pinning, Master-IP key sync, 5 fixed ports (`84`, `847`, `7483`, `24819`, `2054`), auto-discovery, and reverse heartbeat push (`/_mc_agent/heartbeat`).
* **3-Way Live Ping Monitoring:** Real-time **Direct Iran Ping (`🇮🇷`)**, **Outbound Ping (`🚀`)**, and **Reverse Ping (`↩️` Server → Master)** with dynamic latency-colored pills, one-click `📡 Ping` / `↻ Ping All`, and automatic background live sync.
* **Multi-Iran Reachability & Filtering Check:** Test direct ICMP & TCP connectivity to any foreign node simultaneously from all Iran servers registered in your panel.
* **Streamlined Add-Server Wizard:** Automatic IPv6 detection drawer in Step 1 (`GET /api/servers/resolve-ipv6`) and clean 3-row SSH credentials in Step 2 with auto-populating Saved SSH Profiles dropdown and minimal SVG password visibility toggle.

### 2. Datacenter Cloud Hub (`Datacenter` Tab)
* **5 Integrated Cloud Providers:** Encrypted API management for **Hetzner Cloud**, **DigitalOcean**, **Linode (Akamai)**, **OVHcloud**, and **YottaSrc**.
* **Full VM Lifecycle:** Create servers, Rebuild OS, Reboot/Power controls, 1-click Peer v4 Cloud Firewall, Auto-Import, and background **Auto-Pair with Peer Agent v4**.

### 3. Unified Tunnel Orchestration & MSmart
* **Supported Engines:** `Backhaul`, `Rathole`, `Paqet` (auto-bundled on Iran Mirror), `Realm`, `GRE`, `IP6GRE`, and `VXLAN` with automated 2-sided deployment, health watchdog, and `MSmart` auto-failover.
* **Static Zero-APT Bundle:** One-click installation of tunnel engines and network tools (`iperf3`, `speedtest`, `jq`, `socat`) from GitHub, ParsPack Iran Mirror, or Master Cache.

### 4. Unified Master Outbound (Xray Core)
* **Isolated Local SOCKS5 (`127.0.0.1:10808`):** Routes Master Panel traffic (GitHub, Telegram Bot, Foreign Nodes) without touching user tunnels.
* **Single Config, Subscriptions & Load Balance:** Clean single-URI bar (`vless://`, `vmess://`, `trojan://`, `ss://`, `socks5://`), collapsible **Subscriptions** with `Subscription-Userinfo` traffic/expiry meter & per-sub real delay ping, and optional **Load Balance & Auto-Failover** toggle cards.

### 5. Local & Remote Web Terminal + Full File Manager
* **Master & Remote Nodes:** Built-in Bash terminal with 1-click quick snippets (`System`, `Ports`, `MControl`, `Services`, `IPs`) and Script Vault.
* **Full Web File Manager:** Browse, edit, binary-safe **Download** & multi-file **Upload** (up to 20MB), create files/folders, rename, copy/duplicate, recursive delete (with system-path protection), and a full **Right-Click Context Menu** on both Master and remote servers.

### 6. MDesign Theme Engine & Per-Tab Control Center
* **Decoupled Design & Color Engine:** 30+ structural designs across 6 families (`Prism`, `Neon`, `Material`, `Studio`, `iOS`, `Android`) with independent Classic & Royal color palettes and live Accent pickers.
* **Inline Floating Bar Editor:** Right-click or hold (`0.5s`) on the top navigation bar to drag-reorder, remove (`−`), or add (`+`) tabs directly — synced to the panel SQLite database across devices.
* **Advanced Per-Tab Settings (`⚙`):** Configure refresh intervals, default server sorting/filtering, compact mode, vertical card alignment, and visibility for all 11 tabs (active or hidden), synced to the database.

### 7. Domains (DNS), Uptime Monitor & Telegram Bot
* **Cloudflare & ArvanCloud DNS:** Multi-provider zone management, automatic server IP matching, and DNS auto-failover.
* **Uptime Monitor & Public `/status`:** Multi-point HTTP/SSL, TCP, Ping & DNS monitors with heartbeat bars and dedicated notifications.
* **Telegram Bot & Auto-Backuper:** Inline keyboard management, encrypted database backups, and actionable alerts.

---

## CLI Management

After installation, manage MControl anytime via:
```bash
mcontrol
```
