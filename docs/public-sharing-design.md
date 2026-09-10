# Public service sharing design

The design choices below are confirmed through interview answers A1 through A24 and have been synthesized into the [implementation spec](public-sharing-spec.md). This document retains the design explanation and supporting research. Implementation and deployment have not begun.

## Purpose and scope

dev-reverse-proxy publishes development services at public HTTPS URLs beneath `devrp.liuf.uk`. Public sharing replaces the existing local-only `.localhost` behavior. Support Linux development machines and a Linux gateway initially.

The operator's existing VPS and Traefik provide the public gateway. Configuration publishing and service traffic between the gateway and development machines use their existing Tailscale connections. Developer machines need no application tokens, Tailscale API keys, or additional node enrollment. One-time gateway, DNS, and tailnet policy setup are acceptable.

Visitors need no gateway authentication. Applications can supply their own authentication. Support HTTP applications, WebSockets, and streaming responses. Public raw TCP, UDP, and HTTPS to local applications are outside the initial scope.

## Traffic and trust

```text
Visitor -- public HTTPS --> existing Traefik on VPS
                                  |
                           HTTP over Tailscale
                                  |
                         Tailscale Serve TCP forwarder
                                  |
                           local HTTP application

Developer CLI -- Tailscale --> gateway configuration API
```

Requests and responses between the gateway and development machine travel over Tailscale. Traefik terminates public HTTPS and supplies trusted forwarding headers. TCP forwarding preserves the public Host and Traefik's original scheme and visitor-address metadata. Applications trust the configured proxy chain and remain responsible for allowed hostnames, redirects, and cookies. Do not rewrite response bodies.

Only the operator's authorized devices can publish, using Tailscale identity and policy. Each device can publish only services on itself. Restrict access to forwarding listeners to the gateway. Publication ownership must govern registration, heartbeat, and removal, rather than trusting a caller-supplied name alone.

## CLI and publication names

Preserve the launch workflow and add publication of an existing service:

```bash
devrp -i myapp -- npm run dev
devrp -i myapp -p 3000
```

Preserve `-s`, `--server`, and `SERVER` for the gateway's tailnet address. Missing gateway configuration produces a setup error. The gateway supplies the public base domain. Preserve the familiar ID, port, and wildcard flags and existing environment-variable configuration where applicable.

In launch mode, select or accept the application port and supply `PORT`, `DEVRP_ID`, and `DEVRP_BASE_URL=https://<id>.devrp.liuf.uk` to the child. In existing-service mode, the selected port identifies the application already running. Applications bound only to loopback must work.

Publication names are global across development machines. IDs are lowercase single DNS labels, such as `myapp` or `my-api`; preserve `myapp` as the default. Dotted IDs are no longer accepted. Competing active publications fail with a clear conflict, without replacing the publication or changing the requested name.

`-w` publishes both the application's own hostname and hostnames one label beneath it, such as `tenant.myapp.devrp.liuf.uk`, to the same local service.

## DNS and certificates

Provision `*.devrp.liuf.uk` to point at the VPS public IP during infrastructure setup. Ordinary publications use that record and the centrally managed `*.devrp.liuf.uk` certificate. Their startup makes no Hetzner API calls.

Only wildcard publications use the Hetzner DNS API. For `-i myapp -w`, ensure both `myapp.devrp.liuf.uk` and `*.myapp.devrp.liuf.uk` have address records pointing to the public gateway. Configure the zone, credentials, and public IP on the gateway. Reuse matching records, reject conflicts, and retain records between sessions. Removing the Traefik route stops sharing. An ordinary publication can reuse retained records without calling Hetzner.

Traefik remains the sole certificate manager. Wildcard publications request coverage for their hostname and one additional label through Traefik's existing DNS challenge resolver. devrp does not acquire certificates independently.

