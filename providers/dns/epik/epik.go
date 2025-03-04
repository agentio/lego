// Package epik implements a DNS provider for solving the DNS-01 challenge using Epik.
package epik

import (
	"context"
	"errors"
	"fmt"
	"net/http"
	"strings"
	"time"

	"github.com/go-acme/lego/v4/challenge"
	"github.com/go-acme/lego/v4/challenge/dns01"
	"github.com/go-acme/lego/v4/platform/config/env"
	"github.com/go-acme/lego/v4/providers/dns/epik/internal"
)

// Environment variables names.
const (
	envNamespace = "EPIK_"

	EnvSignature = envNamespace + "SIGNATURE"

	EnvTTL                = envNamespace + "TTL"
	EnvPropagationTimeout = envNamespace + "PROPAGATION_TIMEOUT"
	EnvPollingInterval    = envNamespace + "POLLING_INTERVAL"
	EnvHTTPTimeout        = envNamespace + "HTTP_TIMEOUT"
)

var _ challenge.ProviderTimeout = (*DNSProvider)(nil)

// Config is used to configure the creation of the DNSProvider.
type Config struct {
	Signature          string
	PropagationTimeout time.Duration
	PollingInterval    time.Duration
	TTL                int
	HTTPClient         *http.Client
}

// NewDefaultConfig returns a default configuration for the DNSProvider.
func NewDefaultConfig() *Config {
	return &Config{
		TTL:                env.GetOrDefaultInt(EnvTTL, 3600),
		PropagationTimeout: env.GetOrDefaultSecond(EnvPropagationTimeout, dns01.DefaultPropagationTimeout),
		PollingInterval:    env.GetOrDefaultSecond(EnvPollingInterval, dns01.DefaultPollingInterval),
		HTTPClient: &http.Client{
			Timeout: env.GetOrDefaultSecond(EnvHTTPTimeout, 30*time.Second),
		},
	}
}

// DNSProvider implements the challenge.Provider interface.
type DNSProvider struct {
	config *Config
	client *internal.Client
}

// NewDNSProvider returns a DNSProvider instance configured for Epik.
// Credentials must be passed in the environment variable: EPIK_SIGNATURE.
func NewDNSProvider() (*DNSProvider, error) {
	values, err := env.Get(EnvSignature)
	if err != nil {
		return nil, fmt.Errorf("epik: %w", err)
	}

	config := NewDefaultConfig()
	config.Signature = values[EnvSignature]

	return NewDNSProviderConfig(config)
}

// NewDNSProviderConfig return a DNSProvider instance configured for Epik.
func NewDNSProviderConfig(config *Config) (*DNSProvider, error) {
	if config == nil {
		return nil, errors.New("epik: the configuration of the DNS provider is nil")
	}

	if config.Signature == "" {
		return nil, errors.New("epik: missing credentials")
	}

	client := internal.NewClient(config.Signature)

	if config.HTTPClient != nil {
		client.HTTPClient = config.HTTPClient
	}

	return &DNSProvider{config: config, client: client}, nil
}

// Timeout returns the timeout and interval to use when checking for DNS propagation.
// Adjusting here to cope with spikes in propagation times.
func (d *DNSProvider) Timeout() (timeout, interval time.Duration) {
	return d.config.PropagationTimeout, d.config.PollingInterval
}

// Present creates a TXT record using the specified parameters.
func (d *DNSProvider) Present(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)

	// find authZone
	authZone, err := dns01.FindZoneByFqdn(info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("epik: could not find zone for domain %q: %w", domain, err)
	}

	subDomain, err := dns01.ExtractSubDomain(info.EffectiveFQDN, authZone)
	if err != nil {
		return fmt.Errorf("epik: %w", err)
	}

	record := internal.RecordRequest{
		Host: subDomain,
		Type: "TXT",
		Data: info.Value,
		TTL:  d.config.TTL,
	}

	_, err = d.client.CreateHostRecord(context.Background(), dns01.UnFqdn(authZone), record)
	if err != nil {
		return fmt.Errorf("epik: %w", err)
	}

	return nil
}

// CleanUp removes the TXT record matching the specified parameters.
func (d *DNSProvider) CleanUp(domain, token, keyAuth string) error {
	info := dns01.GetChallengeInfo(domain, keyAuth)

	// find authZone
	authZone, err := dns01.FindZoneByFqdn(info.EffectiveFQDN)
	if err != nil {
		return fmt.Errorf("epik: could not find zone for domain %q: %w", domain, err)
	}

	dom := dns01.UnFqdn(authZone)

	ctx := context.Background()

	records, err := d.client.GetDNSRecords(ctx, dom)
	if err != nil {
		return fmt.Errorf("epik: %w", err)
	}

	subDomain, err := dns01.ExtractSubDomain(info.EffectiveFQDN, authZone)
	if err != nil {
		return fmt.Errorf("epik: %w", err)
	}

	for _, record := range records {
		if strings.EqualFold(record.Type, "TXT") && record.Data == info.Value && record.Name == subDomain {
			_, err = d.client.RemoveHostRecord(ctx, dom, record.ID)
			if err != nil {
				return fmt.Errorf("epik: %w", err)
			}
		}
	}

	return nil
}
