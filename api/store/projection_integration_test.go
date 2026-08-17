package store

import (
	"context"
	"os"
	"testing"
	"time"

	"github.com/google/uuid"
)

// TestEmailDeletionRefreshesMailboxProjection is opt-in because it exercises
// PostgreSQL-specific data-modifying CTE semantics. Point
// FARMAIL_TEST_DATABASE_URL at an isolated database initialized with
// sql/init.sql; the test creates and removes only UUID-suffixed fixtures.
func TestEmailDeletionRefreshesMailboxProjection(t *testing.T) {
	dsn := os.Getenv("FARMAIL_TEST_DATABASE_URL")
	if dsn == "" {
		t.Skip("FARMAIL_TEST_DATABASE_URL is not set")
	}

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	store, err := New(ctx, dsn)
	if err != nil {
		t.Fatalf("open test store: %v", err)
	}
	defer store.Close()

	fixtureID := uuid.New()
	accountID := uuid.New()
	domainName := "projection-" + fixtureID.String() + ".test"
	if _, err := store.pool.Exec(ctx, `
		INSERT INTO accounts (id, username, api_key, is_admin)
		VALUES ($1, $2, $3, FALSE)
	`, accountID, "projection-"+fixtureID.String(), "sk-test-"+fixtureID.String()); err != nil {
		t.Fatalf("insert account: %v", err)
	}
	defer func() {
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM accounts WHERE id = $1`, accountID)
		_, _ = store.pool.Exec(context.Background(), `DELETE FROM domains WHERE domain = $1`, domainName)
	}()

	var domainID int
	if err := store.pool.QueryRow(ctx, `
		INSERT INTO domains (domain, is_active, status)
		VALUES ($1, TRUE, 'active')
		RETURNING id
	`, domainName).Scan(&domainID); err != nil {
		t.Fatalf("insert domain: %v", err)
	}

	insertMailbox := func(t *testing.T, label string, received []time.Time) (uuid.UUID, []uuid.UUID, string) {
		t.Helper()
		mailboxID := uuid.New()
		address := label + "@" + domainName
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO mailboxes (id, account_id, address, domain_id, full_address)
			VALUES ($1, $2, $3, $4, $5)
		`, mailboxID, accountID, label, domainID, address); err != nil {
			t.Fatalf("insert mailbox: %v", err)
		}

		emailIDs := make([]uuid.UUID, len(received))
		for i, receivedAt := range received {
			emailIDs[i] = uuid.New()
			if _, err := store.pool.Exec(ctx, `
				INSERT INTO emails (
					id, mailbox_id, sender, subject, parsed_code,
					parsed_code_source, parsed_link, parsed_link_source, received_at
				) VALUES ($1, $2, $3, $4, $5, 'subject', $6, 'body', $7)
			`, emailIDs[i], mailboxID, "sender@example.test", "subject-"+label,
				label+"-code-"+string(rune('0'+i)), "https://example.test/"+label, receivedAt); err != nil {
				t.Fatalf("insert email %d: %v", i, err)
			}
		}
		latest := len(emailIDs) - 1
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO mailbox_state (
				mailbox_id, account_id, domain_id, domain_name, full_address,
				latest_email_id, latest_sender, latest_subject,
				latest_code, latest_code_source, latest_link, latest_link_source,
				latest_received_at, email_count
			) VALUES ($1, $2, $3, $4, $5, $6, 'sender@example.test', $7, $8, 'subject', $9, 'body', $10, $11)
		`, mailboxID, accountID, domainID, domainName, address, emailIDs[latest],
			"subject-"+label, label+"-code-"+string(rune('0'+latest)),
			"https://example.test/"+label, received[latest], len(emailIDs)); err != nil {
			t.Fatalf("insert mailbox projection: %v", err)
		}
		return mailboxID, emailIDs, address
	}

	assertState := func(t *testing.T, mailboxID uuid.UUID, wantID *uuid.UUID, wantCount int64) {
		t.Helper()
		var gotID *uuid.UUID
		var gotCount int64
		if err := store.pool.QueryRow(ctx, `
			SELECT latest_email_id, email_count
			FROM mailbox_state
			WHERE mailbox_id = $1
		`, mailboxID).Scan(&gotID, &gotCount); err != nil {
			t.Fatalf("read mailbox projection: %v", err)
		}
		if gotCount != wantCount {
			t.Fatalf("email_count = %d, want %d", gotCount, wantCount)
		}
		if wantID == nil {
			if gotID != nil {
				t.Fatalf("latest_email_id = %v, want NULL", *gotID)
			}
			return
		}
		if gotID == nil || *gotID != *wantID {
			t.Fatalf("latest_email_id = %v, want %v", gotID, *wantID)
		}
	}

	t.Run("single latest deletion promotes previous email", func(t *testing.T) {
		now := time.Now().UTC()
		mailboxID, emailIDs, address := insertMailbox(t, "single", []time.Time{now.Add(-time.Minute), now})
		defer store.pool.Exec(context.Background(), `DELETE FROM mailboxes WHERE id = $1`, mailboxID)

		if err := store.DeleteEmail(ctx, emailIDs[1], mailboxID, accountID, nil); err != nil {
			t.Fatalf("delete latest email: %v", err)
		}
		assertState(t, mailboxID, &emailIDs[0], 1)

		mailbox, state, err := store.LookupMailboxProjectionScoped(ctx, accountID, false, nil, address)
		if err != nil || mailbox.ID != mailboxID || state == nil || state.LatestEmailID == nil || *state.LatestEmailID != emailIDs[0] {
			t.Fatalf("valid projection lookup failed: mailbox=%+v state=%+v err=%v", mailbox, state, err)
		}
		mailbox, latest, err := store.LookupLatestEmailScoped(ctx, accountID, false, nil, address)
		if err != nil || mailbox.ID != mailboxID || latest == nil || latest.ID != emailIDs[0] {
			t.Fatalf("latest email lookup failed: mailbox=%+v email=%+v err=%v", mailbox, latest, err)
		}
		items, total, err := store.ListEmails(ctx, mailboxID, accountID, nil, 1, 20)
		if err != nil || total != 1 || len(items) != 1 || items[0].ID != emailIDs[0] {
			t.Fatalf("email page lookup failed: items=%+v total=%d err=%v", items, total, err)
		}
		mailboxes, mailboxTotal, err := store.ListMailboxes(ctx, accountID, nil, 1, 20)
		if err != nil || mailboxTotal != 1 || len(mailboxes) != 1 || mailboxes[0].ID != mailboxID {
			t.Fatalf("mailbox page lookup failed: items=%+v total=%d err=%v", mailboxes, mailboxTotal, err)
		}
		mailboxes, mailboxTotal, err = store.ListMailboxes(ctx, accountID, nil, 2, 20)
		if err != nil || mailboxTotal != 1 || len(mailboxes) != 0 {
			t.Fatalf("empty mailbox page lost its total: items=%+v total=%d err=%v", mailboxes, mailboxTotal, err)
		}
		creatorTokenID := uuid.New()
		if _, err := store.pool.Exec(ctx, `
			INSERT INTO account_tokens (id, account_id, name, token_hash, token_prefix, token_kind)
			VALUES ($1, $2, 'projection creator', $3, 'sk-test', 'donation')
		`, creatorTokenID, accountID, "creator-"+creatorTokenID.String()); err != nil {
			t.Fatalf("insert creator token: %v", err)
		}
		defer store.pool.Exec(context.Background(), `DELETE FROM account_tokens WHERE id = $1`, creatorTokenID)
		if _, err := store.pool.Exec(ctx, `UPDATE mailboxes SET creator_token_id = $2 WHERE id = $1`, mailboxID, creatorTokenID); err != nil {
			t.Fatalf("assign creator token: %v", err)
		}
		mailboxes, mailboxTotal, err = store.ListMailboxes(ctx, accountID, &creatorTokenID, 1, 20)
		if err != nil || mailboxTotal != 1 || len(mailboxes) != 1 || mailboxes[0].ID != mailboxID {
			t.Fatalf("creator-scoped mailbox page failed: items=%+v total=%d err=%v", mailboxes, mailboxTotal, err)
		}

		// A stale historical projection must never be trusted by the hot read path.
		if _, err := store.pool.Exec(ctx, `UPDATE mailbox_state SET latest_email_id = $2 WHERE mailbox_id = $1`, mailboxID, uuid.New()); err != nil {
			t.Fatalf("corrupt projection fixture: %v", err)
		}
		_, state, err = store.LookupMailboxProjectionScoped(ctx, accountID, false, nil, address)
		if err != nil || state != nil {
			t.Fatalf("stale projection was not rejected: state=%+v err=%v", state, err)
		}
	})

	t.Run("ingress returns the inserted event and keeps a newer projection", func(t *testing.T) {
		now := time.Now().UTC()
		mailboxID, emailIDs, address := insertMailbox(t, "ingress", []time.Time{now.Add(-time.Minute), now})
		defer store.pool.Exec(context.Background(), `DELETE FROM mailboxes WHERE id = $1`, mailboxID)
		future := now.Add(time.Hour)
		if _, err := store.pool.Exec(ctx, `UPDATE mailbox_state SET latest_received_at = $2 WHERE mailbox_id = $1`, mailboxID, future); err != nil {
			t.Fatalf("make newer projection fixture: %v", err)
		}

		result, delivered, err := store.InsertEmailResolved(ctx, IngressDelivery{
			Recipient: address, LocalPart: "ingress", DomainName: domainName,
			AccountID: accountID, DomainID: domainID, MailboxTTLMinutes: 30,
			Sender: "new@example.test", Subject: "new event", BodyText: "new body",
			SizeBytes: 9,
		})
		if err != nil || !delivered || result == nil {
			t.Fatalf("insert ingress email: result=%+v delivered=%v err=%v", result, delivered, err)
		}
		if result.Email.ID == emailIDs[0] || result.Email.ID == emailIDs[1] || result.Email.Subject != "new event" {
			t.Fatalf("ingress event did not return inserted email: %+v", result.Email)
		}
		assertState(t, mailboxID, &emailIDs[1], 3)
	})

	t.Run("age cleanup preserves a newer projection", func(t *testing.T) {
		now := time.Now().UTC()
		mailboxID, emailIDs, _ := insertMailbox(t, "age", []time.Time{now.Add(-48 * time.Hour), now})
		defer store.pool.Exec(context.Background(), `DELETE FROM mailboxes WHERE id = $1`, mailboxID)

		deleted, err := store.DeleteEmailsOlderThan(ctx, 24*time.Hour)
		if err != nil || deleted != 1 {
			t.Fatalf("age cleanup: deleted=%d err=%v", deleted, err)
		}
		assertState(t, mailboxID, &emailIDs[1], 1)
	})

	t.Run("global trim promotes the retained newest email", func(t *testing.T) {
		now := time.Now().UTC()
		mailboxID, emailIDs, _ := insertMailbox(t, "trim", []time.Time{now.Add(-time.Second), now})
		defer store.pool.Exec(context.Background(), `DELETE FROM mailboxes WHERE id = $1`, mailboxID)

		deleted, err := store.TrimEmailsToMaxCount(ctx, 1)
		if err != nil || deleted != 1 {
			t.Fatalf("global trim: deleted=%d err=%v", deleted, err)
		}
		assertState(t, mailboxID, &emailIDs[1], 1)
	})

	t.Run("maintenance purge clears projection", func(t *testing.T) {
		now := time.Now().UTC()
		mailboxID, _, address := insertMailbox(t, "purge", []time.Time{now.Add(-time.Second), now})
		defer store.pool.Exec(context.Background(), `DELETE FROM mailboxes WHERE id = $1`, mailboxID)

		deleted, err := store.PurgeEmails(ctx, accountID, false, nil, address, "", 0)
		if err != nil || deleted != 2 {
			t.Fatalf("maintenance purge: deleted=%d err=%v", deleted, err)
		}
		assertState(t, mailboxID, nil, 0)
		mailbox, latest, err := store.LookupLatestEmailScoped(ctx, accountID, false, nil, address)
		if err != nil || mailbox.ID != mailboxID || latest != nil {
			t.Fatalf("empty latest lookup failed: mailbox=%+v email=%+v err=%v", mailbox, latest, err)
		}
		items, total, err := store.ListEmails(ctx, mailboxID, accountID, nil, 5, 20)
		if err != nil || total != 0 || len(items) != 0 {
			t.Fatalf("empty email page failed: items=%+v total=%d err=%v", items, total, err)
		}
	})
}
