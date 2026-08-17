package store

import "time"

// PoolStats is safe to sample without acquiring a database connection. It is
// intentionally a snapshot rather than a health verdict; operators can see
// saturation and wait pressure while the normal health endpoint remains cheap.
type PoolStats struct {
	TotalConns           int32   `json:"total_conns"`
	AcquiredConns        int32   `json:"acquired_conns"`
	IdleConns            int32   `json:"idle_conns"`
	ConstructingConns    int32   `json:"constructing_conns"`
	MaxConns             int32   `json:"max_conns"`
	AcquireCount         int64   `json:"acquire_count"`
	CanceledAcquireCount int64   `json:"canceled_acquire_count"`
	EmptyAcquireCount    int64   `json:"empty_acquire_count"`
	AcquireDurationMS    float64 `json:"acquire_duration_ms"`
}

type CacheStats struct {
	TokenHits          uint64 `json:"token_hits"`
	TokenMisses        uint64 `json:"token_misses"`
	ActiveDomainHits   uint64 `json:"active_domain_hits"`
	ActiveDomainMisses uint64 `json:"active_domain_misses"`
}

func (s *Store) PoolStats() PoolStats {
	stat := s.pool.Stat()
	return PoolStats{
		TotalConns:           stat.TotalConns(),
		AcquiredConns:        stat.AcquiredConns(),
		IdleConns:            stat.IdleConns(),
		ConstructingConns:    stat.ConstructingConns(),
		MaxConns:             stat.MaxConns(),
		AcquireCount:         stat.AcquireCount(),
		CanceledAcquireCount: stat.CanceledAcquireCount(),
		EmptyAcquireCount:    stat.EmptyAcquireCount(),
		AcquireDurationMS:    float64(stat.AcquireDuration()) / float64(time.Millisecond),
	}
}

func (s *Store) CacheStats() CacheStats {
	return CacheStats{
		TokenHits:          s.tokenCacheHits.Load(),
		TokenMisses:        s.tokenCacheMisses.Load(),
		ActiveDomainHits:   s.domainCacheHits.Load(),
		ActiveDomainMisses: s.domainCacheMisses.Load(),
	}
}
