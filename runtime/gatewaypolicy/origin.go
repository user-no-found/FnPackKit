package gatewaypolicy

import (
	"net"
	"net/url"
	"slices"
	"strconv"
	"strings"
)

func validHost(host string) bool {
	if host == "" || len(host) > 512 || strings.ContainsAny(host, " ,/\\@?#\t\r\n%") {
		return false
	}
	parsed, err := url.Parse("http://" + host)
	if err != nil || parsed.Host != host || parsed.Hostname() == "" {
		return false
	}
	if strings.Contains(host, ":") {
		if _, port, err := net.SplitHostPort(host); err == nil {
			value, err := strconv.ParseUint(port, 10, 16)
			return err == nil && value > 0
		}
		return strings.HasPrefix(host, "[") && strings.HasSuffix(host, "]") && net.ParseIP(parsed.Hostname()) != nil
	}
	return true
}

func parseOrigin(raw string) (*url.URL, bool) {
	origin, err := url.Parse(raw)
	if err != nil || len(raw) > 1024 || (origin.Scheme != "http" && origin.Scheme != "https") || origin.Opaque != "" || origin.User != nil || !validHost(origin.Host) || origin.Path != "" || origin.ForceQuery || origin.RawQuery != "" || strings.Contains(raw, "#") {
		return nil, false
	}
	return origin, true
}

func normalizedHostname(host string) string {
	if address := net.ParseIP(host); address != nil {
		return address.String()
	}
	return strings.TrimSuffix(strings.ToLower(host), ".")
}

// strictOrigin preserves the exact authority and configured stripped-port contract.
func strictOrigin(origin *url.URL, host string, ports map[string][]uint16) bool {
	if origin.Host == host {
		return true
	}
	forwarded, err := url.Parse("http://" + host)
	if err != nil || forwarded.Port() != "" || normalizedHostname(origin.Hostname()) != normalizedHostname(forwarded.Hostname()) {
		return false
	}
	text := origin.Port()
	port, err := strconv.ParseUint(text, 10, 16)
	if err != nil || port == 0 || strconv.FormatUint(port, 10) != text {
		return false
	}
	return slices.Contains(ports[origin.Scheme], uint16(port))
}
