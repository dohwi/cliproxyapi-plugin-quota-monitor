<p align="center"><b>English</b> · <a href="README.ko.md">한국어</a></p>

<div align="center">
  <h1>cliproxyapi-plugin-quota-monitor</h1>
  <p>A native Go plugin for CLIProxyAPI that monitors provider account quotas and credits, with a responsive bilingual (English/Korean) dashboard.</p>

  <p>
    <a href="https://go.dev" target="_blank" rel="noreferrer"><img src="https://img.shields.io/badge/Go-1.26.9-00ADD8?style=flat-square&logo=go&logoColor=white" alt="Go 1.26.9" /></a>
    <a href="https://github.com/dohwi/cliproxyapi-plugin-quota-monitor/actions/workflows/verify.yml" target="_blank" rel="noreferrer"><img src="https://img.shields.io/github/actions/workflow/status/dohwi/cliproxyapi-plugin-quota-monitor/verify.yml?branch=main&style=flat-square&label=verify" alt="Verify workflow status" /></a>
    <a href="LICENSE" target="_blank" rel="noreferrer"><img src="https://img.shields.io/badge/License-PolyForm_Noncommercial_1.0.0-blue?style=flat-square" alt="PolyForm Noncommercial 1.0.0" /></a>
  </p>

  <p><a href="#preview">Preview</a> · <a href="#features">Features</a> · <a href="#getting-started">Getting Started</a> · <a href="#routes">Routes</a> · <a href="#language">Language</a> · <a href="docs/contract.md">Contract</a></p>
</div>

---

## Overview

`cliproxyapi-plugin-quota-monitor` is a `c-shared` C ABI native Go plugin that the CLIProxyAPI host loads directly. It periodically and safely collects the remaining quota, reset schedule, active/disabled state and available credit balance of your registered AI provider accounts, and serves them through a self-contained responsive dashboard and a management API.

## Preview

| Desktop Dashboard | Mobile View |
| :---: | :---: |
| <img src="docs/assets/quota-desktop.en.png" alt="Account quota dashboard: status summary, per-account limits and reset times" width="100%" /> | <img src="docs/assets/quota-mobile.en.png" alt="Account quota dashboard on mobile" width="100%" /> |

## Features

