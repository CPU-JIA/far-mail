package ingress

import (
	"fmt"
	"strings"
	"testing"

	"github.com/google/uuid"
)

func benchmarkDomainSnapshot(count int) *snapshot {
	domains := make([]snapshotDomain, 0, count)
	byName := make(map[string]snapshotDomain, count)
	for i := 0; i < count; i++ {
		name := fmt.Sprintf("d%05d.example.test", i)
		item := snapshotDomain{id: i + 1, domain: name}
		domains = append(domains, item)
		byName[name] = item
	}
	return &snapshot{
		domains:           domains,
		domainsByName:     byName,
		defaultAccountID:  uuid.MustParse("00000000-0000-0000-0000-000000000001"),
		mailboxTTLMinutes: 30,
	}
}

func BenchmarkResolveRecipientIndexed10K(b *testing.B) {
	snap := benchmarkDomainSnapshot(10_000)
	const address = "user@sub.d09999.example.test"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		if _, ok := snap.resolveRecipient(address); !ok {
			b.Fatal("recipient did not resolve")
		}
	}
}

// This benchmark keeps the former scan algorithm only as a measurement
// control. It is not used by the server.
func BenchmarkResolveRecipientLinear10K(b *testing.B) {
	snap := benchmarkDomainSnapshot(10_000)
	const address = "user@sub.d09999.example.test"
	b.ReportAllocs()
	for i := 0; i < b.N; i++ {
		_, domain, ok := splitAddress(address)
		if !ok {
			b.Fatal("invalid address")
		}
		matched := false
		for _, item := range snap.domains {
			if domain == item.domain || strings.HasSuffix(domain, "."+item.domain) {
				matched = true
			}
		}
		if !matched {
			b.Fatal("recipient did not resolve")
		}
	}
}
