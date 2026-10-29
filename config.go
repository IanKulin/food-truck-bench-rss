package main

import (
	"fmt"
	"net/netip"
	"net/url"
	"os"
	"strconv"
	"strings"
	"time"
)

const (
	fetchTimeout    = 20 * time.Second
	articleDelay    = 1 * time.Second
	maxBodyBytes    = 5 << 20
	minPollInterval = time.Minute
	feedTitle       = "FoodTruck Bench Blog"
	feedSubtitle    = "Unofficial full-text feed of the FoodTruck Bench blog"
)

type Config struct {
	PublicURL    string
	SourceURL    string
	Origin       string
	PollInterval time.Duration
	DataFile     string
	ListenAddr   string

	RateLimit      int // requests per minute per client address; 0 disables
	TrustedProxies []netip.Prefix
}

func loadConfig(getenv func(string) string) (Config, error) {
	var c Config
	env := func(k, def string) string {
		if v := strings.TrimSpace(getenv(k)); v != "" {
			return v
		}
		return def
	}

	c.PublicURL = strings.TrimRight(env("PUBLIC_URL", ""), "/")
	if c.PublicURL == "" {
		return c, fmt.Errorf("PUBLIC_URL is required")
	}
	if err := checkAbsURL("PUBLIC_URL", c.PublicURL); err != nil {
		return c, err
	}

	c.SourceURL = env("SOURCE_URL", "https://foodtruckbench.com/blog")
	if err := checkAbsURL("SOURCE_URL", c.SourceURL); err != nil {
		return c, err
	}
	u, _ := url.Parse(c.SourceURL)
	c.Origin = u.Scheme + "://" + u.Host

	d, err := time.ParseDuration(env("POLL_INTERVAL", "3h"))
	if err != nil {
		return c, fmt.Errorf("POLL_INTERVAL: %w", err)
	}
	if d < minPollInterval {
		return c, fmt.Errorf("POLL_INTERVAL must be at least %s", minPollInterval)
	}
	c.PollInterval = d

	c.DataFile = env("DATA_FILE", "data/foodtruckbench.json")
	c.ListenAddr = env("LISTEN_ADDR", ":8080")

	c.RateLimit, err = strconv.Atoi(env("RATE_LIMIT", "60"))
	if err != nil || c.RateLimit < 0 {
		return c, fmt.Errorf("RATE_LIMIT must be a non-negative integer (requests/minute, 0 disables)")
	}
	c.TrustedProxies, err = parsePrefixes(env("TRUSTED_PROXIES", defaultTrustedProxies))
	if err != nil {
		return c, fmt.Errorf("TRUSTED_PROXIES: %w", err)
	}
	return c, nil
}

func checkAbsURL(name, s string) error {
	u, err := url.Parse(s)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" {
		return fmt.Errorf("%s must be an absolute http(s) URL, got %q", name, s)
	}
	return nil
}

func getenvOS(k string) string { return os.Getenv(k) }
