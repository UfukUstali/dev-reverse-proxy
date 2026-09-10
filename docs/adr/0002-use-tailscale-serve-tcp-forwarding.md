# Use Tailscale Serve TCP forwarding

Use a foreground Tailscale Serve TCP forwarder for each publication, carrying HTTP from Traefik to the local application through the machine's existing Tailscale connection. Tailscale Serve's HTTP proxy replaces forwarding metadata, losing Traefik's original visitor address and HTTPS scheme; TCP forwarding preserves that metadata and avoids maintaining a forwarding implementation in devrp. The CLI owns the Serve subprocess lifetime, while Traefik remains responsible for public TLS and trusted forwarding headers.
