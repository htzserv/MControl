# MControl v1.6.8 — Installation & CLI Guide

Central control panel for managing servers, tunnels, uptime monitors, DNS domains, and the Android app (**MDesign**).

## Quick Install Commands

### Interactive Install (GitHub / Foreign)
```bash
bash <(curl -fsSL https://github.com/htzserv/MControl/releases/latest/download/install.sh)
```

### Interactive Install (Iran Mirror / ParsPack)
```bash
bash <(curl -fsSL https://c107328.parspack.net/c107328/MControl/install.sh) --mirror
```

### Beta Channel Install (`beta-latest`)
```bash
# From GitHub
bash <(curl -fsSL https://github.com/htzserv/MControl/releases/download/beta-latest/install.sh) --beta

# From Iran Mirror
bash <(curl -fsSL https://c107328.parspack.net/c107328/MControl/beta-latest/install.sh) --mirror --beta
```

## Installer CLI Flags (`install.sh`)

- `--mirror` / `iran`: Download from Iran Mirror (ParsPack).
- `--direct` / `foreign` / `github`: Download directly from GitHub Releases.
- `--stable` / `stable`: Install Stable release (panel only).
- `--full` / `full-stable`: Install Stable release + Static Engines Bundle.
- `--beta` / `beta`: Install rolling `beta-latest` (panel only).
- `--full-beta` / `beta-full`: Install rolling `beta-latest` + Static Engines Bundle.
- `--bundle` / `engines`: Install Static Engines Bundle only.
- `--wipe` / `wipe`: Wipe previous service, binaries, CLI, and data directory before installing.
- `[port]`: Optional custom panel port (defaults to a random port in `20000–54999`).

## Post-Install CLI Menu
```bash
mcontrol
```
