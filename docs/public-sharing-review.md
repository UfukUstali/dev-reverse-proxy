# Public sharing implementation review

The review compares the implementation with starting commit `6e39dd3d61630654cdb46f92a853eca39f706649`. Two independent agents reviewed standards and spec compliance. The spec source is `docs/public-sharing-spec.md`. Standards sources are `CONTEXT.md` and the two Tailscale ADRs. This repository has no separate coding standards or issue-tracker configuration.

## Standards

No hard breaches of the documented vocabulary or either ADR were found. The implementation uses existing Tailscale identity and foreground Serve TCP forwarding. No baseline code smell warranted a separate finding.

The reviewer reported one correctness issue. Expiry generated routing and then deleted publications using a later timestamp. A publication expiring during disk synchronization could disappear from memory while its route remained. The fix captures expired publication IDs before the write and deletes only those IDs afterward. Publications expiring during the write remain for the next pass.

The reviewer inspected the correction and confirmed the finding was resolved. Standards had zero documented breaches and zero smell findings. Its separate correctness finding is fixed.

## Spec

The reviewer found three correctness issues.

1. Expiry could leave an abandoned route indefinitely. This violated the thirty-second lease and cleanup requirements. Cleanup now deletes only the captured expired IDs.
2. When Serve exited and unregister failed, a new session could conflict with the original surviving lease. This violated automatic recovery after interruptions. Recovery now retries removal of the previous session before choosing a replacement port and session.
3. A failed registration during recovery could overwrite the previous assignment with an empty response. A subsequent successful heartbeat could then restart readiness with no URL. Registration now replaces the assignment only after success and validation.

The reviewer inspected all three corrections and reported no remaining findings from the review. The executable regression for failed unregister reproduced the self-conflict before the fix and recovered afterward. The real Traefik test verifies eventual lease-based route removal. It does not deterministically reproduce the disk-synchronization race, which is corrected by the captured-ID logic.

Spec had three findings, all fixed. No scope-creep finding was reported. Live tailnet policy, public DNS propagation, and certificate acquisition remain deployment checks documented in `gateway-setup.md`.