DNS wildcards can match names several labels below them, but existing intermediate names stop fallback to a higher wildcard. Creating `*.myapp.devrp.liuf.uk` makes `myapp.devrp.liuf.uk` exist, so its own address must be explicit. Certificate-validation records can also create intermediate names. These are consequences of [RFC 4592 sections 2.2 and 3.3](https://www.rfc-editor.org/rfc/rfc4592.html#section-2.2), not findings from inspecting the operator's live zone.

Use Hetzner's official Go client, [`github.com/hetznercloud/hcloud-go/v2/hcloud`](https://github.com/hetznercloud/hcloud-go), for [DNS record sets](https://docs.hetzner.cloud/reference/cloud#zone-rrsets). A custom HTTP client is unnecessary unless implementation reveals a gap. Hetzner [removed its legacy DNS Console and API on 27 May 2026](https://status.hetzner.com/incident/c2146c42-6dd2-4454-916a-19f07e0e5a44). Select a released SDK and compatible Go toolchain during implementation.

## Traefik integration

Run the gateway beside existing Traefik. Write a dedicated file into its watched dynamic-configuration directory using the existing HTTPS entrypoint and certificate resolver. Route each publication to its development machine's tailnet address and forwarding port.

Leave public error responses to existing Traefik behavior. Do not build custom responses or pages for missing publications or unavailable applications.

## Local forwarding

Use the existing Tailscale daemon through a foreground Serve subprocess per publication:

```bash
tailscale serve --tcp=23001 tcp://127.0.0.1:3000
```

Here `3000` is the application's selected port and `23001` is an available forwarding port advertised to the gateway. Traefik uses `http://<development-machine-tailnet-ip>:23001`. The traffic remains HTTP, encrypted across Tailscale. See the [Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve).

Supervise Serve without `--bg`. Stop and wait for that publication's subprocess during cleanup and supervise it during reconnection. Preserve unrelated Serve configuration and other publications. Retry concurrent configuration conflicts and choose another port if occupied. Never use `serve reset` for publication cleanup.

TCP forwarding was selected in A24 because the [Tailscale HTTP proxy](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go) and [Go Rewrite mechanism](https://github.com/golang/go/blob/master/src/net/http/httputil/reverseproxy.go) replace incoming forwarding metadata. HTTP Serve would lose Traefik's visitor address and original HTTPS scheme. The [Serve CLI implementation](https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_v2.go) associates foreground configuration with its session. These findings come from upstream source, not a live test of the operator's installation.

## Lifecycle and readiness

Sharing lasts while the CLI runs. Normal child exit ends its publication. Ctrl-C removes the publication and stops a launched child, but leaves an independently started application running.

Configuration errors, name conflicts, and conflicting DNS records fail before launching a child. After successful registration, launch the child and check readiness. Distinguish URL assignment from readiness, which requires the local port to accept connections, public DNS to resolve to the gateway, and successful public TLS negotiation. No application-specific health endpoint is required. After sixty seconds without readiness, report the pending condition and keep retrying until cancellation.

Expire publications after thirty seconds without a heartbeat, freeing their names. A running CLI reconnects after temporary network interruptions or sleep and re-registers after a gateway restart. Recover a name only if available. If another publication has claimed it, report the conflict and stop that publication attempt while leaving the application running. Gateway or network outages do not terminate the application.

## Deployment inputs and verification

The existing Traefik deployment has not been inspected. Before deployment, obtain its configuration, watched directory, HTTPS entrypoint, and certificate resolver names. Also supply the gateway's tailnet address and identity, authorization policy, public IP, and Hetzner zone configuration. Configure credentials directly on the gateway rather than recording them in this document.

Implementation verification must cover CLI launch and existing-service workflows, name ownership and expiry, reconnection without stale-session interference, concurrent Serve processes and isolated cleanup, ordinary publication without Hetzner access, wildcard DNS conflicts and reuse, Traefik configuration generation, and forwarding of Host, visitor address, HTTPS scheme, WebSockets, and streams. Readiness checks must distinguish ordinary and wildcard publication coverage.

Live Tailscale, DNS, certificate issuance, and existing Traefik integration remain unverified. The current repository still implements the original local proxy.
