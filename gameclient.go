package main

import (
	"bytes"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"time"

	"github.com/golang-jwt/jwt/v5"
)

type partnerClaims struct {
	PartnerID string `json:"partnerId"`
	UserID    string `json:"userId"`
	jwt.RegisteredClaims
}

type GameClient struct {
	base   string
	client *http.Client
}

func NewGameClient(base string) *GameClient {
	return &GameClient{
		base: base,
		client: &http.Client{
			Timeout: 15 * time.Second,
		},
	}
}

func mintPartnerJWT(secret, partnerID, externalUID string, ttl time.Duration) (string, error) {
	now := time.Now()
	claims := partnerClaims{
		PartnerID: partnerID,
		UserID:    externalUID,
		RegisteredClaims: jwt.RegisteredClaims{
			IssuedAt:  jwt.NewNumericDate(now),
			ExpiresAt: jwt.NewNumericDate(now.Add(ttl)),
		},
	}
	t := jwt.NewWithClaims(jwt.SigningMethodHS256, claims)
	return t.SignedString([]byte(secret))
}

type exchangeResult struct {
	SessionToken string
	PlatformUID  int64
	Raw          map[string]any
}

func (g *GameClient) Exchange(partnerJWT string) (*exchangeResult, error) {
	body, err := g.postJSON("/api/v1/partner/exchange", map[string]any{"token": partnerJWT}, nil)
	if err != nil {
		return nil, err
	}
	data, _ := body["data"].(map[string]any)
	if data == nil {
		return nil, fmt.Errorf("exchange: missing data: %v", body)
	}
	token, _ := data["token"].(string)
	user, _ := data["user"].(map[string]any)
	detail := map[string]any{}
	if user != nil {
		if d, ok := user["user_detail"].(map[string]any); ok {
			detail = d
		} else if d, ok := user["userDetail"].(map[string]any); ok {
			detail = d
		} else {
			detail = user
		}
	}
	uid := int64From(detail["id"])
	if uid == 0 {
		uid = int64From(detail["Id"])
	}
	if token == "" || uid == 0 {
		return nil, fmt.Errorf("exchange: incomplete response token=%q uid=%d body=%v", token, uid, body)
	}
	return &exchangeResult{SessionToken: token, PlatformUID: uid, Raw: body}, nil
}

func (g *GameClient) Credit(apiKey string, userID, amount int64, reason, idem string) (map[string]any, error) {
	return g.postJSON("/api/v1/ledger/credit", map[string]any{
		"user_id":          userID,
		"amount":           amount,
		"reason":           reason,
		"idempotency_key":  idem,
	}, map[string]string{"X-API-Key": apiKey})
}

func (g *GameClient) Debit(apiKey string, userID, amount int64, reason, idem string) (map[string]any, error) {
	return g.postJSON("/api/v1/ledger/debit", map[string]any{
		"user_id":          userID,
		"amount":           amount,
		"reason":           reason,
		"idempotency_key":  idem,
	}, map[string]string{"X-API-Key": apiKey})
}

func (g *GameClient) postJSON(path string, payload any, headers map[string]string) (map[string]any, error) {
	b, err := json.Marshal(payload)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequest(http.MethodPost, g.base+path, bytes.NewReader(b))
	if err != nil {
		return nil, err
	}
	req.Header.Set("Content-Type", "application/json")
	for k, v := range headers {
		req.Header.Set(k, v)
	}
	resp, err := g.client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(raw, &out); err != nil {
		return nil, fmt.Errorf("game %s: non-json (%d): %s", path, resp.StatusCode, truncate(string(raw), 200))
	}
	if resp.StatusCode >= 400 {
		msg, _ := out["msg"].(string)
		if msg == "" {
			msg, _ = out["error"].(string)
		}
		if msg == "" {
			msg = string(raw)
		}
		return out, fmt.Errorf("game %s: HTTP %d: %s", path, resp.StatusCode, msg)
	}
	return out, nil
}

func int64From(v any) int64 {
	switch n := v.(type) {
	case float64:
		return int64(n)
	case int64:
		return n
	case int:
		return int64(n)
	case json.Number:
		i, _ := n.Int64()
		return i
	case string:
		var i int64
		fmt.Sscan(n, &i)
		return i
	default:
		return 0
	}
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n] + "…"
}
