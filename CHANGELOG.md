# Changelog

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
