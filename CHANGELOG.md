# Changelog

## 1.2.3

### Tunnel Engine Stability & Hardening
- **NAT-Friendly Keepalives & Heartbeats:** Tuned Backhaul (`keepalive_period = 20`, `heartbeat = 10`, `retry_interval = 1`, `dial_timeout = 10`, `aggressive_pool = true`, full `mux` buffers) and Rathole (`heartbeat_interval = 15`, `heartbeat_timeout = 40`, `keepalive_secs = 15`, `keepalive_interval = 5`, `nodelay = true`) to eliminate periodic silent drops behind ISP NAT/firewalls.
- **Systemd Anti-Lock & Auto-Watchdog:** Added `StartLimitIntervalSec=0` and `RestartSec=1` across all `mcontrol-tun-*` services, plus a built-in background **Tunnel Watchdog** (`StartTunnelWatchdog`) that checks active tunnels every 45 seconds on both Iran and Foreign nodes and auto-restarts dropped units.
- **Kernel GRE / VXLAN Hardening:** Added `rp_filter=0`, `ip_forward`, bidirectional `FORWARD ACCEPT`, and `TCPMSS --clamp-mss-to-pmtu` rules to prevent packet fragmentation and asymmetric routing drops.
- **Native Paqet YAML Engine:** Rewrote Paqet deployment to use real `paqet run -c <file.yaml>` configs with automatic interface/IP/gateway MAC detection and automatic `iptables` `raw NOTRACK` + `mangle RST DROP` rule management and cleanup.
- **Security Fix:** Closed command-injection risk in tunnel creation by enforcing strict regex validation (`^[A-Za-z0-9._-]{1,64}$`) on tunnel names and tokens.

### MTunnel → MControl Pro Migrator
- **End-to-End Safe Migration Pipeline:** Automatic read-only scan on server add, AES-GCM encrypted verbatim snapshot (downloadable as `.json`), static engine bundle install & verification, clean MTunnel removal, and automatic/manual tunnel pairing.
- **Zero-Downtime Kernel Preservation:** Automatically detects existing MTunnel `GRE` / `VXLAN` tunnels, keeps `mgre.service` / `mxlan.service` running untouched, and exposes live **`Status / Restart / Stop / Logs`** controls directly inside the MControl **Tunnels** tab.
- **Pro Pairing & Accuracy:** Fixed Paqet `ROLE=1` (Foreign/Server) vs `ROLE=2` (Iran/Client) mapping, preserved Rathole `UDP_PORTS`, added `PEER_SETUP_ID` matching, and added a **Manual Pair** UI for GRE-layered or custom-IP tunnels.

### Multi-Provider Cloud DNS Manager (Cloudflare + ArvanCloud)
- **Dual-Provider Architecture:** Independent sync schedules, centered status bubbles, and full CRUD (`A`, `AAAA`, `CNAME`, `TXT`) for both **Cloudflare** and **ArvanCloud**.
- **Automatic Server-to-Domain Matching:** Matches DNS records against managed servers' IPv4/IPv6 addresses and displays domain badges on server cards.
- **Instant Parallel Bulk Actions:** Added checkbox selection + `Select All` with parallel backend endpoints (`POST /api/cloudflare/bulk-delete` and `POST /api/cloudflare/bulk-proxy`) for instant bulk Proxy ON/OFF and deletion.

