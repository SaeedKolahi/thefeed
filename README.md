# thefeed

DNS-based feed reader for Telegram channels. Designed for environments where only DNS queries work.

[English](README.md) | [فارسی](README-FA.md)

## How It Works

```
┌──────────────┐     DNS TXT Query       ┌──────────────┐     MTProto     ┌──────────┐
│    Client    │ ──────────────────────▸ │    Server    │ ──────────────▸ │ Telegram │
│  (Web UI)    │ ◂────────────────────── │  (DNS auth)  │ ◂────────────── │   API    │
└──────────────┘     Encrypted TXT       └──────────────┘                 └──────────┘
```

**Server** (runs outside censored network):
- Connects to Telegram, reads messages from configured channels
- Serves feed data as encrypted DNS TXT responses
- Random padding on responses to vary size (anti-DPI)
- Session persistence — login once, run forever
- No-Telegram mode (`--no-telegram`) — reads public channels without needing Telegram credentials
- All data stored in a single directory

**Client** (runs inside censored network):
- Browser-based web UI with RTL/Farsi support (VazirMatn font)
- Configure via the web UI — no CLI flags needed
- Sends encrypted DNS TXT queries via available resolvers
- **Resolver scoring**: tracks per-resolver success rate and latency; healthier resolvers are preferred automatically
- **Scatter mode**: fans out the same DNS request to multiple resolvers simultaneously and uses the fastest response (default: 2 concurrent resolvers per request)
- **1-hour localStorage cache**: channel list and messages are cached in the browser — reopening the app shows cached data instantly while a fresh fetch runs in the background
- Send messages to channels and private chats (requires server `--allow-manage`)
- Channel management (add/remove channels remotely via admin commands)
- Message compression (deflate) for efficient transfer
- Web UI password protection (`--password` on client)
- New message indicators and next-fetch countdown timer
- Channel type badges (Private/Public)
- Media type detection (`[IMAGE]`, `[VIDEO]`, etc.)
- Live DNS query log in the browser
- All data (config, cache) stored next to the binary

## Anti-DPI Features

- Variable response and query sizes to prevent fingerprinting
- Multiple query encoding modes for stealth
- **Resolver scoring**: per-resolver success-rate + latency scoreboard; high-scoring resolvers are picked more often via weighted-random selection
- **Scatter mode**: same block fetched from N resolvers simultaneously, first response wins — faster fetches and implicit failover
- Rate limiting and background noise traffic to blend in
- Message compression to minimize query count

## Protocol

All communication is encrypted with AES-256 and transmitted via standard DNS TXT queries and responses. Traffic is designed to blend with normal DNS activity. Message data is compressed before encryption.

## Quick Install (Server)

One-line install (downloads latest release from GitHub)

```bash
sudo bash -c "$(curl -Ls https://raw.githubusercontent.com/sartoopjj/thefeed/main/scripts/install.sh)"
```

Or manually:

```bash
# On your server (Linux with systemd)
curl -Ls https://raw.githubusercontent.com/sartoopjj/thefeed/main/scripts/install.sh -o install.sh
sudo bash install.sh
```

The script will:
1. Download the latest release binary from GitHub
2. Ask for your domain, passphrase, and channels
3. Ask whether to use Telegram login (recommended: **No** — public channels work without it)
4. If Telegram mode: ask for API credentials and login
5. Set up a systemd service

Update:
```bash
sudo bash -c "$(curl -Ls https://raw.githubusercontent.com/sartoopjj/thefeed/main/scripts/install.sh)"
```
Re-login: `curl -Ls https://raw.githubusercontent.com/sartoopjj/thefeed/main/scripts/install.sh | sudo bash -s -- --login`
Uninstall: `curl -Ls https://raw.githubusercontent.com/sartoopjj/thefeed/main/scripts/install.sh | sudo bash -s -- --uninstall`

## Manual Setup

### Prerequisites

- Go 1.26+
- A domain with NS records pointing to your server
- Telegram API credentials from https://my.telegram.org (only if you need private channels)

### Server

