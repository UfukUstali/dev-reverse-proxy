# Debrief: Public service sharing design

**Status:** done    **Branch:** master @ e2d9035

## In one paragraph

The design interview defines how dev-reverse-proxy will publish local HTTP applications through existing Traefik on a public VPS. Existing Tailscale connections carry configuration calls and application traffic, and foreground Tailscale Serve TCP forwarding preserves Traefik's headers. Ordinary publications use shared wildcard DNS; wildcard publications use Hetzner's official Go client. The interview decisions are recorded and synthesized into a local Markdown spec at the operator's request. Runtime implementation has not begun.

## Changes

- Defined publication, wildcard publication, gateway, and related domain terms in `CONTEXT.md`. The design and ADRs use this vocabulary.
- Recorded the existing-tailnet dependency and credential rationale in `docs/adr/0001-use-existing-tailscale-connections.md`.
- Recorded TCP forwarding and its header-preservation rationale in `docs/adr/0002-use-tailscale-serve-tcp-forwarding.md`.
- Consolidated accepted answers A1 through A24 into `docs/public-sharing-design.md`, including lifecycle, CLI compatibility, DNS, Traefik integration, and verification requirements.
- Wrote `docs/public-sharing-spec.md` using the requested template, with 45 user stories, implementation requirements, proposed behavior-focused testing, and explicit scope boundaries. Linked it from the design document.
- Added this handoff in `docs/design-debrief.md` to distinguish design completion from implementation and deployment.

## Decisions

The operator accepted the VPS, existing Traefik, keyless developer CLI, Linux-only support, foreground publication lifetime, shared DNS fast path, and TCP Serve forwarding during the interview. The design contains no remaining interview choices. Deployment-specific addresses, policy, paths, and resolver names remain inputs. Normal child exit ending its publication and isolated Serve cleanup are implementation consequences of the agreed foreground lifetime.

## Verification

Repository inspection established the current CLI, registration lifecycle, and Traefik file integration. Official Hetzner documentation and Tailscale source informed the integration choices, with links in the design. Markdown whitespace, final newlines, local document links, required spec sections, and numbered user stories were checked with Python. No application code changed, so application tests were not run. Live Tailscale behavior, DNS, certificates, and the existing Traefik deployment remain unverified.

## What follows

New work, not entered in an issue tracker: implement the Markdown spec when requested, obtain deployment inputs, and run the integration checks listed in the design before deployment.

## Spec impact

Replace the old `.localhost` purpose with public HTTPS sharing. Preserve the familiar flags and launch workflow, add existing-port publication, and restrict IDs to single DNS labels. Certificates stay in Traefik. Developer machines require no new credentials. Default Traefik error handling remains in place.

## Needs your decision

None for the requested Markdown spec. Its test architecture is explicitly proposed, and deployment inputs remain outstanding for implementation and deployment.