### MDesign UI/UX & Theme Engine Overhaul
- **11-Axis Modular Theme Engine:** Expanded Theme Studio with `Floating Sidebar` navigation, `Prism Bubbles` card surface, `Master Cards` (`Unified` vs `Separate Bubbles`), `Metric Graph` (`Wave Sparkline`, `Circular Ring`, `Classic Line`), and `Checkboxes` (`Rounded`, `Square`, `Circle`, `Neon Glow`, `Material`, `Mini Switch`).
- **Symmetric Top Bar:** Replaced the dedicated Update tab with symmetric floating bubbles flanking the main nav: **Version / Update Bubble** (left, with Rolling Beta toggle, Changelog, and Offline ZIP upload) and **Backup / Restore Bubble** (right, instant `.zip` download & 1-click file-picker restore). Reordered main tabs to `Overview`, `Servers`, `Tunnels`, `Domains`, `Settings`.
- **Overview Master Toolbox:** Replaced the Disk card with a **Toolbox** card (`Speedtest`, `Tweak`, `Clean RAM`, `MSmart`, `Restart Panel`), moved Disk % to the master strip, renamed RX/TX to **Download / Upload**, and moved `Live Logs` to Settings.
- **Paginated Wizards:**
  - **Add Server Wizard:** Vertically centered 2-to-3 step paginated modal (`Name/IP/Route` → `SSH Credentials` → conditional `MTunnel Migrator`) with automatic GeoIP role detection.
  - **New 2-Sided Tunnel Wizard:** Taller 3-step modal with a **3-Bubble Route Selector** (Country Flag, Server Name, IP + Flag behind IP).
- **Clean Settings Layout:** 2-row 3×2 sub-tabs (with dedicated `Themes` tab) and wider uniform-width single-column cards.
- **Modernized Login Logo:** Clean single-ribbon `M` vector logo with slow opposing left-to-right and right-to-left rainbow gradient streams.

## 1.2.2
- **MTunnel → MControl Pro Migrator & Engine Hardening:**
  - Fixed **Paqet** both in native tunnel creation (`tunnels.go`) and MTunnel migration (`migrate.go`): uses real `paqet run -c <file.yaml>` configs, corrects MTunnel `ROLE=1` (Foreign/Server) vs `ROLE=2` (Iran/Client) mapping, automatically restores and cleans `iptables` `raw NOTRACK` and `mangle RST DROP` rules.
  - Closed command-injection risk in `handleCreateTunnel` by strictly validating tunnel names & tokens (`^[A-Za-z0-9._-]{1,64}$`).
  - Added **MTunnel Migrator & Kept Kernel Tunnels Hub** inside the **Tunnels** tab with live **`Status / Restart / Stop / Logs`** control on kept `mgre.service` / `mxlan.service` kernel tunnels, plus **Auto-Pair** and **Manual Pair** (supporting `PEER_SETUP_ID`, GRE-layered, and custom-IP tunnels).
  - Cleaned stray `iptables -t mangle -S` log noise in `legacyWipeScript`, preserved Rathole `UDP_PORTS`, and updated `migrate_test.go` so `go test ./...` passes cleanly.
- Added **Cloudflare Domains Manager** (`internal/api/cloudflare.go` + dedicated **Domains** tab):
  - Manage all Cloudflare Zones & DNS records (`A`, `AAAA`, `CNAME`, `TXT`, etc.) directly from MControl with Create, Edit, Delete, and 1-click Proxied (CDN) toggle.
  - **Automatic Server-to-Domain IP Matching:** Automatically matches every Cloudflare `A`/`AAAA` record against your managed servers' IPv4/IPv6 addresses, displaying matched server badges in the Domains tab and matched domain badges directly on every Server card.
  - Automatic routing through **Master Outbound (`127.0.0.1:10808`)** with direct fallback.
- Added **MControl Model 3 Neon Ribbon `M`** vector branding across the **Favicon**, **Header/Sidebar**, and **Animated Login Page** logo.
- Simplified Telegram toggle label to `Enable Telegram Bot`.
- Added **MTunnel → MControl migration** (`internal/api/migrate.go`, `internal/db/legacy.go`):
  - When a server is added, the panel scans it (read-only) for an old MTunnel installation and opens the **Migrate from MTunnel** window; the same window is available later from the new **MTunnel** button on every server card.
  - Import: every MTunnel config file (backhaul `.meta/.toml`, rathole `meta.conf/config.toml`, paqet `.meta/.yaml`, GRE/VXLAN `.conf`, MPorter, Smart) is saved **verbatim** as an AES-GCM encrypted snapshot, and each tunnel side is staged in the database. The exact snapshot can be downloaded as JSON.
  - Order is enforced: snapshot saved → static bundle installed → engines verified → only then MTunnel is removed. Any failure before the wipe leaves MTunnel untouched.
  - The wipe removes MTunnel services, units, cron lines, iptables rules, configs and launchers but never the engines (`bh`, `rathole`, `paqet`, ...) that MControl just installed.
  - When both sides of a Backhaul / Rathole / Paqet tunnel are known (matched by engine + link port + token/key), the tunnel is re-created under MControl with the **original config files** (same ports, token, transport, TLS settings) so MControl start/stop/logs/delete work on it.
  - GRE / VXLAN: all their data is saved verbatim in the snapshot, and the kernel part (`/etc/mgre`, `mgre`/`mxlan`, their services) is left running untouched so those tunnels never drop; everything else of MTunnel is removed. MPorter rules and Smart settings are saved but not re-created automatically and need an explicit confirmation before MTunnel is removed.
