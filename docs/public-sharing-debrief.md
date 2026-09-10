# Debrief: Public development service sharing

**Status:** done    **Branch:** master @ d8123c3

The revision above is the tested implementation revision. This hand-back and the review report are committed afterward as documentation.

## In one paragraph

Public sharing replaces local `.localhost` routing. The CLI publishes a loopback application through an owned foreground Tailscale Serve TCP process. The gateway authorizes the connecting machine through tailscaled, leases a global publication name to its session, and writes HTTPS routes for the existing Traefik deployment. Ordinary sharing uses shared wildcard DNS and TLS without Hetzner calls. Wildcard sharing retains the required exact and child-wildcard DNS records. Applications survive sharing outages, while Ctrl-C and normal child exit clean up the foreground workflow. Automated checks pass. Live tailnet policy and public DNS/certificate provisioning remain checks to perform before deployment.

## Changes

Changes are listed in dependency order.

- The publication protocol defines lowercase names, forwarding ports, session identifiers, and gateway responses in `internal/publication/protocol.go`. The gateway and CLI use it to distinguish application ports, forwarding ports, publication names, and ownership.
- Tailnet identity and Serve supervision live in `internal/tailnet/identity.go` and `internal/tailnet/serve.go`. Identity comes from the TCP connection and tailscaled capability lookup. Each Serve process owns one foreground session. The CLI builds on this to publish loopback applications without new enrollment or developer credentials.
- Gateway lifecycle, DNS provisioning, and routing live in `internal/gateway/gateway.go`, `internal/gateway/dns.go`, and `internal/gateway/traefik.go`. Session retirement prevents stale calls from reviving publications. Atomic file replacement publishes route changes. Ordinary routes omit a certificate resolver. Wildcard routes request both certificate names and create or reuse both address records through the official SDK.
- `server/main.go` now requires a Tailscale bind address and runs the gateway beside the existing Traefik deployment. The obsolete dotted-name helpers in `server/utils.go` were removed. Gateway configuration errors fail startup, and an orderly shutdown clears routes.
- CLI configuration, API calls, application supervision, and readiness integration live in `client/devrp/main.go`, `internal/cli/control.go`, `internal/cli/run.go`, and `internal/cli/readiness.go`. The command supports launch and existing-port modes. Recovery retains the last valid assignment and resolves a previous lease before replacing a failed forwarder. A real name conflict stops sharing while preserving foreground supervision of the application.
- Public readiness checks live in `internal/readiness/readiness.go`. They require local TCP acceptance, DNS addresses matching the gateway, and valid public TLS for the publication and, when requested, a child hostname. A sixty-second diagnostic does not end the retry loop.
- Behavioral tests were added in `client/devrp/main_test.go`, `internal/tailnet/identity_test.go`, `internal/gateway/gateway_test.go`, `internal/gateway/dns_test.go`, `internal/gateway/traefik_test.go`, and `internal/readiness/readiness_test.go`. They exercise executable CLI behavior, gateway HTTP requests, a tailscaled Unix-socket fixture, a Hetzner-compatible API, and real Traefik traffic. CLI subprocesses are built with the race detector too.
- `go.mod` and `go.sum` pin Hetzner SDK v2.47.0 and require Go 1.25. `Dockerfile`, `.dockerignore`, and `docker-compose.yml` build and run only the gateway with the existing tailscaled socket and watched directory. The container uses Go 1.26.5 for its build.
- `README.md` documents commands, environment variables, lifecycle, the control API, migration, and test invocation. `docs/gateway-setup.md` documents grants, proxy trust, central DNS/TLS provisioning, and live checks. `docs/public-sharing-review.md` records the independent reviews and their corrections. This file records the implementation hand-back.

## Decisions

The supplied spec and ADRs confirm existing Tailscale enrollment, foreground Serve TCP transport, central Traefik TLS, retained wildcard DNS, thirty-second leases, and application survival during outages. The user approved executable CLI and gateway HTTP tests with controlled external substitutes, plus narrow external-boundary tests.

Implementation choices made during the work:

- Authorization requires the custom `devrp.liuf.uk/publish` Tailscale application capability. Changing its name requires updating the gateway lookup and policy example.
- The gateway binds directly to its tailnet IP and uses LocalAPI over the tailscaled Unix socket. Putting another HTTP proxy in front would require a different authenticated connection boundary.
- Forwarding listeners use ports 20000 through 29999. Changing the range requires updating the shared protocol and tailnet policy together.
- The CLI uses random 256-bit session identifiers. The gateway retains retired sessions in memory for its process lifetime. This favors rejection of delayed calls over bounded tombstone storage for this single-operator service.
- Heartbeats run every five seconds. The gateway expires leases after thirty seconds and checks route cleanup at least once per second.
- The gateway owns only `devrp.yml`, preserving other dynamic configuration files. An operator must remove the old devrp `dynamic.yml` during migration if it remains in the watched directory.
- Hetzner SDK v2.47.0 requires Go 1.25. The available Go 1.26.5 toolchain was used for local and container builds.

No domain-language change or new ADR was needed. These choices fit the two existing ADRs and are documented in the setup guide and README.

## Verification

Commands and results:

