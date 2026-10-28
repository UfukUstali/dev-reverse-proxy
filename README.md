# dev-reverse-proxy

Publish a Linux development service at a public HTTPS URL through an existing Traefik gateway and Tailscale. Applications may bind to loopback. Developer machines use their existing Tailscale enrollment and need no DNS credentials.

## Use

Install with Go 1.25 or newer:

```bash
go install ./client/devrp
export SERVER=http://gateway.your-tailnet.ts.net:8080

# Launch an application. devrp selects an available application port.
devrp -i myapp -- npm run dev

# Launch on an explicit application port.
devrp -i api -p 3035 -- node server.js

# Publish an already-running application.
devrp -i api -p 3035

# Also publish one hostname label beneath the publication name.
devrp -i tenants -p 3035 -w
```

The gateway supplies the domain. With `devrp.liuf.uk`, `myapp` receives `https://myapp.devrp.liuf.uk`. Wildcard mode also serves names such as `tenant.tenants.devrp.liuf.uk`. Names must be lowercase single DNS labels, at most 63 characters. The default is `myapp`; active names cannot be taken over.

`-s` or `-server` overrides `SERVER`. There is no local fallback. `-i` or `-id` overrides `ID`, `-p` or `-port` overrides `PORT`, and `-w` or `-wildcard` overrides `WILDCARD`. Pass `-w=false` to disable an environment wildcard setting. Separate the child command with `--`.

Launched applications receive `PORT`, `DEVRP_ID`, and `DEVRP_BASE_URL`. The application port differs from the Tailscale forwarding port. devrp owns one foreground `tailscale serve --tcp` process per publication in ports 20000 through 29999. Tailscale must be on PATH and the current account must have Serve permission.

`URL assigned` means registration succeeded. `Ready` additionally means the local port accepts connections, public DNS points to the gateway, and public TLS validates. Wildcard readiness checks a child hostname too. After sixty seconds devrp reports the pending condition and keeps trying. It does not require an application health endpoint.

Ctrl-C removes sharing and stops a launched application process group. It leaves independently started applications running. Normal child exit removes sharing. Gateway outages leave applications running while devrp retries. Publications expire after thirty seconds without a heartbeat. If someone else claims the name during an outage, devrp stops sharing and keeps supervising the launched application until it exits or you press Ctrl-C.

## Gateway

For guided setup, copy or clone this checkout onto the VPS and run `./scripts/setup-gateway.sh` there. It requires existing Traefik v3, Tailscale, and Docker Compose. The wizard saves settings in a private `.env`, guides DNS and tailnet changes, and asks before starting the gateway.

Use the existing public VPS, Traefik v3 deployment, and DNS challenge resolver. Follow [gateway setup](docs/gateway-setup.md) for the tailnet grants, forwarding trust chain, shared wildcard certificate, and live acceptance checks.

Build the registration service with `go build -o devrp-gateway ./server`. Configure these environment variables on the gateway:

| Variable | Meaning |
| --- | --- |
| `LISTEN_ADDR` | Required gateway Tailscale IP and port, such as `100.64.0.1:8080` |
| `GATEWAY_PUBLIC_IP` | Required public VPS address used by DNS and readiness |
| `PUBLIC_DOMAIN` | Public base domain, default `devrp.liuf.uk` |
| `CONFIG_DIR` | Existing Traefik watched directory, default `/config` |
| `TRAEFIK_ENTRYPOINT` | HTTPS entrypoint, default `websecure` |
| `TRAEFIK_RESOLVER` | Existing DNS challenge resolver, default `hetzner` |
| `TAILSCALE_SOCKET` | Existing tailscaled socket, default `/var/run/tailscale/tailscaled.sock` |
| `HETZNER_TOKEN` | Cloud API token, required only for wildcard publications |
| `HETZNER_ZONE` | Existing Cloud DNS zone name or ID, required only for wildcard publications |
| `AUTHCENTER_ROUTING_SUBDOMAIN` | Optional Authcenter publication name, such as `authcenter`; configure together with the secret |
| `AUTHCENTER_ROUTING_SECRET` | Bearer secret for routing polls |
| `AUTHCENTER_ROUTING_INTERVAL` | Positive Go duration between polls, default `30s` |

The Compose file runs only the gateway with host networking. Set `GATEWAY_TAILSCALE_IP`, `GATEWAY_PUBLIC_IP`, and `TRAEFIK_CONFIG_DIR` before `docker compose up -d --build`. It mounts the existing tailscaled socket and watched directory. It does not create another Traefik or Tailscale deployment.

Ordinary publications use the centrally provisioned wildcard DNS record and certificate and never call Hetzner. Wildcard publications create or reuse the exact hostname and child-wildcard address records. Conflicts fail registration. Records remain after sharing ends; route removal ends access. Traefik alone acquires and renews certificates.

The gateway owns `devrp.yml` in its watched directory. Use one gateway instance and a dedicated control port. Startup clears stale routes; orderly shutdown clears active routes. The gateway stores leases in memory, so a restart causes running CLIs to register again. The old local-only `dynamic.yml` file is not used; remove it during migration if it was created by the previous devrp deployment.

## Authcenter aliases