- Fixed: after adding a server the panel never opened the Setup window because the API did not return a top-level `id`.
- Fixed: deleting a tunnel left its `.yaml` config behind on the servers.

## 1.2.1
- Added **Native Telegram Bot & Auto-Backuper** (`internal/api/telegram.go` + dedicated **Telegram** tab):
  - Full inline-keyboard management: live Master/Server stats, Server list, Tunnel control (`Status`, `Restart`, `Stop`, `Logs`, `Delete`), interactive **New Tunnel Wizard**, 1-click **Update / Beta Install**, and **Emergency Tools** (Panel URL, Restart Panel, Reset 2FA).
  - **On-Demand & Scheduled Auto-Backuper** (every 6/12/24/48 hours) to Admin Chat or a dedicated private Backup Channel, plus **Auto-Backup Before Panel Updates** and **Send-to-Restore** (send a backup `.zip` to the bot and confirm to restore).
  - **Actionable Smart Alerts** with 15-minute anti-spam cooldown: Server Offline/Recovered, High CPU/RAM/Disk (>90%), Tunnel Down alerts with inline **Restart** & **Logs** buttons, and **Daily Digest** (Iran Time UTC+3:30).
  - **Auto-Fallback Connectivity**: automatically routes Telegram API through **Master Outbound (`127.0.0.1:10808`)** when enabled, with automatic fallback to direct connection.
- Added **Direct Rolling Beta Install** support (`--beta` / `MCONTROL_CHANNEL=beta`) to `install.sh` and documented it in `README.md`.
- Includes all `v1.2.0` beta improvements: dedicated **Outbound** tab with Subscriptions, Xray Core status/version/installer, Observatory Load-Balancing & Auto-Failover, **Real Delay** tester, and crisp Light-Mode modal surfaces.

## 1.2.0
- Switched official releases to clean 3-segment SemVer (`v1.2.0`), with intermediate updates delivered through the **Rolling Beta (`beta-latest`)** channel.
- Consolidates all recent features: MDesign 3-section tunnel builder, 30 curated themes across `Neon` / `Material` / `Studio`, Master Outbound (Xray Core), Rolling Beta Builds system, MSmart Benchmark with instant re-ranking & saved history, and Kernel GRE/VXLAN VIP & smart load balancing.

## 1.1.9.5
- Imported the new curated **3-family Theme System** (`Neon`, `Material`, `Studio`) with family-specific CSS finishes, WCAG-safe accent contrast (`lum > 0.179`), accessible `<button>` theme cards, and live login swatch updates.
- Added 6 new high-contrast curated themes (30 themes total): **Aurora Mint**, **Crimson Grid** (`Neon`), **Material Espresso**, **Material Cobalt** (`Material`), **Obsidian Mono**, and **Emerald Desk** (`Studio`).
- Preserved all `v1.1.9.4` Rolling Beta Builds and `v1.1.9.3` Master Outbound (Xray) features.

## 1.1.9.4
- Added **Rolling Beta Builds System**:
  - Updated `.github/workflows/release.yml` so pushing commits to `main` without bumping `VERSION` automatically builds and updates a rolling `beta-latest` pre-release (`beta-meta.json` + static binaries) without cluttering official releases; bumping `VERSION` publishes an official release + updates `beta-latest`.
  - Added **Enable Beta Builds (Rolling Pre-Releases)** toggle in **Settings**.
  - Added automatic beta build detection (`CheckAll`), **Rolling Beta** status row & **Install Beta Build** button in the Update Center, and `BETA` badge support on the top-left round Update button.

