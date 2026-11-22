# MControl — Central Server & Tunnel Management Panel (MDesign)

**MControl** is a standalone, single-binary Go management panel for multi-node infrastructure (Iran & Foreign servers), engineered with the **MDesign** theme engine (30 curated themes across `Neon`, `Material`, and `Studio`), **Peer Agent v2**, **Master Outbound (Xray Core)**, and a **Native Telegram Bot & Auto-Backuper**.

## Quick Install (Stable Release)

```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash
```

Custom port (default `2053`):
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- 2053
```

## Quick Install (Rolling Beta Channel)

Install directly from the latest `beta-latest` rolling pre-release build:
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- --beta
```

Rolling Beta with custom port:
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- 2053 --beta
```

## Key Features
- **Single Static Binary + Embedded UI + SQLite:** Zero runtime dependencies on the Master server.
- **Unified Tunnel Orchestrator & MSmart Benchmark:** Deploy and manage `Backhaul`, `Rathole`, `Paqet`, `GRE` (`4-to-4`, `6-to-4`, `6-to-6`), and `VXLAN` tunnels with Kernel VIP load balancing (`Round-Robin`, `Random`, `Lowest Ping`, `Lightest Load`, `Least Traffic`), 1-click in-tunnel Speedtest, visual 8-Ping tester, and saved benchmark history.
- **Master Outbound (Xray Core):** Isolated local SOCKS5 (`127.0.0.1:10808`) strictly for the Master Panel (GitHub updates, Telegram bot, Foreign control) with Subscription sync, Xray Observatory Load-Balancing + Auto-Failover, and Real Delay testing.
- **Native Telegram Bot & Auto-Backuper:** Full inline-keyboard tunnel wizard & management, scheduled Auto-Backuper (6/12/24/48h) to Admin or Backup Channel, Pre-Update auto-backups, Send-to-Restore `.zip` backups, actionable outage alerts (with Restart/Logs buttons), Daily Digest, and Emergency panel tools.
- **Peer Agent v2 (TLS) & Setup Studio:** Pair nodes via 1-click Peer Code and install OS prerequisites & tunnel engines from **GitHub**, **Iran Mirror**, or **Direct from Master**.
- **MDesign Theme Engine:** 30 curated high-contrast themes (`Neon`, `Material`, `Studio`) with 8-axis live Theme Studio.
- **CLI Manager:** Run `mcontrol` on the Master server for service control, port/web-path updates, emergency 2FA/HTTPS reset, and logs.

## Automatic Releases & Rolling Beta (GitHub Actions)
- **Official Release:** Bump `VERSION` (e.g., `1.2.1`) and push to `main` — GitHub Actions builds static `amd64`/`arm64` binaries + `.sha256` checksums and publishes an official Release.
- **Rolling Beta:** Push changes to `main` without changing `VERSION` — GitHub Actions automatically updates the `beta-latest` rolling pre-release without cluttering official releases.
