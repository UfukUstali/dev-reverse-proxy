# Public development service sharing

## Problem Statement

The operator runs development services on several Linux machines already connected to Tailscale. The current proxy only exposes services through local `.localhost` names and assumes that applications run on the same host as its Traefik deployment. It cannot give an external visitor or webhook sender a public URL for a service on another development machine.

The operator wants public sharing similar to ngrok or Cloudflare Tunnel while keeping the familiar devrp CLI. Developer machines should not need new credentials or node enrollment. The operator already has a public VPS, Traefik with DNS challenge certificate provisioning, and Hetzner DNS.

## Solution

Publish local services at HTTPS URLs beneath `devrp.liuf.uk` through the existing Traefik gateway. Use existing Tailscale connections for both configuration calls and service traffic between the VPS and development machines. A supervised foreground Tailscale Serve TCP forwarder carries HTTP from the gateway to each loopback-bound application, preserving Traefik's forwarding headers.

Ordinary publications use the existing `*.devrp.liuf.uk` DNS record and certificate without calling Hetzner. Wildcard publications additionally provision their own address records through the gateway. Traefik remains the sole certificate manager. Public sharing replaces local-only mode.

## User Stories

1. As an operator, I want to publish services through my existing public VPS, so that I can use infrastructure I already maintain.
2. As an operator, I want to retain my existing Traefik deployment, so that routing and certificates remain centrally managed.
3. As a developer, I want the CLI to reuse my machine's existing Tailscale connection, so that I do not configure new keys or enroll another node.
4. As an operator, I want configuration publishing to travel over Tailscale, so that the configuration API is available through the private network.
5. As an operator, I want service requests and responses between the gateway and development machines to travel over Tailscale, so that this traffic is encrypted.
6. As a developer, I want to launch an application through the familiar devrp command, so that public sharing fits my existing workflow.
7. As a developer, I want to publish an already-running local port without a child command, so that I do not have to restart my application.
8. As a developer, I want loopback-bound applications to work, so that I do not change their bind addresses.
9. As a developer, I want automatic or explicit application-port selection in launch mode, so that I can use applications with different startup requirements.
10. As a developer, I want the selected port, publication ID, and public base URL passed to a launched application, so that it can configure itself correctly.
11. As a developer, I want to configure the gateway through the existing server flags or environment variable, so that I can keep familiar invocation patterns.
12. As a developer, I want a clear error when the gateway address is missing, so that I know how to configure public sharing.
13. As a developer, I want the gateway to supply the public base domain, so that I do not configure the domain separately on each machine.
14. As a developer, I want a chosen publication name to produce a predictable HTTPS hostname, so that visitors and integrations can reuse it.
15. As a developer, I want the existing default name `myapp` to remain available, so that omitting the ID continues to work.
16. As an operator, I want publication names restricted to lowercase single DNS labels, so that hostname ownership is unambiguous.
17. As a developer, I want a clear conflict when another active publication owns my requested name, so that the CLI does not silently replace it or choose a different URL.
18. As an operator, I want only my authorized devices to publish, so that tailnet membership alone does not grant uncontrolled publishing access.
19. As an operator, I want a device to publish only its own local services, so that it cannot register routes to another machine.
20. As an operator, I want heartbeats and removal tied to publication ownership, so that another caller cannot modify a publication merely by knowing its name.
21. As an operator, I want only the gateway to reach the forwarding listeners, so that applications can trust the configured proxy chain.
22. As a visitor, I want to open a public HTTPS URL without joining the tailnet, so that I can access a shared service normally.
23. As a webhook sender, I want to reach a publication without gateway login, so that automated integrations work.
24. As a developer, I want ordinary publications to use shared wildcard DNS and certificates, so that startup does not depend on Hetzner API calls.
25. As a developer, I want wildcard mode to serve both my publication hostname and one label beneath it, so that tenant subdomains reach the same local application.
26. As an operator, I want the gateway to provision DNS only for wildcard publications, so that developer machines need no DNS credentials.
27. As an operator, I want matching DNS records reused and conflicts reported, so that publishing does not overwrite unrelated configuration.
28. As an operator, I want publication DNS records retained between sessions, so that repeated sharing avoids unnecessary DNS changes.
29. As a developer, I want an ordinary publication to reuse records left by earlier wildcard use without contacting Hetzner, so that the fast path remains available.
30. As an operator, I want Traefik to acquire and renew every public certificate, so that devrp does not introduce another certificate manager.
31. As a developer, I want WebSockets and streaming responses to pass through, so that development tools and streaming applications work.
32. As an application developer, I want the original public Host, HTTPS scheme, and visitor address preserved through trusted forwarding metadata, so that application behavior reflects the public request.
33. As an application developer, I want to retain responsibility for authentication, allowed hosts, redirects, and cookies, so that the gateway does not rewrite application behavior.
34. As a developer, I want URL assignment distinguished from readiness, so that I know whether the application can actually be reached over public HTTPS.
35. As a developer, I want slow startup to report its pending condition after sixty seconds and keep retrying, so that a slow application or certificate operation does not force a restart.
36. As a developer, I want configuration and naming errors to fail before my child application starts, so that a failed publication does not unexpectedly launch it.
37. As a developer, I want Ctrl-C to stop sharing and stop an application launched by devrp, so that one foreground command owns the workflow.
38. As a developer, I want stopping publication of an independently started application to leave that application running, so that sharing does not take ownership of its lifetime.
39. As a developer, I want normal child exit to end its publication, so that the gateway does not keep routing to an application that has finished.
40. As a developer, I want temporary network or gateway failures to leave my application running, so that an interruption in sharing does not interrupt development.
41. As a developer, I want a running CLI to reconnect after sleep or a gateway restart, so that sharing recovers automatically when possible.
42. As a developer, I want an abandoned publication name freed after thirty seconds without a heartbeat, so that another machine can use it.
43. As a developer, I want a reconnect conflict to stop the publication attempt while leaving my application running, so that recovery does not displace another active publication.
44. As a developer, I want several devrp processes to coexist with unrelated Tailscale Serve configuration, so that starting or stopping one publication does not disrupt another.
45. As an operator, I want existing Traefik error responses used for unavailable services, so that devrp needs no custom public error pages.

