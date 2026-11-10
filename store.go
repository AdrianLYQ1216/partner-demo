package main

import (
	"crypto/rand"
	"encoding/hex"
	"sync"
	"time"
)

type demoToken struct {
	TenantID  string
	ExpiresAt time.Time
}

type userRecord struct {
	TenantID    string
	ExternalUID string
	PlatformUID int64
	UpdatedAt   time.Time
}

type Store struct {
	mu     sync.RWMutex
	tokens map[string]demoToken
	users  map[string]userRecord // tenantID + "\x00" + externalUID
}

func NewStore() *Store {
	return &Store{
		tokens: map[string]demoToken{},
		users:  map[string]userRecord{},
	}
}

func userKey(tenantID, externalUID string) string {
	return tenantID + "\x00" + externalUID
}

func (s *Store) IssueToken(tenantID string, ttl time.Duration) (string, time.Time, error) {
	b := make([]byte, 24)
	if _, err := rand.Read(b); err != nil {
		return "", time.Time{}, err
	}
	tok := hex.EncodeToString(b)
	exp := time.Now().Add(ttl)
	s.mu.Lock()
	s.tokens[tok] = demoToken{TenantID: tenantID, ExpiresAt: exp}
	s.mu.Unlock()
	return tok, exp, nil
}

func (s *Store) ResolveToken(tok string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.tokens[tok]
	if !ok || time.Now().After(rec.ExpiresAt) {
		return "", false
	}
	return rec.TenantID, true
}

func (s *Store) PutUser(tenantID, externalUID string, platformUID int64) {
	s.mu.Lock()
	s.users[userKey(tenantID, externalUID)] = userRecord{
		TenantID:    tenantID,
		ExternalUID: externalUID,
		PlatformUID: platformUID,
		UpdatedAt:   time.Now(),
	}
	s.mu.Unlock()
}

func (s *Store) GetUser(tenantID, externalUID string) (userRecord, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	rec, ok := s.users[userKey(tenantID, externalUID)]
	return rec, ok
}