```bash
# Build
make build-server

# First run: login to Telegram and save session
./build/thefeed-server \
  --login-only \
  --data-dir ./data \
  --domain t.example.com \
  --key "your-secret-passphrase" \
  --api-id 12345 \
  --api-hash "your-api-hash" \
  --phone "+1234567890"

# Normal run (uses saved session from data directory)
./build/thefeed-server \
  --data-dir ./data \
  --domain t.example.com \
  --key "your-secret-passphrase" \
  --api-id 12345 \
  --api-hash "your-api-hash" \
  --phone "+1234567890" \
  --listen ":53"
```

All data files (session, channels) are stored in the `--data-dir` directory (default: `./data`).

Environment variables: `THEFEED_DOMAIN`, `THEFEED_KEY`, `THEFEED_MSG_LIMIT`, `THEFEED_ALLOW_MANAGE` (set to `0` to force-disable even if the flag is baked into the service), `THEFEED_ENABLE_MEDIA` (`1`/`0`), `TELEGRAM_API_ID`, `TELEGRAM_API_HASH`, `TELEGRAM_PHONE`, `TELEGRAM_PASSWORD`

#### Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | `./data` | Data directory for channels, session, config |
| `--domain` | | DNS domain (required) |
| `--key` | | Encryption passphrase (required) |
| `--channels` | `{data-dir}/channels.txt` | Path to channels file |
| `--api-id` | | Telegram API ID (required) |
| `--api-hash` | | Telegram API Hash (required) |
| `--phone` | | Telegram phone number (required) |
| `--session` | `{data-dir}/session.json` | Path to Telegram session file |
| `--login-only` | `false` | Authenticate to Telegram, save session, exit |
| `--no-telegram` | `false` | Run without Telegram login (public channels only) |
| `--listen` | `:5300` | DNS listen address |
| `--padding` | `32` | Max random padding bytes (0=disabled) |
| `--msg-limit` | `15` | Maximum messages to fetch per Telegram channel |
| `--allow-manage` | `false` | Allow remote send/channel management (default: disabled) |
| `--enable-media` | `false` | Enable media preview/download support over DNS |
| `--version` | | Show version and exit |

### Client

```bash
# Build
make build-client

# Run (opens web UI in browser)
./build/thefeed-client

# Custom data directory and port
./build/thefeed-client --data-dir ./mydata --port 9090

# With remote management enabled
./build/thefeed-client --password "your-secret"
```

On first run, the client creates a `./thefeeddata/` directory next to where you run it. Open `http://127.0.0.1:8080` in your browser and configure your domain, passphrase, and resolvers through the Settings page.

All configuration, cache, and data files are stored in the data directory.

#### Client Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--data-dir` | `./thefeeddata` | Data directory for config, cache |
| `--port` | `8080` | Web UI port |
| `--password` | | Password for web UI (empty = no auth) |
| `--version` | | Show version and exit |

The **concurrent requests (scatter)** setting and all other profile options (resolvers, rate limit, query mode, timeout) are configured through the web UI profile editor, not CLI flags.

#### Android (Termux)

```bash
# Install Termux from F-Droid
pkg update && pkg install curl

# Download Android binary
curl -Lo thefeed-client https://github.com/sartoopjj/thefeed/releases/latest/download/thefeed-client-android-arm64
chmod +x thefeed-client
./thefeed-client
# Open in browser: http://127.0.0.1:8080
```

#### Android (Native APK Wrapper)

> download it from the latest release assets: `thefeed-android-arm64.apk`


You can build or download a native Android app that:
- runs thefeed client binary in a foreground/background service
- opens the local web UI inside an in-app WebView

Project path:
- `android/`

Build steps:

```bash
# 1) Build Android binary from project root
make build-android-arm64

# 2) Copy binary into Android app assets (required filename)
cp build/thefeed-client-android-arm64 android/app/src/main/assets/thefeed-client

# 3) Build debug APK
cd android
gradle wrapper --gradle-version 8.10.2
./gradlew assembleDebug
```

APK output:

```bash
android/app/build/outputs/apk/debug/app-debug.apk
```

Install on device:

```bash
adb install -r android/app/build/outputs/apk/debug/app-debug.apk
```

### Web UI