## Implementation Decisions

- Support Linux development machines and a Linux gateway. Replace the local-only proxy purpose while preserving the familiar launch command, server, ID, port, and wildcard options, and applicable environment variables. Add a mode with no child command that publishes a selected existing port.
- Keep application-port selection distinct from forwarding-port selection. The application receives its selected port; the gateway receives the forwarding port on the development machine's Tailscale address.
- Keep the existing Go CLI and Go registration service roles. Extend the CLI to manage the local application, foreground Serve subprocess, registration, heartbeat, recovery, and readiness. Extend the registration service to authorize publications, manage their lifecycle, provision wildcard DNS, and generate Traefik configuration.
- The gateway address comes from the existing server flags or `SERVER`. There is no local default fallback. The gateway supplies the public base domain and publication URL. Launch mode supplies `PORT`, `DEVRP_ID`, and `DEVRP_BASE_URL` to the child, with the latter containing the public HTTPS URL.
- IDs are global lowercase single DNS labels. Preserve `myapp` as the default. Reject dotted IDs and competing active claims. A wildcard publication owns its own hostname and the next label beneath it.
- Retain the registration, heartbeat, unregister, status, and publication-list roles of the existing control API. Registration must identify the publication name, wildcard intent, and forwarding port. Derive the development-machine identity and route destination from the authenticated tailnet connection rather than an arbitrary supplied host. Responses provide the public URL or actionable failure information.
- Extend publication state to include its owning development machine and active publication session, forwarding destination, wildcard intent, and heartbeat expiry. Scope heartbeat and removal to the owning active publication. Delayed calls from an old session must not refresh or remove a later publication using the same name. Session identifiers are not manually configured credentials; exact wire representation is an implementation choice.
- Use Tailscale identity and policy for publishing authorization. Restrict the control API to the tailnet and forwarding-listener access to the gateway. Existing machine enrollment supplies network identity; no application token or extra node enrollment is required on development machines.
- Use one supervised foreground Tailscale Serve TCP forwarder per publication. The listener port is the available port advertised to the gateway; its target is the loopback application port. Do not use Serve's HTTP proxy, persistent background mode, or global reset for publication cleanup.
- TCP forwarding carries HTTP unchanged. Traefik terminates public HTTPS and supplies forwarding metadata. Configure the proxy trust chain so visitor-supplied forwarding headers cannot impersonate trusted metadata. Applications remain responsible for their own trust-proxy configuration and application behavior.
- Preserve other Serve sessions. Handle occupied forwarding ports and concurrent Serve configuration updates without replacing existing listeners. Cleanup stops and waits for the owned Serve subprocess; reconnection supervises its recovery as well as registration.
- Run the gateway beside existing Traefik and write a dedicated dynamic-configuration file into its watched directory. Use the existing HTTPS entrypoint and certificate resolver. Generated routes target the authenticated development machine's tailnet address and forwarding port. Route removal, rather than DNS deletion, ends publication.
- Provision the shared wildcard DNS record and certificate centrally as infrastructure setup. Ordinary publications never call Hetzner, including when using retained records from previous wildcard publications. Traefik must use the shared certificate for this fast path rather than requesting a separate certificate for every ordinary publication.
- For wildcard publications, use Hetzner's official Go SDK for the current Cloud DNS API. Configure DNS credentials, zone, and public gateway IP on the gateway. Ensure address records for the publication hostname and its child wildcard both point to the VPS. Reuse matching records, fail on conflicts, and keep records between sessions. Do not overwrite unrelated records or record sets.
- The exact address record accompanying a child wildcard is required by DNS semantics: creating a descendant makes the parent name exist and prevents fallback to the higher wildcard. Certificate-validation records may have the same effect. Wildcard TLS coverage is one label, so Traefik must provide coverage for the publication hostname and its child wildcard.
- Traefik exclusively manages certificate acquisition and renewal through its existing DNS challenge setup. DNS records always point to the gateway's public IP, regardless of which machine owns the publication.
- Configuration errors, active-name conflicts, and conflicting DNS records fail before child launch. Once registration succeeds, launch the child and check readiness. Distinguish URL assignment from readiness, which requires local port acceptance, DNS resolution to the gateway, and successful public TLS negotiation. Wildcard readiness must account for the child hostname coverage as well as the publication hostname. Do not require an application-specific health endpoint.
- After sixty seconds without readiness, report the pending condition and continue retrying until cancellation. Do not terminate a running application because of temporary gateway, certificate-readiness, or network delays.
- Publications expire after thirty seconds without a heartbeat. A still-running CLI re-registers after interruption, sleep, or gateway restart, but must not displace another publication. On a reconnect name conflict, stop that publication attempt with a clear error and leave the application running.
- Publication lifetime follows the foreground CLI. Normal child exit ends publication. Ctrl-C removes the publication and stops a launched child, while an independently started application remains running. Gateway outages do not stop the application. Cleanup must not leave active routing that can be revived by an old publication session.
- Use default Traefik responses for unknown publications and unavailable local services. Do not add custom public error pages or response-body rewriting.
- Choose a released Hetzner SDK and compatible Go toolchain during implementation. Exact dependency versions, configuration field names, and internal module boundaries remain implementation choices.

