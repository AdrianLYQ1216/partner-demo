package main

import (
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type Server struct {
	cfg   *Config
	store *Store
	game  *GameClient
}

func (s *Server) routes() http.Handler {
	mux := http.NewServeMux()
	mux.HandleFunc("GET /healthz", s.handleHealth)
	mux.HandleFunc("POST /v1/auth", s.handleAuth)
	mux.HandleFunc("POST /v1/users", s.withTenant(s.handleCreateUser))
	mux.HandleFunc("POST /v1/users/{external_uid}/credit", s.withTenant(s.handleCredit))
	mux.HandleFunc("POST /v1/users/{external_uid}/debit", s.withTenant(s.handleDebit))
	mux.HandleFunc("POST /v1/users/{external_uid}/launch", s.withTenant(s.handleLaunchA))
	mux.HandleFunc("POST /v1/users/{external_uid}/launch/session", s.withTenant(s.handleLaunchB))
	return mux
}

func (s *Server) handleHealth(w http.ResponseWriter, _ *http.Request) {
	writeJSON(w, http.StatusOK, map[string]any{
		"ok":       true,
		"tenants":  len(s.cfg.Tenants),
		"game_api": s.cfg.GameAPIBase,
		"web_base": s.cfg.WebBase,
	})
}

type authBody struct {
	TenantID string `json:"tenant_id"`
	APIKey   string `json:"api_key"`
}

func (s *Server) handleAuth(w http.ResponseWriter, r *http.Request) {
	var body authBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	body.TenantID = strings.TrimSpace(body.TenantID)
	t, ok := s.cfg.Tenants[body.TenantID]
	if !ok || body.APIKey == "" || body.APIKey != t.DemoKey {
		writeErr(w, http.StatusUnauthorized, "invalid tenant_id or api_key")
		return
	}
	tok, exp, err := s.store.IssueToken(t.ID, s.cfg.DemoTokenTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "token issue failed")
		return
	}
	writeJSON(w, http.StatusOK, map[string]any{
		"access_token": tok,
		"expires_in":   int(time.Until(exp).Seconds()),
		"token_type":   "Bearer",
		"tenant_id":    t.ID,
	})
}

type tenantCtx struct {
	Tenant TenantConfig
}

type handlerFunc func(w http.ResponseWriter, r *http.Request, tc tenantCtx)

func (s *Server) withTenant(next handlerFunc) http.HandlerFunc {
	return func(w http.ResponseWriter, r *http.Request) {
		auth := r.Header.Get("Authorization")
		const prefix = "Bearer "
		if !strings.HasPrefix(auth, prefix) {
			writeErr(w, http.StatusUnauthorized, "missing Bearer token")
			return
		}
		tenantID, ok := s.store.ResolveToken(strings.TrimSpace(auth[len(prefix):]))
		if !ok {
			writeErr(w, http.StatusUnauthorized, "invalid or expired access_token")
			return
		}
		t, ok := s.cfg.Tenants[tenantID]
		if !ok {
			writeErr(w, http.StatusUnauthorized, "unknown tenant")
			return
		}
		next(w, r, tenantCtx{Tenant: t})
	}
}

type createUserBody struct {
	ExternalUID string `json:"external_uid"`
	UserID      string `json:"user_id"` // alias
}

func (s *Server) handleCreateUser(w http.ResponseWriter, r *http.Request, tc tenantCtx) {
	var body createUserBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	ext := strings.TrimSpace(body.ExternalUID)
	if ext == "" {
		ext = strings.TrimSpace(body.UserID)
	}
	if ext == "" {
		writeErr(w, http.StatusBadRequest, "external_uid required")
		return
	}
	partnerJWT, err := mintPartnerJWT(tc.Tenant.PartnerSecret, tc.Tenant.ID, ext, s.cfg.PartnerJWTTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mint partner jwt failed")
		return
	}
	ex, err := s.game.Exchange(partnerJWT)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.store.PutUser(tc.Tenant.ID, ext, ex.PlatformUID)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":     tc.Tenant.ID,
		"external_uid":  ext,
		"platform_uid":  ex.PlatformUID,
		"partner_jwt":   partnerJWT,
		"session_token": ex.SessionToken,
	})
}

type moneyBody struct {
	Amount         int64  `json:"amount"`
	IdempotencyKey string `json:"idempotency_key"`
	Reason         string `json:"reason"`
}

func (s *Server) ensureUser(tc tenantCtx, externalUID string) (userRecord, error) {
	if rec, ok := s.store.GetUser(tc.Tenant.ID, externalUID); ok {
		return rec, nil
	}
	partnerJWT, err := mintPartnerJWT(tc.Tenant.PartnerSecret, tc.Tenant.ID, externalUID, s.cfg.PartnerJWTTTL)
	if err != nil {
		return userRecord{}, err
	}
	ex, err := s.game.Exchange(partnerJWT)
	if err != nil {
		return userRecord{}, err
	}
	s.store.PutUser(tc.Tenant.ID, externalUID, ex.PlatformUID)
	return userRecord{
		TenantID:    tc.Tenant.ID,
		ExternalUID: externalUID,
		PlatformUID: ex.PlatformUID,
	}, nil
}

