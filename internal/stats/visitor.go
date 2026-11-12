package stats

import (
	"crypto/hmac"
	"crypto/rand"
	"crypto/sha256"
	"fmt"
	"strings"
)

// SaltBytes is the size of a daily salt.
const SaltBytes = 32

// VisitorHashBytes is the size of a stored visitor hash.
const VisitorHashBytes = 16

// botMarkers are lower-case User-Agent substrings of crawlers, monitors and
// HTTP tools. The list is short on purpose: counts are approximate.
var botMarkers = []string{
	"bot", "crawl", "spider", "slurp", "archiver", "fetcher", "scanner",
	"curl/", "wget", "python", "go-http-client", "java/", "okhttp", "libwww",
	"httpclient", "axios", "node-fetch", "headless", "phantomjs", "lighthouse",
	"pagespeed", "preview", "monitor", "uptime", "pingdom", "facebookexternalhit",
	"whatsapp", "telegram", "embedly", "yandex.com/bots", "mediapartners",
}

// IsBot reports whether a User-Agent belongs to a robot or a tool. An empty
// User-Agent is treated as a robot.
func IsBot(ua string) bool {
	ua = strings.ToLower(strings.TrimSpace(ua))
	if ua == "" {
		return true
	}
	for _, m := range botMarkers {
		if strings.Contains(ua, m) {
			return true
		}
	}
	return false
}

// NewSalt returns a random daily salt.
func NewSalt() ([]byte, error) {
	b := make([]byte, SaltBytes)
	if _, err := rand.Read(b); err != nil {
		return nil, fmt.Errorf("stats: generate salt: %w", err)
	}
	return b, nil
}

// VisitorHash identifies a visitor within one day without storing the IP
// address or User-Agent: HMAC-SHA256 keyed with the day's salt, truncated.
// Once the salt is deleted the hash cannot be linked back to anyone.
func VisitorHash(salt []byte, ip, ua string) []byte {
	m := hmac.New(sha256.New, salt)
	m.Write([]byte(ip))
	m.Write([]byte{0})
	m.Write([]byte(ua))
	return m.Sum(nil)[:VisitorHashBytes]
}