## Testing Decisions

The following testing approach is proposed for implementation. The conversation confirmed the required behaviors, but did not separately approve a test architecture.

- Prefer one process-level integration test boundary: invoke the CLI, run the gateway and Traefik, and observe publication behavior through HTTP requests, CLI output, child-process lifetime, and external configuration effects. Exercise the CLI, publication lifecycle, generated routing, DNS integration, and local forwarding together where practical.
- A good test proves behavior visible to a developer, operator, or visitor. Assert routing, authorization, headers, readiness, cleanup, and recovery. Do not assert private structs, exact generated YAML formatting, helper call sequences, or the number of internal functions.
- Use controlled substitutes at external boundaries for ordinary automated runs: a local Hetzner-compatible API fixture and a controllable Tailscale identity and Serve process boundary. Avoid requiring production DNS credentials or modifying an existing tailnet for routine tests. Retain real CLI and gateway behavior inside the test boundary. Add narrowly scoped boundary tests only where the process-level suite cannot expose a failure reliably.
- The repository currently has no test suite or established test fixtures. Existing control API endpoints and executable CLI behavior are the available test boundaries. Use Go's standard testing and HTTP test facilities unless implementation identifies a concrete need for another dependency.
- Verify launch and existing-port modes, application environment, explicit and selected ports, missing gateway configuration, single-label validation, default naming, public URL output, and configuration failure before child launch.
- Verify rejection of unauthorized publishers, destinations on another machine, simultaneous name claims, cross-owner heartbeat and removal, and stale session calls after expiry or re-registration. Verify spoofed forwarding headers do not replace Traefik's trusted request metadata.
- Verify ordinary startup makes zero Hetzner API calls, including during simulated Hetzner outage and after earlier wildcard use. Verify wildcard creation of both required address records, reuse, conflicts, retained records, and external API failure without corrupting other publications or DNS records.
- Verify HTTP routing to the correct development machine, public Host, original HTTPS scheme and visitor address, WebSocket exchange, incremental streaming, wildcard routing, and use of existing Traefik error handling.
- Verify child exit, Ctrl-C in both CLI modes, thirty-second expiry, gateway restart, network interruption, reconnect conflict, and multiple simultaneous publications. Ensure application survival during sharing outages and isolated Serve cleanup. Use controllable external timing or test configuration to make lifecycle tests deterministic without testing private implementation details.
- Verify readiness remains pending for an unavailable application, incorrect DNS, or missing certificate coverage; verify the sixty-second diagnostic and eventual recovery. Cover the ordinary certificate fast path and child-wildcard certificate coverage separately.
- Before deployment, perform a live integration check using the selected Tailscale version, actual Traefik configuration, and authorized test DNS names. Verify foreground Serve cleanup, real tailnet identity and access policy, DNS propagation, certificate acquisition, headers, WebSockets, streaming, and reconnect behavior. Substituted external boundaries cannot prove these live integrations.

