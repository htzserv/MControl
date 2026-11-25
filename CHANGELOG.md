# Changelog

## 1.6.1
- **Phase 3 — Backhaul v1.0.0 Next-Gen Sync (Parallax, Multi-Transport Failover & Game Mode):**
  - **Parallax Multi-Lane Mode (`parallax` / `parallaxmux`):** added wizard UI & TOML config generation for KCP+FEC over 2–12 parallel TCP lanes with randomized encrypted warm-up and automatic silent-stall redialing.
  - **Multi-Transport Failover (`failover`):** run 2–4 Backhaul carriers (e.g., `tlsmux,xhttpsmux,parallaxmux`) behind the same user ports with automatic failover & configurable primary hysteresis restoration.
  - **Game Mode Multi-Copy UDP (`game_mode`):** one-click toggle to duplicate game UDP packets across 2–4 encrypted UDP carrier ports with first-arrival-wins deduplication.
  - **MSmart Benchmark:** added `Backhaul ParallaxMUX` to default benchmark candidates and refreshed EN/FA READMEs for `v1.6.1`.


## 1.6.0
- **Phase 2 — Anti-DPI Agent Veil, Advanced Kernel Engines, Smart Guard Firewall & MSmart v2:**
  - **Peer Agent v4.1 Veil Layer (`internal/sshclient/veil.go` & `cmd/mcontrol-agent`):** wraps Master ↔ Peer TLS control traffic in an AES-256-CTR directional keystream with random nonce & padding (`nonce[0] != 0x16`), hiding bare-IP ClientHello packets from DPI while keeping 100% backward compatibility with legacy plain-TLS peers.
  - **Advanced Kernel Engines & Networking (`internal/api/tunnels.go` & `pro.go`):**
    - Added **IPIP** and **WireGuard** engines alongside GRE/IP6GRE/VXLAN.
    - Added **FOU (GRE/IPIP over UDP)** encapsulation to bypass raw protocol 47/4 filtering.
    - Added deterministic **Kernel IPsec (`ip xfrm`)** encryption (`AES-GCM-128`, `AES-GCM-256`, `ChaCha20-Poly1305`) with zero external daemon overhead.
    - Added **Transparent Forwarding (`fwmark` + policy routing)** to preserve real client IPs without altering destination services.
    - Added **Auto Path-MTU Discovery (`POST /api/tunnels/{id}/pmtu`)**, **Kernel Smart Queue Shaping (`tc` CAKE / TBF + `fq_codel`)**, and expanded **MikroTik RouterOS v7** export for GRE, IP6GRE, IPIP, VXLAN, and WireGuard.
  - **Smart Guard Firewall (`internal/api/guard.go`):** atomic `nftables` firewall per server with automatic listening-port scanner, per-port **Open / Iran-Only (`geoip:ir`) / Closed** policy, automatic SSH/Panel/Peer protection, and **90-second Safe Mode auto-rollback**.
  - **MSmart v2 & 2-Node Free Port Finder (`internal/api/msmart.go` & `/api/tunnels/free-port`):** added Backhaul v0.9.3 transports (`TLS`, `TLSMUX`, `XHTTPS`, `KCPMUX`) to default benchmarks, excluded the initial 1.5s TCP slow-start warm-up window from throughput results, and added 1-click free-port discovery in the Tunnel Wizard.


## 1.5.3
- **Phase 1 — Backhaul v0.9.3 Sync, Security Hardening & Reliability:**
  - **Custom Backhaul v0.9.3 Fork Integration:** updated `.github/workflows/bundle.yml` to bundle `bh-linux-amd64` from `htzserv/Backhaul` (with upstream fallback), added all **15 Backhaul v0.9.3 transports** (`tcp`, `tcpmux`, `tls`, `tlsmux`, `ws`, `wss`, `wsmux`, `wssmux`, `udp`, `kcp`, `kcpmux`, `xhttp`, `xhttps`, `xhttpmux`, `xhttpsmux`) to the Tunnel Wizard, and added **5 performance presets** (`Balanced`, `Many Users`, `Gaming`, `Streaming`, `Max Capacity`) plus Anti-DPI options (`SNI`, `uTLS Fingerprint`, `Edge IP`, `TLS Record Fragmentation`, `XHTTP Path/Host/Mode/Fallback`, `KCP Mode/Crypt/NoFEC`).
  - **Parallel & Real Health-Checked Tunnel Watchdog:** upgraded `StartTunnelWatchdog()` to check tunnels concurrently (up to 6 parallel workers) and verify actual control port listening (`ss`) or kernel interface existence (`ip link`), not just `systemctl is-active`.
  - **Peer Agent v4 Security & Speed:** added `subtle.ConstantTimeCompare` + 30s in-memory peer-code cache to `ServeAgentHeartbeat` ($O(1)$ lookup) and optional Master TLS certificate fingerprint pinning (`MC_MASTER_CERT_FP`) in `cmd/mcontrol-agent`.
  - **Bulletproof Remote File Manager:** replaced `ls | awk` parsing with `find -printf` (safe for spaces/symlinks) and upgraded remote file upload to 36KB chunked base64 writes (eliminating `ARG_MAX` & agent 64KB exec body limits on multi-MB uploads).