- **Per-account quota and credit tracking**: remaining percentage per window, reset time and credit balance for Claude, Codex, Antigravity and other providers.
- **Recently used account highlight**: visually marks the account that most recently handled traffic through the proxy.
- **Bounded collection**: per-account probe schedule, a single background worker, timeouts and concurrency limits keep provider APIs from being overloaded.
- **Refresh consistency**: generation-based cache management stops a stale asynchronous probe from overwriting newer data.
- **Self-contained responsive dashboard**: plain HTML/CSS/JS with self-hosted WOFF2 fonts (SUIT, Manrope). No CDN and no chart library.
- **Bilingual UI**: English and Korean, switchable from the dashboard header (see [Language](#language)).
- **Safe endpoint separation**: read-only snapshot endpoints are separate from the explicit refresh endpoint, which has a 30-second global cooldown and rate limiting.

## Tech Stack

| Domain | Technology / Specification |
|---|---|
| Runtime & ABI | Go 1.26.9, cgo, C shared library (`.so`) |
| Host Integration | CLIProxyAPI Plugin C ABI (`v0` management/resource routes) |
| Embedded UI | HTML5, Vanilla JavaScript, CSS Grid/Flexbox |
| Bundled Typography | SUIT Regular/SemiBold, Manrope Variable (self-hosted WOFF2) |
| Verification | Go race/vet, Node layout and i18n contract tests, native C smoke test (`dlopen`) |

## Getting Started

Build on the same OS, architecture and C runtime as the host process (Linux amd64, glibc).

Bootstrap the local runtimes with [mise](https://mise.jdx.dev):

```bash
# Managed runtimes
mise trust
mise install

# Ubuntu/Debian native build tools
sudo apt-get update && sudo apt-get install -y build-essential

# Run tests and the native ABI smoke test
make verify
make native-smoke
```

### Build

```bash
make build
```

The build writes `artifacts/quota-monitor.so` and the header `artifacts/quota-monitor.h`.

## Install

Once listed in the [CLIProxyAPI Plugins Store](https://github.com/router-for-me/CLIProxyAPI-Plugins-Store), install `quota-monitor` from the host's plugin page. To install manually, download `quota-monitor_<version>_linux_amd64.zip` and `checksums.txt` from the [latest release](https://github.com/dohwi/cliproxyapi-plugin-quota-monitor/releases/latest), run `sha256sum -c checksums.txt`, and unzip `quota-monitor.so` into the host's plugin directory.

The Linux build targets glibc 2.36 or older (the official Debian bookworm image). Only `linux/amd64` is published; other platforms must build from source.

## Language

The dashboard ships in English and Korean. A single translate icon button in the filter area switches between Korean and English; its tooltip names the language it switches to.

- **Selection order**: saved choice, then browser language (Korean selects Korean, anything else selects English), then Korean.
- **Shared setting**: the choice is stored in the browser under `cpa_plugin_lang`, the same key used by the [usage-audit](https://github.com/dohwi/cliproxyapi-plugin-usage-audit) dashboard. Changing it in one open dashboard updates the other. This only applies when both dashboards are served from the same origin.
- **Privacy**: this key is the only thing the page writes to browser storage. Credentials are never stored.
- **Not translated**: the plugin name shown in the host menu is registered once at load time, so it stays Korean ("계정 쿼터").

## Routes

Once the CLIProxyAPI host has loaded the plugin, these routes are available:

| Method | Route | Description |
|---|---|---|
| `GET` | `/v0/resource/plugins/quota-monitor/dashboard` | Responsive account quota dashboard |
| `GET` | `/v0/resource/plugins/quota-monitor/data/accounts` | Cached account quota snapshot (never triggers a probe) |
| `GET` | `/v0/resource/plugins/quota-monitor/data/refresh?confirm=1` | Explicit refresh of all accounts (30-second cooldown) |
| `POST` | `/v0/management/plugins/quota-monitor/refresh` | Forced refresh with management authentication |
| `GET` | `/v0/management/plugins/quota-monitor/status` | Collector status and statistics |

## Project Structure

```text
cliproxyapi-plugin-quota-monitor/
├── main.go                 # c-shared C ABI entry point (plugin.register, management routes)
├── internal/
│   ├── quota/              # Per-provider quota clients, parsers and domain models
│   ├── plugin/             # Plugin lifecycle, collection scheduler, snapshots, handlers
│   ├── authdir/            # CLIProxyAPI auth-dir parser
│   ├── availability/       # Per-provider-family availability evaluation
│   ├── overview/           # Conservative per-provider quota summary
│   └── dashboard/          # Embedded dashboard: dashboard.html, style.css, app.js, font assets
├── docs/                   # Protocol contract, management spec and images
├── scripts/                # Dashboard layout verification scripts
├── verification/           # dlopen-based native C ABI smoke test
├── Makefile                # Build, verify and smoke-test targets
└── THIRD_PARTY_NOTICES.md  # Bundled font license notices
```

The dashboard source is three plain files in `internal/dashboard/`. They are embedded with `go:embed` and assembled into one self-contained page at start-up.

## Documentation

- [Plugin Contract](docs/contract.md): C ABI, resource and release contract
- [Management Contract](docs/management.md): configuration and management route details
- [Remaining API](docs/remaining-api.md): snapshot and refresh API specification
- [Collection Contract](docs/collection-contract.md): background collection and security policy

## License

[PolyForm Noncommercial 1.0.0](LICENSE). You may use, modify and distribute this software for noncommercial purposes such as personal use, research and education. Commercial use is not permitted.

## Security & Attribution

- Keys, OAuth tokens and real account credentials are never committed to this repository.
- Bundled dashboard fonts and their licenses are listed in [THIRD_PARTY_NOTICES.md](THIRD_PARTY_NOTICES.md).
