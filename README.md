# Dev Reverse Proxy

A development reverse proxy that exposes local applications on custom subdomains using a heartbeat-based registration model.

## Architecture

```
┌─────────────┐     HTTP POST      ┌─────────────────┐
│   client    │ ──────────────────>│   Go Server     │
│  (Go binary)│   /register        │  (Port 8080)    │
└─────────────┘                    └────────┬────────┘
       │                                    │
       │ POST /heartbeat (every 10s)        │ Writes config
       │                                    ▼
       └─────────────────────────>  ┌─────────────────┐
                                    │  Docker Volume  │
                                    │  /config        │
                                    └────────┬────────┘
                                             │
                                             │ Watches
                                             ▼
                                    ┌─────────────────┐
                                    │    Traefik      │
                                    │   (Port 80)     │
                                    └────────┬────────┘
                                             │
                                             │ Routes to
                                             ▼
                                    ┌─────────────────┐
                                    │  Local App      │
                                    │  (Any port)     │
                                    └─────────────────┘
```

## Quick Start

### 1. Start the infrastructure

```bash
docker-compose up -d
```

This starts:
- **Go Server** on port 8080 - manages registrations and heartbeats
- **Traefik** on port 80 (HTTP) and 8081 (dashboard)

### 2. Run any command with subdomain

```bash
./client -i myapp -- npm run dev
./client -i api -p 3045 -- pnpm run dev --host 0.0.0.0
```

### 3. Access your app

Open http://myapp.localhost in your browser.

## Client Usage

```bash
./client [options] -- <command> [args...]

Options:
  -s, --server URL   Server URL (default: http://localhost:8080)
  -i, --id ID       Client identifier (subdomain)
  -p, --port PORT   Port number (auto-selected 3000-3100 if not set)

Environment Variables (fallback when flags not provided):
  SERVER   - Server URL (default: http://localhost:8080)
  ID       - Subdomain identifier (default: myapp)
  PORT     - Port number (auto-selected 3000-3100 if not set)
```

### Examples

```bash
# Use default subdomain with auto-selected port
./client -- npm run dev

# Specify custom subdomain
./client -i dashboard -- npm run dev

# Specify custom port
./client -i api -p 8085 -- node server.js

# Use environment variables
ID=api PORT=8085 ./client -- node server.js

# Three-level subdomain (sub.foo.bar.localhost)
./client -i prod.api.service -- npm run dev

# Without -- delimiter (command args after flags)
./client -s http://localhost:8080 -i myapp npm run dev
```

## Subdomain Validation

Subdomains can be max 1500 characters long:
- Each level must be 1-63 characters
- Contain only alphanumeric characters and hyphens
- Cannot start or end with a hyphen

Examples: `myapp`, `api.v1`, `prod.api.service`

## API

### POST /register

Register a new client.

**Request Body:**
```json
{
  "id": "myapp",
  "port": 3000
}
```

**Response:**
```json
{
  "status": "registered",
  "url": "myapp.localhost"
}
```

### POST /heartbeat?id=<id>

Send heartbeat to keep registration alive. Must be called every 10 seconds (or before timeout).

**Response:**
```json
{
  "status": "ok"
}
```

### POST /unregister?id=<id>

Explicitly unregister a client (optional, automatic on missing heartbeats).

**Response:**
```json
{
  "status": "unregistered"
}
```

### GET /status

Get server status and client count.

**Response:**
```json
{
  "status": "ok",
  "clients": 3
}
```

### GET /clients

List all registered clients.

**Response:**
```json
{
  "clients": [
    {
      "id": "myapp",
      "internal_id": "myapp",
      "port": 3000,
      "last_heartbeat": "2026-02-16T10:30:00Z"
    }
  ]
}
```

## Heartbeat Mechanism

1. Client registers via `POST /register`
2. Client sends heartbeat via `POST /heartbeat?id=<id>` every 2 seconds
3. Server checks for expired clients every second
4. If no heartbeat received within timeout (default 5), client is removed
5. On client exit, heartbeats stop and client is automatically cleaned up

## Environment Variables

| Variable | Description | Default |
|----------|-------------|---------|
| `PORT` | Server port | `8080` |
| `CONFIG_DIR` | Traefik config directory | `/config` |
| `HEARTBEAT_TIMEOUT` | Client timeout duration | `5s` |

## File Structure

```
.
├── server/
│   └── main.go           # Go HTTP server with heartbeat
├── client/
│   └── main.go           # Go client binary
├── Dockerfile            # Go server container
├── docker-compose.yml    # Infrastructure setup
└── Makefile              # Helper commands
```

## Development

### Building

```bash
# Build server
go build -o server-bin ./server/

# Build client
go build -o client ./client/
```

### Running Locally

```bash
# Terminal 1: Run server
make dev-server

# Terminal 2: Run client
./client -i test -p 8085 -- python -m http.server 8085
```

### Docker Commands

```bash
make up          # Start infrastructure
make down        # Stop infrastructure
make logs         # View all logs
make server-logs  # View server logs only
make status       # Check server status
```
