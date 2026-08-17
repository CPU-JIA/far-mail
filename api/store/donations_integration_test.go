package store

import (
	"context"
	"errors"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestDonationConcurrencyAndRewardState is opt-in because it requires a real
// PostgreSQL database. FARMAIL_TEST_DATABASE_URL must point at an isolated
// database initialized with sql/init.sql. Every fixture uses a UUID-suffixed
// domain and is removed before the test returns.
func TestDonationConcurrencyAndRewardState(t *testing.T) {
	dsn := os.Getenv("FARMAIL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FARMAIL_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()
	db, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	defer db.Close()

	settings := []string{
		"donation_reward_rate_limit_per_minute",
		"donation_reward_daily_request_limit",
		"donation_reward_total_request_limit",
		"donation_token_rate_limit_cap",
		"donation_max_domains_per_token",
		"donation_dns_failure_tolerance",
	}
	originalSettings := make(map[string]string, len(settings))
	for _, key := range settings {
		value, getErr := db.GetSetting(ctx, key)
		if getErr != nil {
			t.Fatalf("read setting %s: %v", key, getErr)
		}
		originalSettings[key] = value
	}
	defer func() {
		for key, value := range originalSettings {
			_ = db.SetSetting(context.Background(), key, value)
		}
	}()

	fixturePrefix := "donation-" + uuid.NewString()
	tokenIDs := make(map[uuid.UUID]struct{})
	defer func() {
		cleanupCtx, cleanupCancel := context.WithTimeout(context.Background(), 15*time.Second)
		defer cleanupCancel()
		_, _ = db.pool.Exec(cleanupCtx, `DELETE FROM domains WHERE domain LIKE $1`, fixturePrefix+"-%")
		for tokenID := range tokenIDs {
			_, _ = db.pool.Exec(cleanupCtx, `DELETE FROM account_tokens WHERE id = $1`, tokenID)
		}
	}()

	t.Run("domain capacity cannot be bypassed concurrently", func(t *testing.T) {
		if err := db.SetSetting(ctx, "donation_max_domains_per_token", "3"); err != nil {
			t.Fatalf("set domain capacity: %v", err)
		}
		first, _, rawToken, err := db.CreateDonationRequest(ctx, fixturePrefix+"-limit-0.test", true, "")
		if err != nil {
			t.Fatalf("create first donation: %v", err)
		}
		tokenIDs[first.TokenID] = struct{}{}

		const contenders = 12
		type result struct {
			donationTokenID uuid.UUID
			err             error
		}
		results := make(chan result, contenders)
		var ready sync.WaitGroup
		ready.Add(contenders)
		start := make(chan struct{})
		for index := 0; index < contenders; index++ {
			go func(index int) {
				ready.Done()
				<-start
				item, _, _, createErr := db.CreateDonationRequest(
					ctx,
					fmt.Sprintf("%s-limit-%d.test", fixturePrefix, index+1),
					true,
					rawToken,
				)
				var tokenID uuid.UUID
				if item != nil {
					tokenID = item.TokenID
				}
				results <- result{donationTokenID: tokenID, err: createErr}
			}(index)
		}
		ready.Wait()
		close(start)

		succeeded := 0
		limited := 0
		for index := 0; index < contenders; index++ {
			result := <-results
			switch {
			case result.err == nil:
				succeeded++
				if result.donationTokenID != first.TokenID {
					t.Fatalf("concurrent continuation created a different token: %s", result.donationTokenID)
				}
			case errors.Is(result.err, ErrDonationTokenLimit):
				limited++
			default:
				t.Fatalf("unexpected concurrent donation error: %v", result.err)
			}
		}
		if succeeded != 2 || limited != contenders-2 {
			t.Fatalf("concurrent capacity result: succeeded=%d limited=%d, want 2/%d", succeeded, limited, contenders-2)
		}

		var stored int
		if err := db.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM domain_donations
			WHERE token_id = $1 AND status <> 'revoked'
		`, first.TokenID).Scan(&stored); err != nil {
			t.Fatalf("count stored donations: %v", err)
		}
		if stored != 3 {
			t.Fatalf("stored donation count = %d, want 3", stored)
		}
	})

	t.Run("parallel verification keeps the reward aggregate exact", func(t *testing.T) {
		const (
			domainCount = 6
			grantRPM    = 17
			grantDaily  = 211
			grantTotal  = int64(4001)
		)
		for key, value := range map[string]string{
			"donation_reward_rate_limit_per_minute": fmt.Sprint(grantRPM),
			"donation_reward_daily_request_limit":   fmt.Sprint(grantDaily),
			"donation_reward_total_request_limit":   fmt.Sprint(grantTotal),
			"donation_token_rate_limit_cap":         "1000",
			"donation_max_domains_per_token":        "10",
			"donation_dns_failure_tolerance":        "2",
		} {
			if err := db.SetSetting(ctx, key, value); err != nil {
				t.Fatalf("set %s: %v", key, err)
			}
		}

		donations := make([]uuid.UUID, 0, domainCount)
		first, _, rawToken, err := db.CreateDonationRequest(ctx, fixturePrefix+"-state-0.test", true, "")
		if err != nil {
			t.Fatalf("create state donation 0: %v", err)
		}
		tokenIDs[first.TokenID] = struct{}{}
		donations = append(donations, first.ID)
		for index := 1; index < domainCount; index++ {
			item, _, _, createErr := db.CreateDonationRequest(
				ctx,
				fmt.Sprintf("%s-state-%d.test", fixturePrefix, index),
				true,
				rawToken,
			)
			if createErr != nil {
				t.Fatalf("create state donation %d: %v", index, createErr)
			}
			donations = append(donations, item.ID)
		}

		verifyAll := func(label string) {
			t.Helper()
			var wg sync.WaitGroup
			errorsCh := make(chan error, len(donations))
			for _, donationID := range donations {
				wg.Add(1)
				go func(id uuid.UUID) {
					defer wg.Done()
					_, verifyErr := db.ApplyDonationVerification(ctx, id, DonationVerification{Valid: true, Status: label})
					errorsCh <- verifyErr
				}(donationID)
			}
			wg.Wait()
			close(errorsCh)
			for verifyErr := range errorsCh {
				if verifyErr != nil {
					t.Fatalf("parallel verification: %v", verifyErr)
				}
			}
		}

		assertAggregate := func(wantActive int) {
			t.Helper()
			var active, rpm, daily int
			var total int64
			if err := db.pool.QueryRow(ctx, `
				SELECT COUNT(*) FILTER (WHERE d.reward_active),
				       t.rate_limit_per_minute, t.daily_request_limit, t.total_request_limit
				FROM account_tokens t
				LEFT JOIN domain_donations d ON d.token_id = t.id
				WHERE t.id = $1
				GROUP BY t.id
			`, first.TokenID).Scan(&active, &rpm, &daily, &total); err != nil {
				t.Fatalf("read reward aggregate: %v", err)
			}
			if active != wantActive || rpm != wantActive*grantRPM || daily != wantActive*grantDaily || total != int64(wantActive)*grantTotal {
				t.Fatalf(
					"reward aggregate active/rpm/daily/total = %d/%d/%d/%d, want %d/%d/%d/%d",
					active, rpm, daily, total,
					wantActive, wantActive*grantRPM, wantActive*grantDaily, int64(wantActive)*grantTotal,
				)
			}
		}

		verifyAll("parallel activation")
		assertAggregate(domainCount)

		// Repeating successful checks concurrently must not duplicate grants.
		verifyAll("idempotent recheck")
		var grants int
		if err := db.pool.QueryRow(ctx, `
			SELECT COUNT(*) FROM donation_reward_events
			WHERE token_id = $1 AND event_type = 'grant'
		`, first.TokenID).Scan(&grants); err != nil {
			t.Fatalf("count grant events: %v", err)
		}
		if grants != domainCount {
			t.Fatalf("grant event count = %d, want %d", grants, domainCount)
		}

		victim := donations[0]
		if _, err := db.ApplyDonationVerification(ctx, victim, DonationVerification{Transient: true, Status: "temporary resolver failure"}); err != nil {
			t.Fatalf("first transient failure: %v", err)
		}
		assertAggregate(domainCount)
		if _, err := db.ApplyDonationVerification(ctx, victim, DonationVerification{Transient: true, Status: "temporary resolver failure"}); err != nil {
			t.Fatalf("second transient failure: %v", err)
		}
		assertAggregate(domainCount - 1)
		if _, err := db.ApplyDonationVerification(ctx, victim, DonationVerification{Valid: true, Status: "DNS recovered"}); err != nil {
			t.Fatalf("recover donation: %v", err)
		}
		assertAggregate(domainCount)

		if err := db.SetDonationRevoked(ctx, victim, true, "integration revoke"); err != nil {
			t.Fatalf("revoke donation: %v", err)
		}
		assertAggregate(domainCount - 1)
		if _, err := db.ApplyDonationVerification(ctx, victim, DonationVerification{Valid: true, Status: "must remain revoked"}); err == nil {
			t.Fatal("revoked donation unexpectedly accepted verification")
		}
		if err := db.SetDonationRevoked(ctx, victim, false, ""); err != nil {
			t.Fatalf("restore donation for recheck: %v", err)
		}
		assertAggregate(domainCount - 1)
		if _, err := db.ApplyDonationVerification(ctx, victim, DonationVerification{Valid: true, Status: "reactivated"}); err != nil {
			t.Fatalf("reactivate restored donation: %v", err)
		}
		assertAggregate(domainCount)
	})
}
