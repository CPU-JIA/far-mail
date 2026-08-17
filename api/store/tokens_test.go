package store

import (
	"fmt"
	"testing"
	"time"

	"farmail/model"

	"github.com/google/uuid"
)

func TestAdminCredentialAcceptsConfiguredLengths(t *testing.T) {
	for _, key := range []string{
		"sk-mail-0123456789abcdef",
		"sk-owner-console-0123456789abcdef0123456789abcdef",
	} {
		if !isAdminAuthKeyFormat(key) {
			t.Fatalf("expected admin key format to be accepted: %s", key)
		}
	}
}

func TestAPICredentialRequires32HexSecret(t *testing.T) {
	if !isAccessTokenFormat("0123456789abcdef0123456789abcdef") {
		t.Fatal("expected 32-hex API token to be accepted")
	}
	for _, invalid := range []string{
		"0123456789abcdef",
		"sk-api-0123456789abcdef0123456789abcdef",
		"0123456789ABCDEF0123456789ABCDEF",
	} {
		if isAccessTokenFormat(invalid) {
			t.Fatalf("API token format accepted invalid value: %s", invalid)
		}
	}
}

func TestCredentialFormatsStayDisjoint(t *testing.T) {
	adminKey := "sk-mail-0123456789abcdef0123456789abcdef"
	apiToken := "0123456789abcdef0123456789abcdef"
	if !isAdminAuthKeyFormat(adminKey) || isAccessTokenFormat(adminKey) {
		t.Fatal("Admin Key must only match the admin credential format")
	}
	if !isAccessTokenFormat(apiToken) || isAdminAuthKeyFormat(apiToken) {
		t.Fatal("API Token must only match the API credential format")
	}
}

func TestGenerateAccessTokenProduces32HexSecret(t *testing.T) {
	first := generateAccessToken()
	second := generateAccessToken()
	if !isAccessTokenFormat(first) || !isAccessTokenFormat(second) {
		t.Fatal("generated API Token does not match the 32-hex contract")
	}
	if first == second {
		t.Fatal("generated API Tokens must be unique")
	}
}

func TestGenerateAdminAuthKeyUsesConfiguredPrefixAndLength(t *testing.T) {
	short := generateAdminAuthKey("owner", 16)
	if len(short) != len("sk-owner-")+16 || !isAdminAuthKeyFormat(short) {
		t.Fatalf("unexpected short admin key: %s", short)
	}
	long := generateAdminAuthKey("mail", 32)
	if len(long) != len("sk-mail-")+32 || !isAdminAuthKeyFormat(long) {
		t.Fatalf("unexpected long admin key: %s", long)
	}
}

func TestTokenAuthCacheReturnsIndependentCopies(t *testing.T) {
	now := time.Now()
	lastUsedAt := now.Add(-time.Minute)
	expiresAt := now.Add(time.Hour)
	revokedAt := now.Add(-time.Hour)
	tokenID := uuid.New()
	s := &Store{tokenAuthCache: make(map[string]tokenAuthCacheEntry)}
	s.cacheAccountToken("hash", model.Account{ID: uuid.New(), Username: "owner"}, model.AccountToken{
		ID:         tokenID,
		Name:       "cached token",
		LastUsedAt: &lastUsedAt,
		ExpiresAt:  &expiresAt,
		RevokedAt:  &revokedAt,
	})

	account, token, ok := s.getCachedAccountByToken("hash")
	if !ok {
		t.Fatal("expected cache hit")
	}
	account.Username = "changed"
	token.Name = "changed"
	*token.LastUsedAt = now
	*token.ExpiresAt = now
	*token.RevokedAt = now

	cachedAccount, cachedToken, ok := s.getCachedAccountByToken("hash")
	if !ok {
		t.Fatal("expected second cache hit")
	}
	if cachedAccount.Username != "owner" || cachedToken.Name != "cached token" {
		t.Fatal("cache returned shared mutable values")
	}
	if !cachedToken.LastUsedAt.Equal(lastUsedAt) || !cachedToken.ExpiresAt.Equal(expiresAt) || !cachedToken.RevokedAt.Equal(revokedAt) {
		t.Fatal("cache returned shared timestamp pointers")
	}
}

func TestTokenAuthCacheExpiresAndRemovesEntry(t *testing.T) {
	s := &Store{tokenAuthCache: map[string]tokenAuthCacheEntry{
		"expired": {expiresAt: time.Now().Add(-time.Second)},
	}}

	if _, _, ok := s.getCachedAccountByToken("expired"); ok {
		t.Fatal("expired cache entry must not be returned")
	}
	s.tokenAuthMu.RLock()
	_, exists := s.tokenAuthCache["expired"]
	s.tokenAuthMu.RUnlock()
	if exists {
		t.Fatal("expired cache entry was not removed")
	}
}

func TestInvalidateTokenAuthCacheRemovesTokenAndAdvancesEpoch(t *testing.T) {
	tokenID := uuid.New()
	otherTokenID := uuid.New()
	s := &Store{tokenAuthCache: map[string]tokenAuthCacheEntry{
		"target": {token: model.AccountToken{ID: tokenID}, expiresAt: time.Now().Add(time.Minute)},
		"other":  {token: model.AccountToken{ID: otherTokenID}, expiresAt: time.Now().Add(time.Minute)},
	}}
	epoch := s.tokenAuthEpoch.Load()

	s.invalidateTokenAuthCache(tokenID)

	if s.tokenAuthEpoch.Load() != epoch+1 {
		t.Fatal("cache invalidation did not advance the epoch")
	}
	if _, _, ok := s.getCachedAccountByToken("target"); ok {
		t.Fatal("invalidated token remained cached")
	}
	if _, _, ok := s.getCachedAccountByToken("other"); !ok {
		t.Fatal("unrelated token cache entry was removed")
	}
}

func TestTokenAuthCacheStaysBounded(t *testing.T) {
	s := &Store{tokenAuthCache: make(map[string]tokenAuthCacheEntry, tokenAuthCacheMaxEntries)}
	expiresAt := time.Now().Add(time.Minute)
	for index := 0; index < tokenAuthCacheMaxEntries; index++ {
		hash := fmt.Sprintf("cached-%d", index)
		s.tokenAuthCache[hash] = tokenAuthCacheEntry{expiresAt: expiresAt}
	}

	s.cacheAccountToken("new-token", model.Account{}, model.AccountToken{})

	if len(s.tokenAuthCache) != tokenAuthCacheMaxEntries {
		t.Fatalf("cache exceeded its entry cap: got %d want %d", len(s.tokenAuthCache), tokenAuthCacheMaxEntries)
	}
	if _, ok := s.tokenAuthCache["new-token"]; !ok {
		t.Fatal("new cache entry was not stored after eviction")
	}
}

func TestInvalidateTokenAuthCachesAdvancesEpochOnce(t *testing.T) {
	firstID := uuid.New()
	secondID := uuid.New()
	s := &Store{tokenAuthCache: map[string]tokenAuthCacheEntry{
		"first":  {token: model.AccountToken{ID: firstID}, expiresAt: time.Now().Add(time.Minute)},
		"second": {token: model.AccountToken{ID: secondID}, expiresAt: time.Now().Add(time.Minute)},
	}}
	epoch := s.tokenAuthEpoch.Load()

	s.invalidateTokenAuthCaches([]uuid.UUID{firstID, secondID})

	if s.tokenAuthEpoch.Load() != epoch+1 {
		t.Fatal("batch cache invalidation must advance the epoch once")
	}
	if len(s.tokenAuthCache) != 0 {
		t.Fatal("batch cache invalidation left target entries cached")
	}
}
