package store

import (
	"time"

	"farmail/model"
)

const activeDomainsCacheTTL = 5 * time.Second

type activeDomainsCacheEntry struct {
	domains   []model.Domain
	expiresAt time.Time
}

type activeDomainsLoadResult struct {
	domains []model.Domain
	epoch   uint64
}

func (s *Store) getCachedActiveDomains() ([]model.Domain, bool) {
	now := time.Now()
	s.activeDomainsMu.RLock()
	entry := s.activeDomainsCache
	s.activeDomainsMu.RUnlock()
	if entry.domains == nil {
		s.domainCacheMisses.Add(1)
		return nil, false
	}
	if !now.Before(entry.expiresAt) {
		s.domainCacheMisses.Add(1)
		s.activeDomainsMu.Lock()
		if s.activeDomainsCache.domains != nil && !now.Before(s.activeDomainsCache.expiresAt) {
			s.activeDomainsCache = activeDomainsCacheEntry{}
		}
		s.activeDomainsMu.Unlock()
		return nil, false
	}
	s.domainCacheHits.Add(1)
	return cloneDomains(entry.domains), true
}

func (s *Store) cacheActiveDomains(domains []model.Domain) {
	s.activeDomainsMu.Lock()
	s.activeDomainsCache = activeDomainsCacheEntry{
		domains:   cloneDomains(domains),
		expiresAt: time.Now().Add(activeDomainsCacheTTL),
	}
	s.activeDomainsMu.Unlock()
}

func (s *Store) invalidateActiveDomainsCache() {
	s.activeDomainsEpoch.Add(1)
	s.activeDomainsMu.Lock()
	s.activeDomainsCache = activeDomainsCacheEntry{}
	s.activeDomainsMu.Unlock()
	s.activeDomainsLoadGroup.Forget("active")
}

func cloneDomains(domains []model.Domain) []model.Domain {
	cloned := make([]model.Domain, len(domains))
	for index := range domains {
		cloned[index] = cloneDomain(domains[index])
	}
	return cloned
}

func cloneDomain(domain model.Domain) model.Domain {
	cloned := domain
	if domain.VerifiedAt != nil {
		verifiedAt := *domain.VerifiedAt
		cloned.VerifiedAt = &verifiedAt
	}
	if domain.MxCheckedAt != nil {
		mxCheckedAt := *domain.MxCheckedAt
		cloned.MxCheckedAt = &mxCheckedAt
	}
	return cloned
}
