package middleware

import (
	"strings"
	"testing"
	"time"
)

func TestFixedWindowKeySeparatesPathsAndWindowsWithoutLeakingCredential(t *testing.T) {
	credential := "Bearer 0123456789abcdef0123456789abcdef"
	now := time.Unix(120, 0)
	a := fixedWindowKey("/public/v1/domains/submit", credential, now, 60)
	b := fixedWindowKey("/public/v1/domains/1/status", credential, now, 60)
	c := fixedWindowKey("/public/v1/domains/submit", credential, now.Add(60*time.Second), 60)
	if a == b || a == c {
		t.Fatal("rate-limit keys must be isolated by endpoint and fixed window")
	}
	if strings.Contains(a, credential) {
		t.Fatal("rate-limit key must not contain the raw credential")
	}
}
