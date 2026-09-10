# Use existing Tailscale connections for public sharing

The operator's development machines and public gateway already belong to a Tailscale network. Use those existing connections for configuration publishing and service traffic, and Tailscale identity and policy to authorize publishing, so the CLI needs no separate credentials or node enrollment. This makes the system depend on the operator's tailnet and its access policy instead of introducing a separately authenticated tunnel service; visitors reach the gateway through public HTTPS.
