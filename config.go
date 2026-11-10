package main

import (
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
)

type TenantConfig struct {
	ID            string `json:"id"`
	DemoKey       string `json:"demo_key"`
	PartnerSecret string `json:"partner_secret"`
	ServiceAPIKey string `json:"service_api_key"`
}

type Config struct {
	ListenAddr    string
	GameAPIBase   string
	WebBase       string
	ServiceAPIKey string
	DemoTokenTTL  time.Duration
	PartnerJWTTTL time.Duration
	Tenants       map[string]TenantConfig
}

func loadConfig() (*Config, error) {
	cfg := &Config{
		ListenAddr:    envOr("LISTEN_ADDR", ":18088"),
		GameAPIBase:   strings.TrimRight(envOr("GAME_API_BASE", "http://127.0.0.1:4000"), "/"),
		WebBase:       strings.TrimRight(envOr("WEB_BASE", "http://127.0.0.1:5173"), "/"),
		ServiceAPIKey: os.Getenv("SERVICE_API_KEY"),
		Tenants:       map[string]TenantConfig{},
	}
	var err error
	cfg.DemoTokenTTL, err = time.ParseDuration(envOr("DEMO_TOKEN_TTL", "1h"))
	if err != nil {
		return nil, fmt.Errorf("DEMO_TOKEN_TTL: %w", err)
	}
	cfg.PartnerJWTTTL, err = time.ParseDuration(envOr("PARTNER_JWT_TTL", "5m"))
	if err != nil {
		return nil, fmt.Errorf("PARTNER_JWT_TTL: %w", err)
	}

	if raw := strings.TrimSpace(os.Getenv("TENANTS_JSON")); raw != "" {
		var list []TenantConfig
		if err := json.Unmarshal([]byte(raw), &list); err != nil {
			return nil, fmt.Errorf("TENANTS_JSON: %w", err)
		}
		for _, t := range list {
			id := strings.TrimSpace(t.ID)
			if id == "" || t.DemoKey == "" || t.PartnerSecret == "" {
				continue
			}
			t.ID = id
			cfg.Tenants[id] = t
		}
	}

	// TENANT_<ID>_DEMO_KEY + PARTNER_<ID>_SECRET (+ optional SERVICE_API_KEY_<ID>)
	for _, e := range os.Environ() {
		const prefix = "TENANT_"
		const suffix = "_DEMO_KEY"
		if !strings.HasPrefix(e, prefix) || !strings.Contains(e, "=") {
			continue
		}
		kv := strings.SplitN(e, "=", 2)
		key, val := kv[0], kv[1]
		if !strings.HasSuffix(key, suffix) || val == "" {
			continue
		}
		envID := strings.TrimSuffix(strings.TrimPrefix(key, prefix), suffix)
		// ACME → acme; FOO_BAR → foo-bar (matches PARTNER_<ENV>_SECRET / partner_id)
		id := strings.ToLower(strings.ReplaceAll(envID, "_", "-"))
		secret := os.Getenv("PARTNER_" + envID + "_SECRET")
		if secret == "" {
			continue
		}
		svc := os.Getenv("SERVICE_API_KEY_" + envID)
		existing, ok := cfg.Tenants[id]
		if !ok {
			existing = TenantConfig{ID: id}
		}
		existing.DemoKey = val
		existing.PartnerSecret = secret
		if svc != "" {
			existing.ServiceAPIKey = svc
		}
		cfg.Tenants[id] = existing
	}

	if len(cfg.Tenants) == 0 {
		return nil, fmt.Errorf("no tenants configured (set TENANT_<ID>_DEMO_KEY + PARTNER_<ID>_SECRET, or TENANTS_JSON)")
	}
	if cfg.ServiceAPIKey == "" {
		for _, t := range cfg.Tenants {
			if t.ServiceAPIKey != "" {
				cfg.ServiceAPIKey = t.ServiceAPIKey
				break
			}
		}
	}
	if cfg.ServiceAPIKey == "" {
		return nil, fmt.Errorf("SERVICE_API_KEY (or per-tenant SERVICE_API_KEY_<ID>) required for credit/debit")
	}
	return cfg, nil
}

func (c *Config) tenantServiceKey(t TenantConfig) string {
	if t.ServiceAPIKey != "" {
		return t.ServiceAPIKey
	}
	return c.ServiceAPIKey
}

func envOr(k, def string) string {
	if v := strings.TrimSpace(os.Getenv(k)); v != "" {
		return v
	}
	return def
}
