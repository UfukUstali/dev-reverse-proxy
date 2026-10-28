package cli

import (
	"testing"

	"github.com/UfukUstali/dev-reverse-proxy/internal/publication"
)

func TestValidAliases(t *testing.T) {
	r := publication.Response{URL: "https://app.devrp.example.com", Domain: "devrp.example.com"}
	for _, aliases := range [][]string{nil, {}, {"https://tenant.devrp.example.com", "https://other.devrp.example.com"}} {
		if !validAliases(r, aliases) {
			t.Fatalf("rejected %q", aliases)
		}
	}
	for _, alias := range []string{
		"", "http://tenant.devrp.example.com", "https://tenant.devrp.example.com/", "https://tenant.devrp.example.com:443",
		"https://deep.tenant.devrp.example.com", "https://TENANT.devrp.example.com", "https://tenant.example.org",
		"https://devrp.example.com", "https://app.devrp.example.com", "https://ten\x1b[31mant.devrp.example.com",
	} {
		if validAliases(r, []string{alias}) {
			t.Fatalf("accepted %q", alias)
		}
	}
	if validAliases(r, []string{"https://tenant.devrp.example.com", "https://tenant.devrp.example.com"}) {
		t.Fatal("accepted duplicate alias")
	}
}
