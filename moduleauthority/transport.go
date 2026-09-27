package moduleauthority

import (
	"fmt"
	"net"
	"net/url"
	"strings"
)

// The transport rule, in one place: the internal token and the module secret
// travel only over TLS, or in plaintext to a loopback address, or in plaintext
// to any address the consumer explicitly asserted protected
// (Seams.AllowInsecureHTTP). Loopback is the literal name "localhost" or a
// loopback IP; nothing is inferred from any other name.

// gatewayBaseURL validates the gateway's base URL and returns it without a
// trailing slash.
func gatewayBaseURL(raw string, allowInsecureHTTP bool) (string, error) {
	if raw == "" {
		return "", fmt.Errorf("%w: the gateway base URL is required", ErrInvalidSeams)
	}
	parsed, err := url.Parse(raw)
	if err != nil || parsed.Host == "" || parsed.Hostname() == "" {
		return "", fmt.Errorf("%w: the gateway base URL %q is not an absolute URL", ErrInvalidSeams, raw)
	}
	if parsed.User != nil || parsed.RawQuery != "" || parsed.Fragment != "" {
		return "", fmt.Errorf("%w: the gateway base URL %q carries userinfo, a query or a fragment", ErrInvalidSeams, raw)
	}
	switch parsed.Scheme {
	case "https":
	case "http":
		if !isLoopback(parsed.Hostname()) && !allowInsecureHTTP {
			return "", fmt.Errorf("%w: the gateway base URL %q is plaintext to a non-loopback host; the broker is sent the internal token and the module secret, so use https, or assert the hop is mesh-protected with AllowInsecureHTTP", ErrInvalidSeams, raw)
		}
	default:
		return "", fmt.Errorf("%w: the gateway base URL %q must be http or https", ErrInvalidSeams, raw)
	}
	return strings.TrimRight(parsed.String(), "/"), nil
}

// authorityBaseURL validates the authority endpoint's host:port and returns
// the base URL the gRPC client dials: https when a TLS configuration is given,
// http (h2c) otherwise.
func authorityBaseURL(authority Authority, allowInsecureHTTP bool) (string, error) {
	if authority.Address == "" {
		return "", fmt.Errorf("%w: the authority endpoint address is required", ErrInvalidSeams)
	}
	host, port, err := net.SplitHostPort(authority.Address)
	if err != nil || host == "" || port == "" || strings.Contains(authority.Address, "/") {
		return "", fmt.Errorf("%w: the authority endpoint address %q must be host:port, with no scheme", ErrInvalidSeams, authority.Address)
	}
	hostPort := net.JoinHostPort(host, port)
	if authority.TLS != nil {
		return "https://" + hostPort, nil
	}
	if !isLoopback(host) && !allowInsecureHTTP {
		return "", fmt.Errorf("%w: the authority endpoint %q is plaintext (no TLS configuration) to a non-loopback host; it is sent the internal token, so dial it with TLS, or assert the hop is mesh-protected with AllowInsecureHTTP", ErrInvalidSeams, authority.Address)
	}
	return "http://" + hostPort, nil
}

func isLoopback(host string) bool {
	if host == "localhost" {
		return true
	}
	ip := net.ParseIP(host)
	return ip != nil && ip.IsLoopback()
}