## 1.5.2
- **Official Stable Release `v1.5.2`:**
  - **Local Master & Remote Full File Manager + Right-Click Context Menu:** added binary-safe file download, multi-file upload (up to 20MB), new file/folder creation, rename, copy/duplicate, recursive delete with system-path protection, and a full right-click context menu for both Web Terminal and File Manager across Master and remote servers.
  - **Advanced DB-Synced Per-Tab Control Center (`GET/POST /api/ui/tab-prefs`):** configure all 11 tabs (active or hidden) from a unified modal — including auto-ping/telemetry intervals, server sort/filter, compact rows, vertical card alignment (Top/Center/Bottom), and toolbar gear visibility.
  - **Paqet Auto-Bundled & Fixed Verification:** auto-downloads `paqet` in `bundle.yml` (YAML indentation fixed) and supports `paqet version` verification in `setup.go`.
  - **ParsPack Mirror AMD64 Optimization & Live Auto-Sync:** pruned legacy `arm64` artifacts from ParsPack S3 mirror for faster sync, upgraded server live auto-sync (quiet 4s poll + 8s probe), and replaced password eye emojis with minimal SVG icons.
  - **Updated Documentation:** refreshed both `README.md` (English) and `README.fa.md` (Persian) for `v1.5.2`.


## 1.5.1
- **Stable Release `v1.5.1`:**
  - **Reliable GitHub Releases (`gh` CLI):** replaced `softprops/action-gh-release@v2` (which failed with parallel asset delete/upload errors under Node 24) with sequential `gh release upload --clobber` + 3x retry, added `mcontrol-agent-linux-amd64/arm64` (+ `.sha256`) to GitHub Releases, and hardened ParsPack S3 sync (`max_concurrent_requests 1` + per-file fallback).
  - **Fixed `openSrvModal` Crash:** removed unguarded `$('s-cred-mode-wrap')` DOM reference in Add/Edit Server modal and wired newly created SSH profiles to auto-fill Step 2 immediately.
  - **Full Peer Agent v4 Rollout:** upgraded `Update Peer Agent`, `Rotate Key`, and `Canary Rollout` to deploy `PeerAgentInstallScriptV4` with `MC_MASTER_URL` reverse heartbeat.
  - **Theme Engine, Floating Bar & Xray Hub Polish:** decoupled Design vs. Color selection (+ 12 structural theme designs), 0.5s long-press / right-click inline top-bar tab editor (DB-synced across devices), horizontally centered page toolbars, unified Xray Core UI with separate ON/OFF Subscriptions & Load Balance toggle cards, Reverse Ping (`↩️`), Multi-Iran Reachability Check, Datacenter Auto-Pair, and Master Local Terminal with quick snippets.