## Out of Scope

- Preserving local-only `.localhost` proxying.
- macOS and Windows support in the initial implementation.
- Publishing arbitrary raw TCP or UDP services to public visitors. TCP forwarding is an internal transport for the supported HTTP applications.
- HTTPS between the forwarding target and the local application.
- New Tailscale enrollment, an embedded independent tailnet node, or developer-managed application and DNS credentials.
- A separate tunnel protocol, replacement certificate manager, or replacement for the operator's existing Traefik.
- Multi-user publishing beyond the operator's authorized devices, gateway login for visitors, and application authentication management.
- Permanent publication-name reservations, silent takeover of active names, automatic suffixes, or ownership of arbitrary nested publication IDs.
- Detached persistent sharing after the CLI exits, per-session DNS deletion, and a new DNS cleanup administration interface.
- Application-specific health endpoints, response-body rewriting, automatic application host/redirect/cookie fixes, and custom public error pages.
- High availability, multiple public gateways, registrar changes, and deployment to live infrastructure as part of writing this spec.

## Further Notes

This spec synthesizes the accepted interview decisions, including the final selection of Tailscale Serve TCP forwarding. It uses the project glossary and the ADRs for existing Tailscale connections and TCP forwarding. It is a local Markdown deliverable; no issue-tracker publication or implementation is included.

Deployment still needs the existing Traefik configuration, watched directory, HTTPS entrypoint and resolver names, gateway tailnet address and identity, authorization policy, public IP, and Hetzner zone configuration. Configure secrets directly on the gateway rather than recording them in this document.

The implementation should distinguish publication ownership from publication naming and distinguish the application port from the forwarding port. The process-level testing boundary is a proposal, not an additional user-approved architectural constraint. A failed reconnect that leaves a launched application running still requires the implementation to retain sensible foreground process supervision.

Supporting sources gathered during the design interview:

- [Tailscale Serve CLI reference](https://tailscale.com/docs/reference/tailscale-cli/serve) documents foreground serving and TCP forwarding.
- [Tailscale Serve CLI implementation](https://github.com/tailscale/tailscale/blob/main/cmd/tailscale/cli/serve_v2.go) provides foreground-session and configuration-conflict behavior.
- [Tailscale HTTP proxy implementation](https://github.com/tailscale/tailscale/blob/main/ipn/ipnlocal/serve.go) and [Go reverse proxy implementation](https://github.com/golang/go/blob/master/src/net/http/httputil/reverseproxy.go) explain the forwarding-metadata tradeoff behind selecting TCP mode.
- [Hetzner's official Go SDK](https://github.com/hetznercloud/hcloud-go) and [Cloud DNS API](https://docs.hetzner.cloud/reference/cloud#zone-rrsets) provide DNS integration support.
- [RFC 4592](https://www.rfc-editor.org/rfc/rfc4592.html#section-2.2) defines the DNS wildcard behavior behind the ordinary fast path and wildcard record pair.

These findings were checked against documentation and upstream source. The operator's live Tailscale, DNS, certificate provisioning, and existing Traefik deployment have not been tested. No prototype was produced.