## 1.1.9.3
- Redesigned **+ New 2-Sided Tunnel** modal with clean **MDesign** 3-section layout (works crisply in all light & dark themes):
  - Removed mismatched OS emojis (`🌍` / `🇮🇷`) in favor of clean `IR •` / `EX •` tags.
  - Added segmented **Engine Pills** (`Backhaul`, `Rathole`, `Paqet`, `GRE`, `VXLAN`), side-by-side **Route** cards with per-server `IPv4/IPv6` selectors, and collapsible **Advanced / Token / VIP** drawer.
- Added **Master Outbound (Xray)** module (`internal/api/outbound.go` + Settings card):
  - Paste any `vless://`, `vmess://`, `trojan://`, `ss://`, or `socks://` URI to run an isolated local Xray SOCKS5 outbound strictly on `127.0.0.1:10808` (`mcontrol-outbound.service`) for the Master Panel only—completely separate from user tunnels.
  - Selective routing toggles for **GitHub & Updates**, **Telegram Bot**, and **Foreign Nodes Control**, plus 1-click **Test Outbound** latency check.

## 1.1.9.2
- Fixed `SQL logic error: table tunnels has no column named vip_targets` by properly registering `ALTER TABLE tunnels` migrations in `internal/db/db.go` so existing databases upgrade seamlessly and tunnel listing/creation work immediately.
- Enabled **Saved SSH Profiles** inside the **Edit Server** modal (both frontend & backend `handleUpdateServer`).
- Redesigned **IP Selection** in `+ New Tunnel`:
  - Removed GRE IP modes from non-kernel tunnels (`Backhaul`, `Rathole`, `Paqet`).
  - Added independent **Iran IP (`IPv4`/`IPv6`)** and **Foreign IP (`IPv4`/`IPv6`)** selectors directly under each server, allowing mixed combinations (e.g. Iran IPv6 ⇄ Foreign IPv4).
- Added a custom **MDesign Visual 8-Ping Tester** (`POST /api/tunnels/ping8`) directly inside the tunnel creation modal—no terminal box—showing 8 live animated ping bars/pills, **Average Ping (ms)**, and **Packet Loss (%)**.

## 1.1.9.1
- Switched release versioning to 4-segment format (`v1.1.9.1`).
- Added instant client-side re-scoring and re-ranking in **MSmart Benchmark**: switching between **Balanced**, **Max Speed**, and **Low Ping** immediately reorders and updates scores for both current and saved benchmark results without needing to re-run the test.

## 1.1.9
- Replaced `[iran]` / `[foreign]` text tags in server selectors with clean icons (`🇮🇷` / `🌍`).
- Expanded **Kernel Tunnel Load Balancing** (`GRE` / `IP6GRE` / `VXLAN`) with full smart modes: **Round-Robin**, **Random**, **Lowest Ping (RTT)**, **Lightest Load (Least Connections)**, and **Least Traffic (Lowest Bandwidth)** with real Linux `iptables` / `conntrack` / `ping` target selection.
- Fixed **Check Protocols** navigation inside `+ New Tunnel`: clicking `⚡ Check Protocols` now cleanly transitions directly into the Protocol Checker modal and returns to your pre-filled tunnel form once a protocol is selected.

## 1.1.8
- Upgraded automatic background update checker to run immediately on load, every **60 seconds** (down from 5 minutes), and automatically whenever you switch back to the panel browser tab (`visibilitychange`).
- Shows an automatic toast alert and lights up the top-left round Update button the moment a new release is detected—no need to open the Update tab manually.

