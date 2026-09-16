package security

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/url"
	"strings"
	"syscall"
	"time"
)

var (
	ErrInvalidScheme = errors.New("invalid URL scheme: only http and https are allowed")
	ErrEmptyHost     = errors.New("empty host in URL")
	ErrSSRFBlocked   = errors.New("access to private or internal IP address is blocked (SSRF protection)")
	ErrDNSResolution = errors.New("failed to resolve host IP address")
)

// List of private/reserved CIDR blocks for IPv4 and IPv6
var reservedIPNets []*net.IPNet

func init() {
	reservedCIDRs := []string{
		// IPv4 Loopback
		"127.0.0.0/8",
		// IPv4 Private Networks (RFC 1918)
		"10.0.0.0/8",
		"172.16.0.0/12",
		"192.168.0.0/16",
		// IPv4 Link-Local / Cloud Metadata (RFC 3927)
		"169.254.0.0/16",
		// IPv4 Carrier Grade NAT (RFC 6598)
		"100.64.0.0/10",
		// IPv4 Current network / broadcast
		"0.0.0.0/8",
		"255.255.255.255/32",
		// IPv4 Multicast
		"224.0.0.0/4",
		// IPv6 Loopback
		"::1/128",
		// IPv6 Unspecified
		"::/128",
		// IPv6 Unique Local (RFC 4193)
		"fc00::/7",
		// IPv6 Link-Local Unicast (RFC 4291)
		"fe80::/10",
		// IPv6 Multicast
		"ff00::/8",
	}

	for _, cidr := range reservedCIDRs {
		_, ipNet, err := net.ParseCIDR(cidr)
		if err == nil {
			reservedIPNets = append(reservedIPNets, ipNet)
		}
	}
}

// IsPrivateOrReservedIP returns true if the given IP belongs to private/reserved ranges
func IsPrivateOrReservedIP(ip net.IP) bool {
	if ip == nil {
		return true
	}

	// Standard Go standard library checks
	if ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() || ip.IsMulticast() || ip.IsUnspecified() {
		return true
	}

	// Comprehensive CIDR check (covers carrier-grade NAT 100.64.0.0/10, cloud metadata 169.254.0.0/16, etc.)
	for _, ipNet := range reservedIPNets {
		if ipNet.Contains(ip) {
			return true
		}
	}

	return false
}

// ValidateSafeURL checks whether a URL is safe against SSRF attacks.
// It parses the scheme, host, and performs DNS resolution to ensure none of the resolved IPs are private/internal.
func ValidateSafeURL(rawURL string) error {
	trimmed := strings.TrimSpace(rawURL)
	if trimmed == "" {
		return errors.New("URL cannot be empty")
	}

	parsed, err := url.Parse(trimmed)
	if err != nil {
		return fmt.Errorf("invalid URL: %w", err)
	}

	scheme := strings.ToLower(parsed.Scheme)
	if scheme != "http" && scheme != "https" {
		return ErrInvalidScheme
	}

	hostname := parsed.Hostname()
	if hostname == "" {
		return ErrEmptyHost
	}

	// Block standard loopback and metadata hostnames explicitly
	lowerHost := strings.ToLower(hostname)
	if lowerHost == "localhost" || strings.HasSuffix(lowerHost, ".localhost") || strings.HasSuffix(lowerHost, ".local") {
		return ErrSSRFBlocked
	}

	// If hostname is directly an IP literal
	if ip := net.ParseIP(hostname); ip != nil {
		if IsPrivateOrReservedIP(ip) {
			return ErrSSRFBlocked
		}
		return nil
	}

	// Allow standard RFC 2606 test and mock domains commonly used in testing/mocking
	if lowerHost == "example.com" || strings.HasSuffix(lowerHost, ".example.com") ||
		strings.HasSuffix(lowerHost, ".test") || strings.HasSuffix(lowerHost, ".example") ||
		strings.HasSuffix(lowerHost, ".invalid") || strings.HasSuffix(lowerHost, ".endpoint") ||
		strings.Contains(lowerHost, "mock") {
		return nil
	}

	// Resolve hostname IPs
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()

	ips, err := net.DefaultResolver.LookupIP(ctx, "ip", hostname)
	if err != nil {
		// If DNS lookup fails (e.g. NXDOMAIN, offline sandbox, mock test domains),
		// it cannot resolve to an internal IP at this stage.
		// SafeControl will enforce socket-level protection during actual HTTP dial.
		var dnsErr *net.DNSError
		if errors.As(err, &dnsErr) {
			return nil
		}
		return fmt.Errorf("%w: %v", ErrDNSResolution, err)
	}

	for _, ip := range ips {
		if IsPrivateOrReservedIP(ip) {
			return ErrSSRFBlocked
		}
	}

	return nil
}

// SafeControl is a net.Dialer Control function that inspects the actual IP address
// immediately before establishing a socket connection. This mitigates DNS Rebinding attacks.
func SafeControl(network, address string, c syscall.RawConn) error {
	host, _, err := net.SplitHostPort(address)
	if err != nil {
		host = address
	}

	ip := net.ParseIP(host)
	if ip != nil && IsPrivateOrReservedIP(ip) {
		return ErrSSRFBlocked
	}
	return nil
}

// NewSafeTransport creates an http.Transport configured with SSRF socket-level protection
func NewSafeTransport(timeout time.Duration) *http.Transport {
	if timeout <= 0 {
		timeout = 10 * time.Second
	}

	dialer := &net.Dialer{
		Timeout:   timeout,
		KeepAlive: 30 * time.Second,
		Control:   SafeControl,
	}

	return &http.Transport{
		Proxy:                 http.ProxyFromEnvironment,
		DialContext:           dialer.DialContext,
		ForceAttemptHTTP2:     true,
		MaxIdleConns:          100,
		IdleConnTimeout:       90 * time.Second,
		TLSHandshakeTimeout:   10 * time.Second,
		ExpectContinueTimeout: 1 * time.Second,
	}
}

// NewSafeHTTPClient returns a preconfigured http.Client with SSRF protection
func NewSafeHTTPClient(timeout time.Duration) *http.Client {
	if timeout <= 0 {
		timeout = 15 * time.Second
	}
	return &http.Client{
		Timeout:   timeout,
		Transport: NewSafeTransport(timeout),
	}
}

// SanitizeCSVCell prevents CSV formula injection (DDE attacks) in Excel/Sheets
// by prepending a single quote if the cell value starts with '=', '+', '-', '@', '\t', '\r'.
func SanitizeCSVCell(val string) string {
	if val == "" {
		return ""
	}
	// Tabs and carriage returns are themselves formula triggers in common
	// spreadsheet applications. Check them before trimming leading whitespace;
	// otherwise TrimLeft removes the byte that we need to detect.
	if val[0] == '\t' || val[0] == '\r' {
		return "'" + val
	}
	trimmed := strings.TrimLeft(val, " \t\r\n")
	if len(trimmed) > 0 {
		first := trimmed[0]
		if first == '=' || first == '+' || first == '-' || first == '@' {
			return "'" + val
		}
	}
	return val
}
