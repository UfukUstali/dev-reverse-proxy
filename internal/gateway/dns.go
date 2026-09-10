package gateway

import (
	"context"
	"fmt"
	"net/netip"
	"strings"

	"github.com/hetznercloud/hcloud-go/v2/hcloud"
)

// DNS provisions only the records needed by wildcard publications.
// Client may point to a Hetzner-compatible fixture for integration tests.
type dnsConfigError struct{ message string }

func (e *dnsConfigError) Error() string { return e.message }
func dnsConfiguration(format string, args ...any) error {
	return &dnsConfigError{message: fmt.Sprintf(format, args...)}
}

type DNS struct {
	Client *hcloud.Client
	Zone   string
}

func (d *DNS) ensure(ctx context.Context, hostname, ip string) error {
	if d == nil || d.Client == nil || d.Zone == "" {
		return dnsConfiguration("wildcard sharing requires HETZNER_TOKEN and HETZNER_ZONE on the gateway")
	}
	zone, _, err := d.Client.Zone.Get(ctx, d.Zone)
	if err != nil {
		return err
	}
	if zone == nil {
		return dnsConfiguration("Hetzner zone %q not found", d.Zone)
	}
	if !strings.HasSuffix(hostname, "."+zone.Name) {
		return dnsConfiguration("publication %s is outside zone %s", hostname, zone.Name)
	}
	name := strings.TrimSuffix(hostname, "."+zone.Name)
	names := []string{name, "*." + name}
	records, err := d.Client.Zone.AllRRSets(ctx, zone)
	if err != nil {
		return err
	}
	address := netip.MustParseAddr(ip)
	recordType := hcloud.ZoneRRSetTypeA
	if address.Is6() {
		recordType = hcloud.ZoneRRSetTypeAAAA
	}
	present := map[string]bool{}
	// Check both names before creating either. Never replace an existing RRset,
	// including address records for the other IP family or an alias/delegation.
	for _, rr := range records {
		if rr.Name != names[0] && rr.Name != names[1] {
			continue
		}
		switch rr.Type {
		case hcloud.ZoneRRSetTypeA, hcloud.ZoneRRSetTypeAAAA:
			if rr.Type != recordType || len(rr.Records) != 1 {
				return dnsConfiguration("DNS conflict at %s %s", rr.Name, rr.Type)
			}
			got, err := netip.ParseAddr(rr.Records[0].Value)
			if err != nil || got != address {
				return dnsConfiguration("DNS conflict at %s %s", rr.Name, rr.Type)
			}
			present[rr.Name] = true
		case hcloud.ZoneRRSetTypeCNAME, hcloud.ZoneRRSetTypeNS:
			return dnsConfiguration("DNS conflict at %s %s", rr.Name, rr.Type)
		}
	}
	for _, name := range names {
		if present[name] {
			continue
		}
		result, _, err := d.Client.Zone.CreateRRSet(ctx, zone, hcloud.ZoneRRSetCreateOpts{Name: name, Type: recordType, Records: []hcloud.ZoneRRSetRecord{{Value: ip}}})
		if err != nil {
			return fmt.Errorf("create DNS %s: %w", name, err)
		}
		if result.Action != nil {
			if err = d.Client.Action.WaitFor(ctx, result.Action); err != nil {
				return fmt.Errorf("wait for DNS %s: %w", name, err)
			}
		}
	}
	return nil
}
