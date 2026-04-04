package netguard

import (
	"fmt"
	"net"
	"net/url"
	"os"
	"strconv"
	"strings"
)

func NormalizePublicHTTPURL(raw string) (string, error) {
	parsed, err := url.Parse(strings.TrimSpace(raw))
	if err != nil {
		return "", fmt.Errorf("invalid url: %w", err)
	}
	if err := ValidatePublicHTTPURL(parsed); err != nil {
		return "", err
	}
	return parsed.String(), nil
}

func ValidatePublicHTTPURL(parsed *url.URL) error {
	if parsed == nil {
		return fmt.Errorf("url is required")
	}
	if parsed.Scheme != "http" && parsed.Scheme != "https" {
		return fmt.Errorf("url must use http or https")
	}
	if parsed.User != nil {
		return fmt.Errorf("url credentials are not allowed")
	}
	host := strings.TrimSpace(parsed.Hostname())
	if host == "" {
		return fmt.Errorf("url host is required")
	}
	if err := validateHost(host); err != nil {
		return err
	}
	if port := strings.TrimSpace(parsed.Port()); port != "" {
		value, err := strconv.Atoi(port)
		if err != nil || value <= 0 || value > 65535 {
			return fmt.Errorf("url port is invalid")
		}
	}
	return nil
}

func validateHost(host string) error {
	if allowPrivateURLs() {
		return nil
	}
	lower := strings.ToLower(strings.TrimSpace(host))
	switch {
	case lower == "localhost":
		return fmt.Errorf("local network urls are not allowed")
	case strings.HasSuffix(lower, ".local"), strings.HasSuffix(lower, ".internal"), strings.HasSuffix(lower, ".localhost"):
		return fmt.Errorf("private hostnames are not allowed")
	}
	if ip := net.ParseIP(lower); ip != nil {
		if !isPublicIP(ip) {
			return fmt.Errorf("private or local network urls are not allowed")
		}
		return nil
	}
	addresses, err := net.LookupIP(lower)
	if err != nil {
		return fmt.Errorf("unable to resolve url host: %w", err)
	}
	if len(addresses) == 0 {
		return fmt.Errorf("url host did not resolve")
	}
	for _, ip := range addresses {
		if !isPublicIP(ip) {
			return fmt.Errorf("private or local network urls are not allowed")
		}
	}
	return nil
}

func isPublicIP(ip net.IP) bool {
	return !(ip.IsLoopback() || ip.IsPrivate() || ip.IsLinkLocalMulticast() || ip.IsLinkLocalUnicast() || ip.IsMulticast() || ip.IsUnspecified())
}

func allowPrivateURLs() bool {
	value := strings.ToLower(strings.TrimSpace(os.Getenv("OPENBID_ALLOW_PRIVATE_URLS")))
	return value == "1" || value == "true" || value == "yes" || value == "on"
}
