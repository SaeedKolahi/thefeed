# thefeed

DNS-based feed reader for Telegram channels. Designed for environments where only DNS queries work.

## How It Works

```
┌──────────────┐     DNS TXT Query       ┌──────────────┐     MTProto     ┌──────────┐
│    Client    │ ──────────────────────▸ │    Server    │ ──────────────▸ │ Telegram │
│  (TUI app)   │ ◂────────────────────── │  (DNS auth)  │ ◂────────────── │   API    │
└──────────────┘     Encrypted TXT       └──────────────┘                 └──────────┘
```

**Server** (runs outside censored network):
- Connects to Telegram, reads messages from configured channels
- Serves feed data as encrypted DNS TXT responses
- Random padding on responses to vary size (anti-DPI)
- Session persistence — login once, run forever

**Client** (runs inside censored network):
- Sends encrypted DNS TXT queries via available resolvers
- Single-label base32 encoding (stealthier) or double-label hex
- Rate limiting to respect resolver limits
- TUI with RTL/Farsi support, log panel showing DNS queries
- Built-in resolver scanner (file with IPs/CIDRs or single CIDR)

## Anti-DPI Features

- **Variable response size**: Random padding (0-32 bytes) on each DNS response prevents fingerprinting by fixed packet size
- **Single-label queries**: Base32 encoded subdomain in one DNS label (`abc123def.t.example.com`) instead of the more detectable two-label hex pattern
- **Resolver shuffling**: Queries are distributed across resolvers randomly
- **Rate limiting**: Configurable query rate to blend with normal DNS traffic
- **Concurrency limiting**: Max 3 concurrent block fetches to avoid DNS bursts
- **Random query padding**: 4 random bytes in each query payload

## Protocol

**Block size**: 180 bytes payload (fits in 512-byte UDP DNS with padding + encryption overhead)

**Query format** (single-label, default): `[base32_encrypted].t.example.com`
**Query format** (double-label): `[hex_part1].[hex_part2].t.example.com`
- Payload: 4 random bytes + 2 channel + 2 block = 8 bytes, AES-256-GCM encrypted

**Response**: `[2-byte length][data][random padding]` → AES-256-GCM encrypted → Base64

**Encryption**: AES-256-GCM with HKDF-derived keys from shared passphrase

## Quick Install (Server)

```bash
# One-line install (downloads latest release from GitHub)
bash <(curl -Ls https://raw.githubusercontent.com/sartoopjj/thefeed/main/scripts/install.sh)
```

Or manually:

```bash
# On your server (Linux with systemd)
curl -Ls https://raw.githubusercontent.com/sartoopjj/thefeed/main/scripts/install.sh -o install.sh
sudo bash install.sh
```

The script will:
1. Download the latest release binary from GitHub
2. Ask for your domain, passphrase, Telegram credentials, channels
3. Login to Telegram interactively (one-time)
4. Set up a systemd service

Update: `sudo bash install.sh` (detects existing config, only updates binary)
Re-login: `sudo bash install.sh --login`
Uninstall: `sudo bash install.sh --uninstall`

## Manual Setup

### Prerequisites

- Go 1.26+
- Telegram API credentials from https://my.telegram.org
- A domain with NS records pointing to your server

### Server

```bash
# Build
make build-server

# First run: login to Telegram and save session
./build/thefeed-server \
  --login-only \
  --domain t.example.com \
  --key "your-secret-passphrase" \
  --channels configs/channels.txt \
  --api-id 12345 \
  --api-hash "your-api-hash" \
  --phone "+1234567890" \
  --session session.json

# Normal run (uses saved session)
./build/thefeed-server \
  --domain t.example.com \
  --key "your-secret-passphrase" \
  --channels configs/channels.txt \
  --api-id 12345 \
  --api-hash "your-api-hash" \
  --phone "+1234567890" \
  --session session.json \
  --listen ":5300"
```

