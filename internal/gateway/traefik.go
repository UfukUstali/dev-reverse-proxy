package gateway

import (
	"fmt"
	"net"
	"regexp"
	"strconv"
	"time"

	"gopkg.in/yaml.v3"
)

func (g *Gateway) writeConfig() error {
	routers, services := map[string]any{}, map[string]any{}
	for id, p := range g.publications {
		if !time.Now().Before(p.Expires) {
			continue
		}
		hostname := id + "." + g.cfg.Domain
		rule := "Host(`" + hostname + "`)"
		tls := map[string]any{}
		if p.Wildcard {
			rule += " || HostRegexp(`^[a-z0-9]([a-z0-9-]{0,61}[a-z0-9])?\\." + regexp.QuoteMeta(hostname) + "$`)"
			tls["certResolver"] = g.cfg.Resolver
			tls["domains"] = []any{map[string]any{"main": hostname, "sans": []string{"*." + hostname}}}
		}
		name := "devrp-" + id
		routers[name] = map[string]any{"entryPoints": []string{g.cfg.EntryPoint}, "rule": rule, "service": name, "tls": tls, "middlewares": []string{"devrp-forwarding"}}
		services[name] = map[string]any{"loadBalancer": map[string]any{"passHostHeader": true, "servers": []any{map[string]any{"url": "http://" + net.JoinHostPort(p.IP, strconv.Itoa(p.Port))}}, "responseForwarding": map[string]any{"flushInterval": "-1ms"}}}
	}
	// Traefik sanitizes X-Forwarded-* at the entrypoint. Remove alternate metadata
	// that applications might otherwise prefer over Traefik's trusted headers.
	headers := map[string]string{"Forwarded": "", "X-Real-IP": "", "X-Forwarded-Server": ""}
	cfg := map[string]any{"http": map[string]any{"routers": routers, "services": services, "middlewares": map[string]any{"devrp-forwarding": map[string]any{"headers": map[string]any{"customRequestHeaders": headers}}}}}
	if len(routers) == 0 {
		httpConfig := cfg["http"].(map[string]any)
		delete(httpConfig, "routers")
		delete(httpConfig, "services")
	}
	data, err := yaml.Marshal(cfg)
	if err != nil {
		return fmt.Errorf("encode routes: %w", err)
	}
	return replaceFile(g.cfg.ConfigDir, data)
}
