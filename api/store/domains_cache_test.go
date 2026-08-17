package store

import (
	"testing"
	"time"

	"farmail/model"
)

func TestActiveDomainsCacheReturnsIndependentCopies(t *testing.T) {
	now := time.Now()
	verifiedAt := now.Add(-time.Hour)
	mxCheckedAt := now.Add(-time.Minute)
	s := &Store{}
	s.cacheActiveDomains([]model.Domain{{
		ID:          1,
		Domain:      "example.com",
		IsActive:    true,
		VerifiedAt:  &verifiedAt,
		MxCheckedAt: &mxCheckedAt,
	}})

	first, ok := s.getCachedActiveDomains()
	if !ok || len(first) != 1 {
		t.Fatal("expected active-domain cache hit")
	}
	first[0].Domain = "changed.example"
	*first[0].VerifiedAt = now
	*first[0].MxCheckedAt = now

	second, ok := s.getCachedActiveDomains()
	if !ok || len(second) != 1 {
		t.Fatal("expected second active-domain cache hit")
	}
	if second[0].Domain != "example.com" || !second[0].VerifiedAt.Equal(verifiedAt) || !second[0].MxCheckedAt.Equal(mxCheckedAt) {
		t.Fatal("active-domain cache returned shared mutable data")
	}
}

func TestActiveDomainsCacheExpiresAndInvalidates(t *testing.T) {
	s := &Store{}
	s.cacheActiveDomains([]model.Domain{{ID: 1, Domain: "example.com", IsActive: true}})
	s.activeDomainsMu.Lock()
	s.activeDomainsCache.expiresAt = time.Now().Add(-time.Second)
	s.activeDomainsMu.Unlock()
	if _, ok := s.getCachedActiveDomains(); ok {
		t.Fatal("expired active-domain cache entry must not be returned")
	}

	s.cacheActiveDomains([]model.Domain{{ID: 2, Domain: "example.net", IsActive: true}})
	epoch := s.activeDomainsEpoch.Load()
	s.invalidateActiveDomainsCache()
	if s.activeDomainsEpoch.Load() != epoch+1 {
		t.Fatal("active-domain cache invalidation did not advance the epoch")
	}
	if _, ok := s.getCachedActiveDomains(); ok {
		t.Fatal("invalidated active-domain cache entry remained available")
	}
}
