# dev-reverse-proxy

A lightweight reverse proxy for local development. Register your services with custom subdomains and access them via `http://yourapp.localhost`.

## Quick Start

```bash
docker compose up -d
go install ./client/devrp
```

Make sure your Go bin directory is in your PATH (e.g., `export PATH=$PATH:$(go env GOPATH)/bin`).

## Usage

```bash
devrp -i myapp -- npm run dev
```

Your app will be available at `http://myapp.localhost`.

## Options

- `-s, -server` - Server URL (default: `http://localhost:8080`)
- `-i, -id` - Client identifier (subdomain)
- `-p, -port` - Port number (auto-selected if not set)

Or via environment variables:

```bash
SERVER=http://localhost:8080 ID=myapp devrp -- npm run dev
```

The client sets `PORT`, `DEVRP_ID`, and `DEVRP_BASE_URL` environment variables for your process.

## Endpoints

- `POST /register` - Register a new subdomain
- `POST /heartbeat?id=<id>` - Send heartbeat to keep registration alive
- `POST /unregister?id=<id>` - Unregister a subdomain
- `GET /status` - Server status
- `GET /clients` - List active clients