## 1.5.0
- **Customizable Top Navigation Tabs + Settings All-Sections Hub + Local Master Terminal:** added an *Edit Top Navigation Tabs* studio inside Theme Studio (drag-and-drop or ▲/▼ reorder, toggle any section like Themes, Outbound, Telegram Bot, Profiles, Master Terminal, or Update Center on/off), added an *All Sections & Tabs* bubble hub inside Settings so every feature stays 1 click away even when hidden from the top bar, and added a Local Master Server Terminal button (`POST /api/local/exec`) inside the top-right collapsible utility bar.
- **Responsive Server Rows, Reverse Ping (`↩️`) & Upgraded Add-Server Wizard:** fixed desktop/tablet server header overlap with a clean flex layout, added Reverse Ping (`Server → Iran Master`), added an automatic IPv6 slide-down drawer in Add Server Step 1 (`GET /api/servers/resolve-ipv6`), and streamlined Step 2 into 3 clean rows (Port + Auth, Username with automatic Saved Profiles dropdown, Password with Show/Hide eye toggle).
- **Official Stable Release `v1.5.0`:**
  - **Peer Agent v4 Ultimate:** zero-dependency static Go binary (`cmd/mcontrol-agent`) with Python v4 fallback, Master-IP key sync (`MASTER_IP|PEER_CODE`), 5 fixed ports (`84`, `847`, `7483`, `24819`, `2054`), auto-discovery, TLS certificate pinning, auto-outbound route detection, and reverse heartbeat push (`/_mc_agent/heartbeat`).
  - **Datacenter Cloud Hub (`Datacenter` tab):** unified encrypted API integration for **Hetzner Cloud, DigitalOcean, Linode (Akamai), OVHcloud & YottaSrc** with OS Rebuild, Create/Delete/Reboot/Power controls, 1-click Peer v4 Cloud Firewall, and 1-click Import.
  - **Master Outbound (Xray Core) Rebuild:** dedicated Single Active Config mode alongside Load Balance, fixed VMess/VLESS Reality/Trojan URI parser, `xray run -test` pre-flight validation, live logs, and Xray Core bundled in Iran Mirror & prereqs bundle.
  - **Server Controls & Dual Ping UX:** centered 5-button dropdown bar (`Terminal`, `System Controls`, `MControl & Peer`, `Network & Stats`, `Manage`), single-line `PEER V4` badge, quick `📡 Ping` header bubble, `↻ Ping All`, dynamic latency-colored dual ping (`🇮🇷` Direct + `🚀` Outbound), and Advanced 4-mode Delete modal.
  - **MDesign PWA Pro:** crisp PNG/maskable icons, animated splash screen, install banner, iOS guide, pull-to-refresh, and branded offline mode.


## 1.0.2
- **Stable Release `v1.0.2`:**
  - **Dynamic Peer Version Badge (`PEER V4`):** replaced the hardcoded `PEER V2` badge with dynamic version detection (`PEER V4` / `PEER V3` / `PEER V2` from `core_version` with agent tooltip).
  - **Quick Terminal Bubble Button (`🖥️ Term`) in Server Header:** added a one-click pill bubble button right next to `PEER V4` / `OUTBOUND` so you can launch Terminal without expanding the server card.
  - **Ping All (`↻ Ping All`):** added `POST /api/servers/ping-all` and a toolbar button to refresh direct Iran + Outbound pings across all servers in parallel.
  - **Single-Line Header Ping Layout:** prevented the manual ping `↻` button from wrapping onto a second line below the server IP.


## 1.0.1
- **Official Public Release (`v1.0.1`):** consolidated Peer Agent v4 (Go static binary + Python fallback, Master-IP key sync, 5 fixed ports, TLS cert pinning, reverse heartbeat), Master Outbound Xray Core (Single Active Config + Load Balance + Iran Mirror bundle), Datacenter Cloud Hub (Hetzner, DigitalOcean, Linode, OVHcloud, YottaSrc + OS Rebuild & Peer FW), MDesign PWA, and Advanced 4-mode Server Delete.
- **Dual Ping (Direct Iran `🇮🇷` + Outbound `🚀`):** displays direct ICMP/TCP latency from Master alongside true end-to-end RTT via Master Outbound (Xray SOCKS5) with dynamic latency colors and 1-click manual Ping (`↻`).
- **Centered 5-Button Server Action Bar:** centered controls (`Terminal`, `System Controls`, `MControl & Peer`, `Network & Stats`, `Manage`), moved live `IPv6`, `BBR`, and `Firewall` status toggles into the `Network & Stats` dropdown, and enriched `MControl & Peer` (`Update Peer Agent`, `Unpair`) and `Manage` (`Copy IPv4`).