The browser-based UI has:
- **Channels sidebar** (left): channel list grouped by type (Public/Private) with badges
- **Messages panel** (right): messages with native RTL/Farsi rendering (VazirMatn font)
- **Send panel**: send messages to channels and private chats when Telegram is connected
- **New message badges**: visual indicators for channels with new messages
- **Next-fetch timer**: countdown to next automatic refresh
- **Media detection**: `[IMAGE]`, `[VIDEO]`, `[DOCUMENT]` tag highlighting
- **Log panel** (bottom): live DNS query log
- **Settings modal**: configure domain, passphrase, resolvers, query mode, rate limit, concurrent requests (scatter), timeout, debug mode
- **Per-profile cache**: 1-hour browser cache so data is visible instantly on reopen

## Development

```bash
make test        # Run tests with race detector
make build       # Build both binaries
make build-all   # Cross-compile all platforms (incl. Android)
make upx         # Compress Linux/Windows/Android binaries with UPX
make vet         # Go vet
make fmt         # Format code
make clean       # Remove build artifacts
```

## Releases (GitHub Actions)

Pushing a tag that starts with `v` triggers CI build + GitHub Release.

- Stable release tag example: `v1.4.0`
- Pre-release tag examples: `v1.4.0-rc1`, `v1.4.0-beta.2`

Rule:
- If tag contains `-`, release is marked as **pre-release** automatically.

Release assets include:
- Server/client binaries for all current target platforms
- Native Android wrapper APK: `thefeed-android-arm64.apk`

## DNS Records Setup

You need **two DNS records** on your domain. Suppose your server IP is `203.0.113.10` and you want to use `example.com`:

### 1. A Record for the NS server

| Type | Name | Value |
|------|------|-------|
| A | `ns.example.com` | `203.0.113.10` |

This points a hostname to your server IP.

### 2. NS Record for the tunnel subdomain

| Type | Name | Value |
|------|------|-------|
| NS | `t.example.com` | `ns.example.com` |

This delegates all DNS queries for `t.example.com` (and its subdomains) to your server.

> **Note:** The server needs to receive packets on external port 53. Running on `:53` directly requires root. It's better to listen on an unprivileged port (`:5300`) and port-forward 53 to it.
>
> Replace `eth0` with your actual network interface name (check with `ip a`):
> ```bash
> sudo iptables -I INPUT -p udp --dport 5300 -j ACCEPT
> sudo iptables -t nat -I PREROUTING -i eth0 -p udp --dport 53 -j REDIRECT --to-ports 5300
> sudo ip6tables -I INPUT -p udp --dport 5300 -j ACCEPT
> sudo ip6tables -t nat -I PREROUTING -i eth0 -p udp --dport 53 -j REDIRECT --to-ports 5300
> ```
>
> To make these rules persistent across reboots:
> ```bash
> sudo apt install iptables-persistent   # Debian/Ubuntu
> sudo netfilter-persistent save
> ```

## channels.txt Format

```
# Comments start with #
@VahidOnline
```

## Security

### Two-Part Access Control

**Encryption passphrase (`--key`):** Required on both server and client. Anyone with this passphrase can read all channel messages (including private channels). You can share it with trusted friends so they can read too.

**Remote management (`--allow-manage` on server):** When enabled, anyone with the encryption key can also send messages and manage channels. Disabled by default. Only enable on trusted servers.

**Client web password (`--password`):** Protects all web UI endpoints with HTTP Basic Auth. This is local protection only — it does NOT affect DNS-level access.

### Security Properties

- All communication is end-to-end encrypted (AES-256)
- Pre-shared passphrase required for both client and server
- Each query is independent — no session state on the wire
- Random padding in both directions prevents traffic analysis
- Write operations gated by server-side `--allow-manage` flag
- Telegram 2FA password is prompted interactively (never stored in args)
- Session file stored with restricted permissions (0600)

> **⚠️ Warning:** If you share your passphrase publicly, **anyone** can run their own
> client with your passphrase and read all your messages. There is no way to prevent this.
> The client `--password` flag only protects the web UI on your own machine — it does NOT stop
> others from using the passphrase. **Never share your passphrase publicly.**

## Service Management

```bash
# After install.sh
systemctl status thefeed-server
systemctl restart thefeed-server
journalctl -u thefeed-server -f

# Update channels
sudo vi /opt/thefeed/data/channels.txt
sudo systemctl restart thefeed-server

# Update binary
sudo bash scripts/install.sh
```

## License

MIT

---

<div align="center">

**For FREE IRAN 🇮🇷**

*Everyone deserves free access to information*

</div>