To import tenant anchor hostnames, set `AUTHCENTER_ROUTING_SUBDOMAIN=authcenter` and `AUTHCENTER_ROUTING_SECRET` on the gateway, or in the Compose `.env`. The subdomain must be a lowercase single DNS label, like a publication name; full URLs, domains, ports, and paths are rejected. The gateway builds `https://authcenter.<PUBLIC_DOMAIN>/routing`, using the existing public HTTPS gateway. Keep the secret out of version control. Polling stays silent while that exact publication is absent or expired, wakes when it registers, and repeats at the configured interval while its lease is active. Aliases alone do not enable polling. Each GET sends `Authorization: Bearer <secret>` and accepts HTTP 200 with `Content-Type: application/json` and this development contract:

```json
{"routes":[{"domain":"acme-templatefoobar.devrp.liuf.uk","appDomain":"templatefoobar.devrp.liuf.uk"}]}
```

`routes` is a complete replacement snapshot. `{"routes":[]}` clears imported aliases. Polls have a five-second timeout and a one-MiB response limit; redirects are rejected. Malformed JSON, unexpected or duplicate fields, invalid hostnames, duplicate alias ownership, conflicting publication hostnames, HTTP failures, and configuration write failures retain the last accepted snapshot. Taking Authcenter offline also retains that snapshot; responses from a publication that leaves during a poll are discarded. There is no persisted alias cache across gateway restarts.

`appDomain` must exactly equal `<publication.ID>.<PUBLIC_DOMAIN>`. The gateway uses only that live publication's Tailscale identity address and forwarding port. It never resolves `appDomain` through DNS and never accepts addresses or ports from Authcenter. An alias without a live target remains dormant. Lease expiry and unregister remove its routing even while Authcenter is unavailable; registering the matching publication restores it without another successful poll. Publication changes rebuild aliases. If a publication later claims an imported single-label hostname, its ordinary route takes precedence; the alias can resume after that publication leaves.

Alias hostnames must be exactly one lowercase DNS label beneath `PUBLIC_DOMAIN`, such as `acme-templatefoobar.devrp.liuf.uk`, without ports, wildcards, trailing dots, or URL syntax. They use the centrally provisioned wildcard DNS and certificate, just like ordinary publications. The operator must provision that shared wildcard before use; alias routers never request certificates.

The gateway rejects all nested aliases, including `acme.templatefoobar.devrp.liuf.uk`, even when the target publication uses wildcard mode. It also rejects aliases equal to any application hostname named in the snapshot and external domains. Any rejected route rejects the entire snapshot and retains the previous accepted aliases. Supporting external domains would require an explicit DNS and certificate policy. Ordinary wildcard publications keep their existing routing behavior independently of Authcenter aliases.

The CLI logs the aliases of its publication: `Alias added: <url>` after registration and whenever a heartbeat reports a new alias, and `Alias removed: <url>` when one disappears. New aliases therefore appear within one poll interval plus the five-second heartbeat. The CLI accepts only distinct `https://<label>.<PUBLIC_DOMAIN>` URLs other than its own and ignores any other list with a warning. Aliases overridden by a publication of the same name are not reported. Older CLIs ignore the field, and newer CLIs print no aliases against older gateways.

Aliases retain the visitor's Host and Traefik's HTTPS forwarding metadata. Apps use the anchor hostname to select the tenant and OIDC realm. App-owned domains remain reserved server identities; Authcenter must not emit them as application routes. Ordinary publication routing continues to work, and consuming apps are responsible for rejecting own-domain requests that lack anchor context.

## Control API

Every endpoint requires an authenticated Tailscale peer with the `devrp.liuf.uk/publish` application capability. The gateway looks up the TCP peer through tailscaled and derives the destination address from that identity. Supplied identity headers and arbitrary destination fields cannot authorize routing.

| Endpoint | Request |
| --- | --- |
| `POST /register` | JSON with `id`, `port`, `wildcard`, and `session` |
| `POST /heartbeat` | Query parameters `id` and `session` |
| `POST /unregister` | Query parameters `id` and `session` |
| `GET /status` | Returns gateway domain and public IP |
| `GET /clients` | Lists active publications, excluding session identifiers |

The CLI generates a random session identifier for each publication attempt. Sessions scope ownership, idempotent registration, heartbeat, and removal. They are not configured credentials. Registration returns the HTTPS `url`, `domain`, and `public_ip`. Registration and heartbeat responses also carry `aliases`, the sorted Authcenter alias URLs currently routed to that publication; the field is omitted when there are none. An expired session receives `410` and requires a fresh session; a competing name receives `409`.

## Verification

```bash
go build ./...
go vet ./...
go test -race ./...

# Include the actual Traefik process test.
TRAEFIK_BINARY=/path/to/traefik go test -race ./...
```

Tests use Go's HTTP fixtures, a Python 3 executable substitute for Tailscale Serve, and a local Hetzner-compatible API. The Traefik test uses generated test certificates and isolated local ports. Without a Traefik binary that test reports a skip. The selected development versions are Tailscale 1.98.10, Traefik 3.6.21, Hetzner SDK v2.47.0, and Go 1.26.5. Real Tailscale authorization and public DNS/certificate provisioning still require the live checks in the setup guide.