- `go build ./...` passed during implementation and after the review corrections.
- Focused `go test` commands covered gateway ownership, identity, DNS, CLI launch and cleanup, readiness, concurrent claims, and recovery. The failed-unregister recovery test failed before its correction and passed afterward.
- `go vet ./...` passed after correcting readiness context ownership.
- `TRAEFIK_BINARY=/tmp/devrp-upstream/traefik go test -race ./... -count=1` passed the complete suite. CLI tests completed in 41 seconds. The Traefik test ran against version 3.6.21.
- `docker build -t devrp-gateway:implementation-test .` passed after the code corrections. No container was deployed to live infrastructure.
- `GATEWAY_TAILSCALE_IP=100.64.0.1 GATEWAY_PUBLIC_IP=203.0.113.10 TRAEFIK_CONFIG_DIR=/tmp/devrp-config docker compose config --quiet` passed with example values.
- `git diff --check` passed.

The following maps all user stories to their evidence and verification limits.

| Spec stories | Evidence and limits |
| --- | --- |
| 1, 2, 13 | Gateway configuration and real Traefik routing test. The gateway returns the domain and HTTPS URL. Existing production Traefik configuration remains unverified. |
| 3, 4, 5, 18, 19, 20, 21 | Unix-socket identity test, HTTP ownership tests, strict rejection of supplied destinations, and setup policy. Real tailscaled grants, encrypted tailnet transport, and gateway-only listener access require the live check. |
| 6, 7, 8, 9, 10, 15 | Executable tests launch children with explicit and selected ports, assert explicit-port environment values and the default name, and publish an independent loopback application. Serve is an executable substitute in routine tests. |
| 11, 12, 16, 17 | CLI missing-gateway test, registration-rejection tests, name-validation tests, simultaneous-claim test, and documented flag/environment handling. Individual aliases and all environment combinations are not separately enumerated in tests. |
| 14, 22, 23 | Gateway URL assertions and real Traefik HTTPS traffic without visitor authentication. External internet reachability remains a deployment check. |
| 24, 29 | Ordinary startup makes no DNS requests, including during a simulated DNS outage and after retained wildcard use. Ordinary Traefik routes use a preloaded certificate without a router certificate resolver. Live shared-certificate provisioning remains unverified. |
| 25, 26, 27, 28 | Hetzner API fixture verifies exact and child-wildcard address records, reuse, conflicts, retained records, and API failures. Real Traefik serves one child label and rejects deeper nesting. Live DNS propagation remains unverified. |
| 30 | Generated wildcard routes name the existing resolver and both certificate domains. Real Traefik TLS tests use fixture certificates. Actual ACME acquisition and renewal remain unverified. |
| 31, 32, 33 | Real Traefik test verifies WebSocket exchange, incremental streaming, public Host, HTTPS scheme, visitor address, and removal of spoofed forwarding metadata. Application authentication, redirects, and cookies remain application responsibilities. |
| 34, 35 | Readiness tests reject unavailable applications, wrong DNS, and missing child-certificate coverage. A controlled deadline reports pending and then permits recovery. Production configuration uses sixty seconds. |
| 36 | Executable tests reject missing gateway, invalid registration, authorization failure, active-name conflict, DNS/configuration rejection, and gateway outage before launching the child. |
| 37, 38, 39 | Executable tests verify Ctrl-C in both modes, independent application survival, normal child exit, unregister, and owned Serve cleanup. |
| 40, 41, 43 | Executable protocol simulations verify outage recovery, gateway restart responses, failed forwarder cleanup, and a real competing-name response while the child stays alive. Actual machine sleep and gateway reboot remain live checks. |
| 42 | Shortened-lease HTTP tests verify name reuse and stale-session rejection. Real Traefik verifies lease-based route disappearance. Cleanup uses one captured expired-ID set; the disk-sync race is reviewed rather than deterministically fault-injected. |
| 44 | Executable tests run two publications, recover a failed Serve subprocess, and preserve unrelated persistent Serve configuration during individual cleanup. Actual daemon behavior remains a live check. |
| 45 | Real Traefik returns its normal 404 for unknown and removed publications. The implementation adds no public error page or response-body rewriting. |

The independent standards review found no documented-standard or code-smell violation. It also found the expiry correctness bug. The spec review found three bugs, including that expiry bug. Both reviewers confirmed the corrections and reported no remaining findings from their reviews. Their reports remain separate in `docs/public-sharing-review.md`.

## What follows

- New deployment work, not on an issue tracker. Configure the production bind address, watched directory, HTTPS entrypoint, resolver, public IP, zone, and gateway-only credentials.
- New deployment work, not on an issue tracker. Apply and verify tailnet grants, then run the live acceptance checklist in `docs/gateway-setup.md` with authorized test names.
- New migration work, not on an issue tracker. Stop the old local-only deployment and remove its generated `dynamic.yml` if it remains watched by Traefik.

## Spec impact

No spec text changed. The implementation selects the capability name, forwarding range, session representation, SDK version, Go requirement, and configuration variables left open by the spec. Implementation and automated verification are complete for the development scope. Deployment must continue to treat actual tailnet policy, public DNS propagation, and ACME acquisition as unverified until the live checklist passes.

## Needs your decision

None for the implementation. Live deployment values belong to the separate infrastructure setup.