func (s *Server) handleCredit(w http.ResponseWriter, r *http.Request, tc tenantCtx) {
	s.handleMoney(w, r, tc, true)
}

func (s *Server) handleDebit(w http.ResponseWriter, r *http.Request, tc tenantCtx) {
	s.handleMoney(w, r, tc, false)
}

func (s *Server) handleMoney(w http.ResponseWriter, r *http.Request, tc tenantCtx, credit bool) {
	ext := r.PathValue("external_uid")
	if ext == "" {
		writeErr(w, http.StatusBadRequest, "external_uid required")
		return
	}
	var body moneyBody
	if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
		writeErr(w, http.StatusBadRequest, "bad request")
		return
	}
	if body.Amount <= 0 {
		writeErr(w, http.StatusBadRequest, "amount must be > 0")
		return
	}
	rec, err := s.ensureUser(tc, ext)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	op := "credit"
	reason := "partner_bring_in"
	if !credit {
		op = "debit"
		reason = "partner_bring_out"
	}
	if body.Reason != "" {
		reason = body.Reason
	}
	idem := body.IdempotencyKey
	if idem == "" {
		idem = fmt.Sprintf("%s:%s:%s:%d", tc.Tenant.ID, op, ext, time.Now().UnixNano())
	}
	apiKey := s.cfg.tenantServiceKey(tc.Tenant)
	var gameResp map[string]any
	if credit {
		gameResp, err = s.game.Credit(apiKey, rec.PlatformUID, body.Amount, reason, idem)
	} else {
		gameResp, err = s.game.Debit(apiKey, rec.PlatformUID, body.Amount, reason, idem)
	}
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	chips := extractChips(gameResp)
	writeJSON(w, http.StatusOK, map[string]any{
		"tenant_id":        tc.Tenant.ID,
		"external_uid":     ext,
		"platform_uid":     rec.PlatformUID,
		"amount":           body.Amount,
		"operation":        op,
		"idempotency_key":  idem,
		"chips":            chips,
		"game_response":    gameResp,
	})
}

func (s *Server) handleLaunchA(w http.ResponseWriter, r *http.Request, tc tenantCtx) {
	ext := r.PathValue("external_uid")
	if ext == "" {
		writeErr(w, http.StatusBadRequest, "external_uid required")
		return
	}
	if _, err := s.ensureUser(tc, ext); err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	partnerJWT, err := mintPartnerJWT(tc.Tenant.PartnerSecret, tc.Tenant.ID, ext, s.cfg.PartnerJWTTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mint partner jwt failed")
		return
	}
	u := s.cfg.WebBase + "/partner/enter?token=" + url.QueryEscape(partnerJWT)
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":         "A",
		"url":          u,
		"tenant_id":    tc.Tenant.ID,
		"external_uid": ext,
		"note":         "Client exchanges Partner JWT on /partner/enter",
	})
}

func (s *Server) handleLaunchB(w http.ResponseWriter, r *http.Request, tc tenantCtx) {
	ext := r.PathValue("external_uid")
	if ext == "" {
		writeErr(w, http.StatusBadRequest, "external_uid required")
		return
	}
	partnerJWT, err := mintPartnerJWT(tc.Tenant.PartnerSecret, tc.Tenant.ID, ext, s.cfg.PartnerJWTTTL)
	if err != nil {
		writeErr(w, http.StatusInternalServerError, "mint partner jwt failed")
		return
	}
	ex, err := s.game.Exchange(partnerJWT)
	if err != nil {
		writeErr(w, http.StatusBadGateway, err.Error())
		return
	}
	s.store.PutUser(tc.Tenant.ID, ext, ex.PlatformUID)
	// Sandbox only: put session JWT in the URL. Document risk — token leakage via Referer/history.
	u := s.cfg.WebBase + "/partner/enter?session=" + url.QueryEscape(ex.SessionToken)
	writeJSON(w, http.StatusOK, map[string]any{
		"mode":         "B",
		"url":          u,
		"tenant_id":    tc.Tenant.ID,
		"external_uid": ext,
		"platform_uid": ex.PlatformUID,
		"warning":      "sandbox only: session JWT in query string can leak via Referer, logs, and browser history",
	})
}

func extractChips(gameResp map[string]any) any {
	if gameResp == nil {
		return nil
	}
	if data, ok := gameResp["data"].(map[string]any); ok {
		if c, ok := data["chips"]; ok {
			return c
		}
	}
	return nil
}

func writeJSON(w http.ResponseWriter, status int, v any) {
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(v)
}

func writeErr(w http.ResponseWriter, status int, msg string) {
	writeJSON(w, status, map[string]any{"error": msg, "status": status})
}
