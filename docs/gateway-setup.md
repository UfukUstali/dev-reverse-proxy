# Gateway setup

Run the registration service on the existing Linux VPS beside Traefik. The gateway and development machines must already belong to the same tailnet. This setup replaces the old local Traefik deployment.

Run `./scripts/setup-gateway.sh` from a checkout on the VPS for an interactive walkthrough. Use an account that can run Docker and inspect Tailscale. Open the printed dashboard URLs on your own computer if the VPS has no browser. The wizard backs up an existing `.env`, saves settings with mode `0600`, and asks before running Compose. Reruns offer saved values. No GitHub secrets are required.

The wizard guides manual edits to your existing DNS, tailnet policy, and Traefik configuration. It does not replace those configurations. Its closing summary lists live checks that you have not confirmed.

External setup references: [Tailscale grants syntax](https://tailscale.com/docs/reference/syntax/grants), [Traefik TLS stores](https://doc.traefik.io/traefik/https/tls/), and [Hetzner API tokens](https://docs.hetzner.com/cloud/api/getting-started/generating-api-token/).

## Network policy

Bind the control API directly to the gateway's Tailscale IP. Do not put a public reverse proxy in front of it. The gateway identifies the TCP peer through the local tailscaled Unix socket and requires the `devrp.liuf.uk/publish` capability in the returned peer capability map. HTTP identity headers are never trusted.

Merge grants like these into the existing tailnet policy, replacing the example tags with tags assigned to the operator's devices. Remove any broader rules that would also allow access to the forwarding range. Tailscale grants are additive.

```json
{
  "grants": [
    {
      "src": ["tag:devrp-developer"],
      "dst": ["tag:devrp-gateway"],
      "ip": ["tcp:8080"],
      "app": {"devrp.liuf.uk/publish": [{}]}
    },
    {
      "src": ["tag:devrp-gateway"],
      "dst": ["tag:devrp-developer"],
      "ip": ["tcp:20000-29999"]
    }
  ]
}
```

Only the gateway may reach development-machine forwarding ports 20000 through 29999. Applications receive traffic on loopback and must trust the gateway's forwarding metadata only through this chain. Grant the developer account permission to run foreground Tailscale Serve using the existing tailscaled operator configuration.

## Traefik

Use Traefik v3 with a watched dynamic configuration directory. devrp owns only `devrp.yml` in that directory. Do not run two gateway instances against the same file. Keep the existing certificate resolver and DNS challenge credentials on the gateway.

On the public HTTPS entrypoint, keep `forwardedHeaders.insecure` and `proxyProtocol.insecure` false. Leave their trusted IP lists empty when visitors connect directly. If a real upstream proxy exists, trust only its addresses and require it to sanitize incoming metadata. Do not configure an entrypoint-wide certificate resolver that infers certificate names from ordinary devrp routers.

Provision `*.devrp.liuf.uk` DNS centrally, pointing to the public VPS IP. Provision a shared certificate covering `devrp.liuf.uk` and `*.devrp.liuf.uk` through Traefik, for example in a separate operator-owned dynamic file:

```yaml
tls:
  stores:
    default:
      defaultGeneratedCert:
        resolver: hetzner
        domain:
          main: devrp.liuf.uk
          sans:
            - '*.devrp.liuf.uk'
```

Ordinary routes enable TLS without a certificate resolver, so they use the shared certificate. Wildcard routes request their own publication hostname and child wildcard through the configured resolver. The gateway creates both address records and retains them on shutdown. Conflicting address or alias records require operator correction and are never overwritten.

Applications own their authentication, allowed-host checks, redirects, cookies, and trust-proxy settings. Public visitors and webhooks need no gateway login. devrp does not rewrite response bodies.

## Live acceptance check

Automated substitutes cannot verify the deployed tailnet policy or public certificate provisioning. Before deployment, use authorized disposable names to check the following:

- An authorized device can publish, an unauthorized device cannot, and another device cannot reach a forwarding listener.
- Two foreground Serve publications coexist with unrelated Serve configuration. Stopping one removes only its listener.
- Ordinary startup works with Hetzner unavailable and the shared certificate already present.
- Wildcard startup creates or reuses both address records, resolves publicly, and negotiates valid TLS for the base hostname and a child hostname.
- The application sees the public Host, HTTPS scheme, and visitor address. Forged forwarding headers do not replace them.
- WebSocket messages and incremental streaming pass through.
- Ctrl-C removes routing, child exit removes routing, and an independent application survives sharing shutdown.
- Gateway restart and sleep recover sharing. A name acquired by another session during an outage produces a conflict without killing the original application.

See the [Tailscale Serve reference](https://tailscale.com/docs/reference/tailscale-cli/serve), [Tailscale Serve implementation](https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_v2.go), [Hetzner SDK](https://github.com/hetznercloud/hcloud-go), and [Traefik entrypoint configuration](https://doc.traefik.io/traefik/v3.0/routing/entrypoints/) for the external interfaces used here.
