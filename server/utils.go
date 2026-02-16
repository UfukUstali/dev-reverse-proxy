package main

import (
	"regexp"
	"strings"
)

var subdomainPartRegex = regexp.MustCompile(`^[a-zA-Z0-9]([a-zA-Z0-9-]*[a-zA-Z0-9])?$`)

func validateSubdomain(subdomain string) bool {
	parts := strings.Split(subdomain, ".")
	if len(parts) == 0 || len(subdomain) > 1500 {
		return false
	}
	for _, part := range parts {
		if len(part) == 0 || len(part) > 63 {
			return false
		}
		if !subdomainPartRegex.MatchString(part) {
			return false
		}
	}
	return true
}

func toInternalID(subdomain string) string {
	return strings.ReplaceAll(subdomain, ".", "_")
}
