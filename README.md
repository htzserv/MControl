# MControl — Central Server & Tunnel Management Panel (MDesign)

**MControl** is a standalone, single-binary Go management panel for multi-node infrastructure (Iran & Foreign servers), engineered with the **MDesign** theme engine (45 structural themes) and **Peer Agent v2** (TLS-encrypted agent management without permanent SSH passwords).

## Quick Install (1-Liner)

```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash
```

Custom port (default `2053`):
```bash
curl -fsSL https://raw.githubusercontent.com/htzserv/MControl/main/install.sh | sudo bash -s -- 2053
```

## Key Features
- **Single Static Binary + Embedded UI + SQLite:** Zero runtime dependencies on the Master server.
- **Peer Agent v2 (TLS):** Pair nodes via 1-click Peer Code, wipe SSH passwords from DB, and run live telemetry, speedtests, tweaks, setup, and tunnel orchestration over TLS.
- **Setup Studio & Master Cache:** Install OS prerequisites and tunnel engines (`Backhaul`, `Rathole`, `Paqet`, `GOST`, `HAProxy`) from **GitHub**, **Iran Mirror**, or **Direct from Master**.
- **Security Hardened:** Expiring HMAC tokens, server-side logout, brute-force throttling, RFC-6238 2FA, Listen Domain + Strict Host Mode, Auto Let's Encrypt SSL, and Full Encrypted ZIP Backups (DB + Key + SSL).
- **MDesign Theme Studio:** 45 structural themes + 8-axis live studio + animated Login screen with theme switcher.
- **CLI Manager:** Run `mcontrol` on the Master server for service control, port/web-path updates, emergency 2FA/HTTPS reset, and logs.

## Automatic Releases (GitHub Actions)
Push code with an updated `VERSION` file to `main` (or trigger the workflow manually in GitHub Actions). GitHub automatically compiles `mcontrol-linux-amd64` / `mcontrol-linux-arm64`, packages `MControl-latest.zip` + `.sha256` checksums, and publishes a GitHub Release so all panels can update with 1 click from **Update ➔ Update from GitHub**.