Environment variables: `THEFEED_DOMAIN`, `THEFEED_KEY`, `TELEGRAM_API_ID`, `TELEGRAM_API_HASH`, `TELEGRAM_PHONE`, `TELEGRAM_PASSWORD`

#### Server Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--domain` | | DNS domain (required) |
| `--key` | | Encryption passphrase (required) |
| `--channels` | `channels.txt` | Path to channels file |
| `--api-id` | | Telegram API ID (required) |
| `--api-hash` | | Telegram API Hash (required) |
| `--phone` | | Telegram phone number (required) |
| `--session` | `session.json` | Path to Telegram session file |
| `--login-only` | `false` | Authenticate to Telegram, save session, exit |
| `--listen` | `:5300` | DNS listen address |
| `--padding` | `32` | Max random padding bytes (0=disabled) |
| `--version` | | Show version and exit |

### Client

```bash
# Build
make build-client

# Basic usage
./build/thefeed-client \
  --domain t.example.com \
  --key "your-secret-passphrase" \
  --resolvers "8.8.8.8,1.1.1.1"

# With resolver scanning from file
./build/thefeed-client \
  --domain t.example.com \
  --key "your-secret-passphrase" \
  --scan configs/resolvers.txt \
  --rate 5

# Scan a CIDR range
./build/thefeed-client \
  --domain t.example.com \
  --key "your-secret-passphrase" \
  --scan "8.8.8.0/24" \
  --resolvers configs/resolvers.txt
```

#### Client Flags

| Flag | Default | Description |
|------|---------|-------------|
| `--domain` | | DNS domain (required) |
| `--key` | | Encryption passphrase (required) |
| `--resolvers` | | Comma-separated IPs or path to file |
| `--scan` | | File with IPs/CIDRs or single CIDR to scan |
| `--scan-workers` | `50` | Concurrent scanner workers |
| `--rate` | `0` | Max DNS queries/sec (0=unlimited) |
| `--query-mode` | `single` | `single` (base32) or `double` (hex) |
| `--cache` | `~/.thefeed/cache` | Cache directory |
| `--version` | | Show version and exit |

### TUI Controls

| Key | Action |
|-----|--------|
| `Tab` / `←` / `→` | Cycle panels (channels → messages → log) |
| `j` / `k` / `↑` / `↓` | Navigate up/down |
| `r` | Refresh feed |
| `PgUp` / `PgDn` | Scroll content |
| `q` / `Ctrl+C` | Quit |

The TUI has three panels:
- **Channels** (left): channel list with selection
- **Messages** (right): messages with RTL/Farsi support
- **Log** (bottom): DNS queries being sent (debug)

## Development

```bash
make test        # Run tests
make build       # Build both binaries
make build-all   # Cross-compile all platforms
make vet         # Go vet
make fmt         # Format code
make clean       # Remove build artifacts
```

## DNS Setup

1. Register a domain (e.g., `example.com`)
2. Add NS record: `t.example.com NS your-server-ip`
3. Or add a glue record pointing `ns.example.com` to your server IP, then `t.example.com NS ns.example.com`
4. Run the server on port 53 (or 5300 and redirect with iptables)

## channels.txt Format

```
# Comments start with #
@VahidOnline
```

## Resolver File Format

```
# One IP or CIDR per line
8.8.8.8
1.1.1.1
9.9.9.9
208.67.222.0/24
```

## Security

- All queries and responses are encrypted with AES-256-GCM
- Separate HKDF-derived keys for queries and responses
- Random padding in queries prevents caching and replay
- Random padding in responses prevents DPI size fingerprinting
- No session state — each query is independent
- Pre-shared passphrase required for both client and server
- Telegram 2FA password is prompted interactively (not stored in CLI args)
- Session file stored with 0600 permissions

## Service Management

```bash
# After install.sh
systemctl status thefeed-server
systemctl restart thefeed-server
journalctl -u thefeed-server -f

# Update channels
sudo vi /etc/thefeed/channels.txt
sudo systemctl restart thefeed-server

# Update binary
cd thefeed && git pull && sudo bash scripts/install.sh
```

## License

MIT