## 1.4.6
- **5-Button Server Dropdown Bar + Dynamic Ping Colors + True Iran Direct Ping:** condensed server controls into 5 clean buttons (*Terminal* standalone + *System Controls*, *MControl & Peer*, *Network & Stats*, *Manage* floating dropdowns), added latency-based Ping bubble colors (green <100ms, amber 100–220ms, red >220ms), and fixed `MeasurePingSmart` to always measure true direct ICMP/TCP latency from Master instead of local Xray SOCKS5 loopback handshake (~0.5ms).
- **Categorized Server Controls + Smart Outbound Ping & Manual Ping Button:** grouped server card actions into 4 clear categories (*Server*, *MControl & Peer*, *Network & Stats*, *Manage*), added a 1-click manual Ping button (`↻`) next to every Ping badge (`POST /api/servers/{id}/ping`), and upgraded auto-ping (`MeasurePingSmart`) to measure RTT over Master Outbound SOCKS5 / Peer TLS so Outbound nodes never show `N/A`.
- **Datacenter Cloud Hub Tab (`دیتاسنتر` / `Datacenter`):** unified multi-cloud management for **Hetzner Cloud, DigitalOcean, Linode (Akamai), OVHcloud & YottaSrc** with encrypted API keys, **OS Rebuild**, Create/Delete/Reboot/Power controls, 1-click Cloud Firewall for Peer v4 ports, Master Outbound routing option, and 1-click Import to MControl Servers.
- **Advanced Server Delete Menu:** clicking Delete now opens a 4-mode modal: *Unpair & Keep Tunnels* (removes master key & panel entry while tunnels stay running), *Unpair Only* (switches back to SSH mode), *Remove from Panel Only* (DB-only for offline nodes), and *Full Uninstall* (wipes Peer Agent, all `mcontrol-tun-*` tunnels, interfaces & iptables rules).
- **Xray Core in Prereqs Bundle & Iran Mirror:** `bundle.yml` now packages `xray` inside `mcontrol-bundle-linux-amd64.tar.gz` and publishes standalone `xray-linux-amd64` / `xray-linux-arm64` (+ `.sha256`) to both GitHub `bundle-latest` and ParsPack S3 Mirror. Panel `Install Xray Core` installs offline from cache or directly from Iran Mirror first.
- **Peer Agent v4 Ultimate (6 Upgrades + 5 Fixed Ports):**
  - Fixed 5-port order (preserving saved port & auto-discovered running agent first): `84` (2-digit) → `847` (3-digit) → `7483` (4-digit) → `24819` (5-digit) → `2054` (always last).
  - Auto-Discovery of existing `mcontrol-agent-*.service` ports before creating new ones.
  - Standalone zero-dependency Go static agent (`cmd/mcontrol-agent`) with instant Python v4 fallback.
  - Non-blocking background 2s stats cache (sub-ms `/stats`).
  - TLS Certificate Pinning (SHA-256 fingerprint verified on every request).
  - Auto-Route Detection (automatically switches to Master Outbound Xray if direct path is blocked) + Reverse Heartbeat Push (`/_mc_agent/heartbeat`).
