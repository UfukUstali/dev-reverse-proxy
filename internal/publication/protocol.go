// Package publication defines the control API shared by the CLI and gateway.
package publication

import "regexp"

var label = regexp.MustCompile(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?$`)
var session = regexp.MustCompile(`^[a-f0-9]{64}$`)

func ValidName(s string) bool    { return label.MatchString(s) }
func ValidSession(s string) bool { return session.MatchString(s) }

const ForwardMin = 20000
const ForwardMax = 29999

type RegisterRequest struct {
	ID       string `json:"id"`
	Port     int    `json:"port"`
	Wildcard bool   `json:"wildcard"`
	Session  string `json:"session"`
}

type Response struct {
	Status   string `json:"status"`
	URL      string `json:"url,omitempty"`
	Domain   string `json:"domain,omitempty"`
	PublicIP string `json:"public_ip,omitempty"`
	Message  string `json:"message,omitempty"`
	// Aliases lists the Authcenter alias URLs currently routed to this
	// publication. Register and heartbeat responses include it.
	Aliases []string `json:"aliases,omitempty"`
}
