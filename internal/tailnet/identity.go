// Package tailnet interfaces with the machine's existing tailscaled.
package tailnet

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"time"
)

type Identity struct{ ID, IP string }
type Identify func(context.Context, string, string) (Identity, error)

func IsAddress(ip netip.Addr) bool {
	return netip.MustParsePrefix("100.64.0.0/10").Contains(ip) || netip.MustParsePrefix("fd7a:115c:a1e0::/48").Contains(ip)
}

func LocalIdentity(socket string) Identify {
	client := &http.Client{Timeout: 5 * time.Second, Transport: &http.Transport{DialContext: func(ctx context.Context, _, _ string) (net.Conn, error) {
		return (&net.Dialer{}).DialContext(ctx, "unix", socket)
	}}}
	return func(ctx context.Context, remote, local string) (Identity, error) {
		peer, err := netip.ParseAddrPort(remote)
		if err != nil || !IsAddress(peer.Addr()) {
			return Identity{}, errors.New("control connections must originate on Tailscale")
		}
		dst, err := netip.ParseAddrPort(local)
		if err != nil || !IsAddress(dst.Addr()) {
			return Identity{}, errors.New("control API must listen on its Tailscale address")
		}
		q := url.Values{"addr": {remote}, "dst_ip": {dst.Addr().String()}, "proto": {"tcp"}}
		req, _ := http.NewRequestWithContext(ctx, "GET", "http://local-tailscaled.sock/localapi/v0/whois?"+q.Encode(), nil)
		resp, err := client.Do(req)
		if err != nil {
			return Identity{}, fmt.Errorf("tailscaled identity: %w", err)
		}
		defer resp.Body.Close()
		if resp.StatusCode != 200 {
			return Identity{}, fmt.Errorf("tailscaled identity returned %s", resp.Status)
		}
		var who struct {
			Node struct {
				StableID  string
				Addresses []netip.Prefix
			}
			CapMap map[string]json.RawMessage
		}
		if err = json.NewDecoder(resp.Body).Decode(&who); err != nil {
			return Identity{}, err
		}
		if _, ok := who.CapMap["devrp.liuf.uk/publish"]; !ok {
			return Identity{}, errors.New("missing devrp.liuf.uk/publish grant")
		}
		if who.Node.StableID == "" {
			return Identity{}, errors.New("missing stable machine identity")
		}
		for _, addr := range who.Node.Addresses {
			if addr.Addr() == peer.Addr() {
				return Identity{ID: who.Node.StableID, IP: peer.Addr().String()}, nil
			}
		}
		return Identity{}, errors.New("peer address does not belong to authenticated machine")
	}
}
