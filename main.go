package main

import (
	"fmt"
	"log"
	"net/http"
	"os"
	"sort"
	"strings"
)

func main() {
	loadDotEnv(".env")
	cfg, err := loadConfig()
	if err != nil {
		log.Fatalf("config: %v", err)
	}
	ids := make([]string, 0, len(cfg.Tenants))
	for id := range cfg.Tenants {
		ids = append(ids, id)
	}
	sort.Strings(ids)
	srv := &Server{
		cfg:   cfg,
		store: NewStore(),
		game:  NewGameClient(cfg.GameAPIBase),
	}
	log.Printf("partner-demo listening on %s", cfg.ListenAddr)
	log.Printf("GAME_API_BASE=%s WEB_BASE=%s tenants=%s", cfg.GameAPIBase, cfg.WebBase, strings.Join(ids, ","))
	if err := http.ListenAndServe(cfg.ListenAddr, srv.routes()); err != nil {
		log.Fatal(err)
	}
}

// loadDotEnv is a tiny optional .env loader (KEY=VALUE lines). No shell expansion.
func loadDotEnv(path string) {
	b, err := os.ReadFile(path)
	if err != nil {
		return
	}
	for _, line := range strings.Split(string(b), "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		k, v, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		k = strings.TrimSpace(k)
		v = strings.TrimSpace(v)
		v = strings.Trim(v, `"'`)
		if k == "" {
			continue
		}
		if _, exists := os.LookupEnv(k); !exists {
			_ = os.Setenv(k, v)
		}
	}
	fmt.Fprintf(os.Stderr, "loaded %s\n", path)
}