- **Random Peer Ports (No 2054):** pairing preserves any existing `AgentPort`, then generates 3 crypto-random ports in `20000–32000` (outside Linux's ephemeral range `32768–60999`) so common port `2054` collisions are eliminated.
- **Peer Agent v3 (Master-IP Key Sync):** each paired panel registers `master_ip|peer_code` in `/etc/mcontrol-agent/peers-<port>.txt`. Multiple Master servers share one agent simultaneously, and if a Master is wiped and reinstalled on the same IP, pairing automatically syncs and reuses its previous key (`SYNCED_KEY`).
- **ParsPack S3 Mirror Sync Fix:** restored clean S3 sync with 3x retry in `.github/workflows/release.yml` (removed `aws configure set` / `continue-on-error` masking issues).
- **Multi-panel Peer Agent (agent v3):** one server can now be paired with several MControl panels (even different versions) at the same time. The agent accepts the unit's `MC_PEER` plus every code in `/etc/mcontrol-agent/peers-<port>.txt`. Pairing from a second panel *adds* its code and keeps the first panel's code (read from the existing unit / legacy `mcontrol-agent.service`) instead of overwriting it.
- **Key rotation only drops this panel's own old code** (`PeerAgentInstallScriptFull`), other panels keep working.
- **Pair no longer aborts on the first blocked port:** if the agent is listening but unreachable (cloud firewall / route), the next candidate port (18443, 29443) is tried, extra services created for failed ports are removed, and the final error explains which ports to open.
- **Iran mirror agent guard:** a mirror copy of `mcontrol-agent.py` is only used if it supports multi-peer (`PEERS_FILE`), so an old mirror file can't downgrade the agent.
- **Country badge colour:** the badge next to each server name now uses one flat colour taken from that country's flag (`CC_COLORS` / `countryColor()` in `web/index.html`); unknown countries keep the old Iran/Foreign colours.

## 1.4.5
- **Direct Beta & Release Updates from Iran Mirror (ParsPack):**
  - Added dedicated **`beta_mirror`** update channel in `internal/updater/updater.go`, `internal/api/api.go`, and the Panel Update Center (`#update-modal`) so both **Official Releases** and **Rolling Beta Action Builds** can be checked and installed directly from the Iran Mirror (`MIRROR_URL/beta-latest/`) without touching GitHub.
- **Consolidated v1.4.5 Release:** Includes 4-page Tunnel Wizard with animated IPv4/IPv6 ping box, Agent-probed Uptime Monitoring, Atelier Pro Theme Engine (12 MDesign presets, live preview, Eco mode, share code, DB & `/status` sync), Per-Tenant White-Label Branding, and automatic ParsPack S3 Mirror sync in GitHub Actions.

## 1.4.4
- **CLI Show Settings (Excluding Credentials):** Added `Show Settings (Excluding User/Pass)` to the MDesign terminal menu (`main.sh` & `mcontrol-cli.sh`) displaying panel port, web path, domain/HTTPS status, channel, and SQLite settings without exposing passwords or credentials.

## 1.4.3
- **Optional Domain & 1-Click Let's Encrypt SSL during First-Time Setup:** Added optional Panel Domain / Address and automatic SSL issuance toggle directly on the First-Time Setup screen (`POST /api/setup/complete`).
- **Full Deep Wipe Option in Installer (`--wipe`):** Added `sudo bash install.sh --wipe` to completely remove old v1/v2 binaries, services, databases, and caches before a clean install.

## 1.4.2
- **Build Fix:** Fixed method receiver syntax typo (`func (h *Handler) benchLog`) in `internal/api/msmart.go`.

## 1.4.1
- **Release & Clean Repository Structure:** Bumped version to `v1.4.1` for a clean release tag (`v1.4.1` + rolling `beta-latest`) without legacy `ci-release.yml` / `internal/httpapi` artifacts.

## 1.4.0
- **Commercial Multi-Tenant SaaS Engine (Database-per-Tenant SQLite):**
  - Added `internal/saas` control plane (`saas_master.db`) with completely isolated per-tenant SQLite databases (`tenants/u_<id>/mcontrol.db`) and independent encryption keys.
  - Added **SuperAdmin Control Center** directly inside the MDesign UI: create/edit/suspend/delete commercial tenants, set quotas (`max_servers`, `max_tunnels`, `max_monitors`) & subscription expiry dates, and one-click **Impersonate** (with top banner & instant return to SuperAdmin).
  - Isolated per-tenant runtime engines (`tgRT`, `benchJob`, `monSSLAlerted`, `monLatAlerted`) and graceful daemon lifecycle (`Stop()`), plus per-tenant public status pages (`/status/<username>`).
  - Preserved 100% of v1.3.2 features (SSH + Peer Agent v2, Backhaul/Rathole/Paqet/GRE/IP6GRE/VXLAN, MTunnel Migrator, MSmart Benchmark, Xray Outbound, Uptime Kuma Monitor, Telegram Bot, Cloudflare/ArvanCloud DNS, Pro Topology/Matrix/Script Vault/SSL Sync, Master Cache `/_mc_dist/`, and Rolling Beta `beta-latest` + `bundle-latest`).
- **Drag & Drop Card Ordering:** Added persistent per-user Drag & Drop reordering for Server cards and Tunnel cards (`GET/PUT /api/saas/order`).
- **MTunnel-Style MDesign Terminal Menu & Installer:** Replaced `install.sh` and `mcontrol-cli.sh` / `main.sh` with MTunnel's gold double-line static IP-only header (no live refresh loop, no tunnel list) and tree menu with Stable / Rolling Beta (`beta-latest`) switching and all emergency CLI controls.

## 1.3.2
- **Fixed Rolling Beta Updater & Offline ZIP Upload:**
  - Added `channel` (`release` vs `beta`) metadata to `beta-meta.json` in GitHub Actions so Stable installs no longer show false beta alerts for the same release, while any subsequent Rolling Beta build (`channel: "beta"`) is detected immediately even from a Stable install.
  - Restored the dedicated **`Install Rolling Beta`** button inside the Update Center modal (`#update-modal`) and fixed the **Offline ZIP** file-picker click handler.
- **MDesign UI Polish:** Larger responsive Web Terminal & SFTP Split-View editor (with Expand button), centered Server Card tweak/action bubbles, 6 MDesign Uptime themes (12th studio axis + quick theme bar), and 6 selectable themes on the Public `/status` page.

## 1.3.1
- **Critical Hotfix (Blank White Screen):** Fixed unescaped newline literals inside `.split('\n')[0]` in `web/index.html` that broke JavaScript parsing on page load, and moved `boot()` to the end of `<script>` to prevent initialization ordering issues.
- **Multi-Line Web Terminal & Fleet Script Runner:** Upgraded Web Terminal, Bulk Fleet Runner, and Script Vault inputs to multi-line `<textarea>` editors (`Ctrl+Enter` to execute, natural `Enter` for new lines, safe paste of multi-line bash scripts), normalized CRLF (`\r\n` → `\n`) on backend execution, and raised command limit to 32 KB (60s timeout).

## 1.3.0

### Native Uptime Monitor (Uptime Kuma-Style) & Dedicated Telegram Bot
- **Multi-Point Monitoring Engine (`internal/db/monitor.go`, `internal/api/monitor.go`):** Track `HTTP/HTTPS` (with live SSL certificate expiry countdown), `TCP Port`, `ICMP Ping (v4/v6)`, and `DNS` targets from **Master**, **Master Outbound (Xray)**, or any managed **Iran/Foreign server** to accurately distinguish ISP filtering from true server outages.
- **Kuma-Style Heartbeat UI & Incidents Timeline:** New **Uptime** tab featuring 30-tick hoverable heartbeat bars, 24h / 7d / 30d uptime percentages, 1-click **Import Servers / Tunnels**, and **Auto-Heal** (automatically restarts a linked tunnel when its monitor goes down).
- **Dedicated Notification Telegram Bot:** Completely independent Bot Token, Chat/Channel ID, and Forum Topic ID with anti-spam retries, exact downtime duration on recovery, SSL expiry alerts, and latency spike warnings.
- **Public Status Page (`/status`):** Clean, login-free MDesign status page exposing only public-flagged monitors under safe display names (hiding IPs and sensitive infrastructure details).

### Disaster Recovery, Network Intelligence & Fleet Pro Suite
- **Replace Server (Disaster Recovery Wizard):** Reassign and redeploy all linked tunnels plus update all matching Cloudflare/ArvanCloud `A`/`AAAA` DNS records from a failed/filtered server to a replacement server in one click.
- **Live Network Topology Map & IPv4/IPv6 Reachability Matrix:** Interactive SVG topology view of all Entry ⇄ Exit tunnels plus a cross-node IPv4/IPv6 reachability matrix scanner.
- **Web File Manager & Script Vault:** Built-in remote file browser/editor inside the Web Terminal modal (`Shell` ⇄ `Files`) plus a reusable **Script Vault**.
- **Bandwidth Quota Guard & Server Tags:** Track monthly GB quotas (80% & 95% Telegram alerts) and filter servers by custom `#tags`.
- **Smart DNS Auto-Failover UI:** Configure `Primary ➔ Backup` failover rules directly inside the **Domains** tab with manual `Test Backup` / `Restore Primary` switches.
- **3x-ui / Marzban Active Port Sync, Auto SSL Cert Sync & MikroTik RouterOS Generator:** Fetch active inbound ports directly into tunnel configs, push SSL certificates across nodes, and generate RouterOS v7 GRE/IP6GRE scripts.
- **24h / 7d SQLite Metrics History & Inline Server Tweak Bubbles:** 5-minute persistent CPU/RAM/Ping history charts on Overview, inline ON/OFF tweak bubbles directly on server cards, and one-time saved Web Terminal authentication.

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
