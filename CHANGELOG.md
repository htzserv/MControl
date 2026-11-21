# Changelog

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
