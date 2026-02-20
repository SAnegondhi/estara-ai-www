package geolocation

import (
	"fmt"
	"net"

	"github.com/oschwald/geoip2-golang"
)

// LocationResult represents the result of an IP geolocation lookup
type LocationResult struct {
	City      string  `json:"city"`
	State     string  `json:"state"`      // ISO code (e.g., "CA")
	Country   string  `json:"country"`    // ISO code (e.g., "US")
	Latitude  float64 `json:"latitude"`
	Longitude float64 `json:"longitude"`
	IsValid   bool    `json:"isValid"`
}

// MaxMindService provides IP geolocation using MaxMind GeoLite2 database
type MaxMindService struct {
	cityReader *geoip2.Reader
}

// NewMaxMindService creates a new MaxMind geolocation service
// dbPath should point to GeoLite2-City.mmdb file
func NewMaxMindService(dbPath string) (*MaxMindService, error) {
	reader, err := geoip2.Open(dbPath)
	if err != nil {
		return nil, fmt.Errorf("failed to open MaxMind database: %w", err)
	}
	return &MaxMindService{cityReader: reader}, nil
}

// LookupIP performs geolocation lookup for the given IP address
// Returns LocationResult with IsValid=false if IP is invalid or private
func (s *MaxMindService) LookupIP(ipStr string) (*LocationResult, error) {
	ip := net.ParseIP(ipStr)
	if ip == nil {
		return &LocationResult{IsValid: false}, nil
	}

	// Check if IP is private (localhost, LAN, etc.)
	if isPrivateIP(ip) {
		return &LocationResult{IsValid: false}, nil
	}

	record, err := s.cityReader.City(ip)
	if err != nil {
		// Return invalid result instead of error for missing/unknown IPs
		return &LocationResult{IsValid: false}, nil
	}

	// Extract state code (first subdivision)
	stateCode := ""
	if len(record.Subdivisions) > 0 {
		stateCode = record.Subdivisions[0].IsoCode
	}

	return &LocationResult{
		City:      record.City.Names["en"],
		State:     stateCode,
		Country:   record.Country.IsoCode,
		Latitude:  record.Location.Latitude,
		Longitude: record.Location.Longitude,
		IsValid:   true,
	}, nil
}

// Close releases the MaxMind database reader
func (s *MaxMindService) Close() error {
	if s.cityReader != nil {
		return s.cityReader.Close()
	}
	return nil
}

// isPrivateIP checks if the IP is in a private range
func isPrivateIP(ip net.IP) bool {
	// Check for loopback
	if ip.IsLoopback() {
		return true
	}

	// Check for link-local
	if ip.IsLinkLocalUnicast() || ip.IsLinkLocalMulticast() {
		return true
	}

	// Check for private IPv4 ranges
	if ip4 := ip.To4(); ip4 != nil {
		// 10.0.0.0/8
		if ip4[0] == 10 {
			return true
		}
		// 172.16.0.0/12
		if ip4[0] == 172 && ip4[1] >= 16 && ip4[1] <= 31 {
			return true
		}
		// 192.168.0.0/16
		if ip4[0] == 192 && ip4[1] == 168 {
			return true
		}
	}

	return false
}