## 1.1.7
- Restored the **Top-Left Round Update Indicator Button (`#top-upd-btn`)** on the main screen with glowing animation and version badge whenever a new release is available.
- Added a **Floating Round `+` Button (FAB)** at the bottom corner of the `Tunnels` tab for instant 1-tap tunnel creation on desktop and mobile.
- Integrated **Check Protocols** directly inside the `+ New Tunnel` modal: once two distinct servers (Iran & Foreign) are selected, a `⚡ Check Protocols` button appears to benchmark all protocols between those two nodes and select the winning protocol straight back into the tunnel form without losing entered fields.

## 1.1.6
- Automatically opens **Server Setup Studio** right after adding a new server so you immediately see and install prerequisites/engines without hunting through menus.
- Added **Custom VIP / Subnet & Multi-VIP Load Balancing** for Kernel Tunnels (`GRE`, `IP6GRE`, `VXLAN`):
  - Supports custom `/30` tunnel subnet (or auto-assigned `10.10.x.0/30`).
  - Supports multiple VIP targets with kernel-level `iptables` load balancing (`Round-Robin / nth` and `Random` modes).

## 1.1.5
- Fixed GRE / IP6GRE `File exists` conflict when benchmarking or deploying multiple tunnels between the same two servers by assigning a unique GRE `key <id>` to every tunnel.
- Improved kernel tunnel error reporting to return the exact kernel/script error instead of a generic systemd failure message.
- Added **Persistent Benchmark History & Saved Logs** (SQLite `benchmark_runs` table): every MSmart benchmark run automatically saves its full logs and ranked results; users can view past runs or download logs anytime.
- Added **Seamless Background Resume**: closing the MSmart modal, switching tabs, or refreshing the browser automatically resumes live benchmark progress and shows a live status badge on the MSmart button.

## 1.1.4
- Added **1-Click In-Tunnel Speedtest** (`Speedtest` button on every deployed tunnel card, `POST /api/tunnels/{id}/speedtest`) measuring real RTT & throughput directly through the active tunnel.
- Redesigned **MSmart Benchmark** protocol selector into clean, well-spaced categories: **Backhaul** (with IPv4/IPv6 option), **Rathole** (with IPv4/IPv6 option), **GRE** (`4-to-4`, `6-to-4`, `6-to-6`), **VXLAN** (`IPv4` / `IPv6`), and **Paqet** (`Raw Packet`).
- Added automatic **IPv6 activation & probe** (`net.ipv6.conf.all.disable_ipv6=0` + `ping -6`) when testing or deploying IPv6 tunnels, even if IPv6 was disabled on one of the nodes.
- Upgraded **New Tunnel** modal with categorized Engine/IP-mode selection (`GRE 4-to-4`, `GRE 6-to-4`, `GRE 6-to-6`, plus IPv6 toggle for Backhaul/Rathole/VXLAN).

## 1.1.3
- Fixed MSmart Benchmark `0 Mbps` failure when `iperf3` is missing: replaced external `iperf3` dependency with a built-in zero-dependency Python3 multi-stream TCP throughput & RTT benchmarker (works out-of-the-box on every server without needing `iperf3`).
- Fixed `Backhaul KCP` / `Rathole` false-negative deploy errors caused by `systemctl is-active` returning `activating` (exit code 3) under `set -e`.
- Fixed `iperf3` 1-click install in Setup Studio to verify the binary actually runs and cleanly fall back to `apt-get` / `dnf` if missing from the bundle.

## 1.1.2
- Fixed `iperf3` missing from the static bundle by compiling a guaranteed static `iperf3` binary directly inside `.github/workflows/bundle.yml` (plus smart fallback in `setup.go`).
- Made every status pill inside Server Setup Studio (`iperf3`, `Speedtest`, `jq`, `Backhaul`, `Rathole`, `Paqet`, `Realm`, `Kernel GRE`, `Peer Agent`) directly clickable to install/reinstall that specific component.
- Hardened Master Panel live hardware telemetry (`pollLocalStats`) with null-safe DOM updates, error isolation, and immediate refresh on tab switch.
- Added **MSmart Lab (Real In-Tunnel Benchmark & Auto-Ranker)** in the `Tunnels` tab: sequentially deploys isolated temporary tunnels between Iran & Foreign nodes, measures real in-tunnel `iperf3` throughput and latency, cleans up automatically, ranks candidates by `Balanced` / `Max Speed` / `Low Ping` profiles, and provides 1-click **Deploy Winner**.

## 1.1.1
- Fixed blank/white page crash caused by legacy `#btn-core-*` / `#core-cmd` / `#core-srv` DOM listeners after replacing the Phase 2 Tunnels tab.

## 1.1.0 (Phase 2 — Unified Tunnels & Zero-APT Static Bundle)
- Added automated static bundle workflow (`.github/workflows/bundle.yml`) building `mcontrol-bundle-linux-amd64.tar.gz` + `.sha256` with `iperf3`, `speedtest`, `jq`, `socat`, `bh` (Backhaul), `rathole`, `paqet`, and `realm` (dropped `mporter`, `haproxy`, `gost`; zero `apt` and zero `unzip` required on Ubuntu 22/24/26).
- Upgraded Setup Studio to deploy directly from the static `.tar.gz` bundle via GitHub, Iran Mirror, or Direct from Master Cache.
- Implemented Phase 2 Unified Tunnel Management (`internal/api/tunnels.go`, SQLite `tunnels` table, and redesigned `Tunnels` tab):
  - Atomic 2-sided deployment (Iran ⇄ Foreign) over Peer Agent v2 (TLS) or SSH.
  - Supports **Backhaul** (`tcp`, `tcpmux`, `tcp+tls`, `ws`, `wss`, `kcp`, `udp`), **Rathole**, **Paqet**, and Kernel Tunnels (**GRE**, **IP6GRE**, **VXLAN**) with automatic `/30` subnetting and persistent `iptables` port forwarding.
  - Direct integration with Saved Port Profiles + live `Start`, `Stop`, `Restart`, `Status`, `Logs`, and clean 2-sided `Delete`.
- Added **Link Test (Ping + iperf3)** tool to benchmark real latency and throughput between any two nodes directly from the Tunnels tab.

## 1.0.3
- Auto-migrated legacy `raw.githubusercontent.com` panel update URLs to GitHub Releases (`releases/latest/download`) in both SQLite and `updater.go` so 1-click GitHub updates never hit HTTP 404.
- Added automatic background update checking (runs on login and every 5 minutes automatically).
- Replaced the small `Update available!` button on the Control Server card with a prominent glowing top-left `Update available!` pill button in the empty top-left space.

## 1.0.2
- Fixed mobile utility bar overlap bug (removed `backdrop-filter` containing-block trap from bottom `#chrome` bar that forced the language/theme/logout buttons on top of navigation tabs).
- Added a clean native-style fixed Mobile Top Header Bar (`<= 820px`, LTR & RTL aware) keeping the MControl logo on one side and utility buttons (Theme, Dark/Light, Language, Logout, Collapse) neatly on the opposite side with zero overlap against page buttons or bottom tabs.
- Polished mobile responsiveness: prevented horizontal overflow on long IPv6/URL strings, improved toolbar wrapping, tap target sizes, and safe-area spacing for notched phones.

## 1.0.1
- Standalone MControl release completely decoupled from MTunnel (migration tool from MTunnel planned after Tunnel Phase).
- Automated GitHub Actions release pipeline (`.github/workflows/release.yml`) building static Linux `amd64` / `arm64` binaries, `MControl-latest.zip`, and `.sha256` checksums on every release.
- 1-liner online installer (`install.sh`) pulling verified pre-built binaries directly from GitHub Releases (no Go compiler required on servers).
- MDesign UI with 45 structural themes, animated Login screen with built-in Theme/FX picker, compact mobile responsive layout, and vertical telemetry dock.
- Peer Agent v2 (HTTPS/TLS + authenticated `/exec` + live telemetry + local Speedtest), Server Setup Studio (GitHub / Mirror / Direct from Master cache), Server Tweaks, Unlimited SSH & Port Profiles, Listen Domain + Auto Let's Encrypt SSL, RFC-6238 2FA, and Full Backup/Restore (DB + Key + SSL).
