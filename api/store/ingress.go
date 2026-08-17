package store

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"

	"farmail/model"

	"github.com/google/uuid"
	"github.com/jackc/pgx/v5"
)

type IngressDomain struct {
	ID     int
	Domain string
}

type IngressSnapshot struct {
	Domains           []IngressDomain
	DefaultAccountID  uuid.UUID
	MailboxTTLMinutes int
}

type IngressDelivery struct {
	Recipient         string
	LocalPart         string
	DomainName        string
	AccountID         uuid.UUID
	DomainID          int
	MailboxTTLMinutes int
	Sender            string
	Subject           string
	BodyText          string
	BodyHTML          string
	HasAttachments    bool
	Raw               string
	SizeBytes         int
}

func (s *Store) LoadIngressSnapshot(ctx context.Context) (*IngressSnapshot, error) {
	rows, err := s.pool.Query(ctx, `
		SELECT id, domain
		FROM domains
		WHERE is_active = TRUE
		ORDER BY length(domain) DESC, domain
	`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()

	domains := make([]IngressDomain, 0, 16)
	for rows.Next() {
		var item IngressDomain
		if err := rows.Scan(&item.ID, &item.Domain); err != nil {
			return nil, err
		}
		item.Domain = strings.ToLower(strings.Trim(strings.TrimSpace(item.Domain), "."))
		if item.Domain == "" {
			continue
		}
		domains = append(domains, item)
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}

	var accountID uuid.UUID
	if err := s.pool.QueryRow(ctx, `
		SELECT id
		FROM accounts
		WHERE is_admin = TRUE AND is_active = TRUE
		ORDER BY created_at
		LIMIT 1
	`).Scan(&accountID); err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, fmt.Errorf("no active admin account for ingress")
		}
		return nil, err
	}

	ttlMinutes := 30
	var rawTTL string
	if err := s.pool.QueryRow(ctx, `
		SELECT COALESCE(NULLIF(value, ''), '30')
		FROM app_settings
		WHERE key = 'mailbox_ttl_minutes'
		LIMIT 1
	`).Scan(&rawTTL); err != nil {
		if !errors.Is(err, pgx.ErrNoRows) {
			return nil, err
		}
	} else if parsed, err := strconv.Atoi(strings.TrimSpace(rawTTL)); err == nil {
		ttlMinutes = parsed
	}

	return &IngressSnapshot{
		Domains:           domains,
		DefaultAccountID:  accountID,
		MailboxTTLMinutes: ttlMinutes,
	}, nil
}

func (s *Store) InsertEmailResolved(ctx context.Context, delivery IngressDelivery) (*model.MailboxEmailEvent, bool, error) {
	var result model.MailboxEmailEvent
	recipient := strings.ToLower(strings.TrimSpace(delivery.Recipient))
	localPart := strings.ToLower(strings.TrimSpace(delivery.LocalPart))
	domainName := strings.ToLower(strings.Trim(strings.TrimSpace(delivery.DomainName), "."))
	if recipient == "" || localPart == "" || domainName == "" || delivery.AccountID == uuid.Nil || delivery.DomainID <= 0 {
		return nil, false, nil
	}
	if delivery.SizeBytes <= 0 {
		delivery.SizeBytes = len(delivery.Raw)
	}
	joined := strings.TrimSpace(delivery.Subject + "\n" + delivery.BodyText + "\n" + delivery.BodyHTML)
	parsedCode, parsedCodeSource := ExtractCode(joined)
	parsedLink, parsedLinkSource := ExtractLink(joined)

	err := s.pool.QueryRow(ctx,
		`WITH upsert_mailbox AS (
			INSERT INTO mailboxes (account_id, address, domain_id, full_address, expires_at, keep_forever)
			VALUES (
				$1,
				$2,
				$3,
				$4,
				CASE WHEN $5 <= 0 THEN NULL ELSE NOW() + make_interval(mins => $5) END,
				FALSE
			)
			ON CONFLICT (full_address) DO UPDATE
			SET expires_at = CASE
					WHEN mailboxes.keep_forever THEN mailboxes.expires_at
					WHEN $5 <= 0 THEN NULL
					WHEN mailboxes.expires_at IS NULL OR mailboxes.expires_at <= NOW() THEN NOW() + make_interval(mins => $5)
					ELSE mailboxes.expires_at
				END
			RETURNING id, account_id, domain_id, full_address, expires_at, keep_forever
		 ),
		 inserted_email AS (
			INSERT INTO emails (
				mailbox_id, sender, subject, body_text, body_html, raw_message, size_bytes, has_attachments,
				parsed_code, parsed_code_source, parsed_link, parsed_link_source
			)
			SELECT id, $6, $7, $8, $9, $10, $11, $12, $13, $14, $15, $16
			FROM upsert_mailbox
			RETURNING id, mailbox_id, sender, subject, has_attachments, parsed_code, parsed_code_source, parsed_link, parsed_link_source, received_at
		 ),
		 state_upsert AS (
		 INSERT INTO mailbox_state (
			mailbox_id, account_id, domain_id, domain_name, full_address,
			latest_email_id, latest_sender, latest_subject,
			latest_code, latest_code_source, latest_link, latest_link_source,
			latest_received_at, email_count, expires_at, keep_forever, created_at, updated_at
		 )
		 SELECT
			m.id,
			m.account_id,
			m.domain_id,
			$17,
			m.full_address,
			e.id,
			e.sender,
			e.subject,
			e.parsed_code,
			e.parsed_code_source,
			e.parsed_link,
			e.parsed_link_source,
			e.received_at,
			1,
			m.expires_at,
			m.keep_forever,
			NOW(),
			NOW()
		 FROM upsert_mailbox m
		 JOIN inserted_email e ON e.mailbox_id = m.id
		 ON CONFLICT (mailbox_id) DO UPDATE SET
			account_id = EXCLUDED.account_id,
			domain_id = EXCLUDED.domain_id,
			domain_name = EXCLUDED.domain_name,
			full_address = EXCLUDED.full_address,
			latest_email_id = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_email_id ELSE mailbox_state.latest_email_id END,
			latest_sender = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_sender ELSE mailbox_state.latest_sender END,
			latest_subject = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_subject ELSE mailbox_state.latest_subject END,
			latest_code = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_code ELSE mailbox_state.latest_code END,
			latest_code_source = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_code_source ELSE mailbox_state.latest_code_source END,
			latest_link = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_link ELSE mailbox_state.latest_link END,
			latest_link_source = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_link_source ELSE mailbox_state.latest_link_source END,
			latest_received_at = CASE
				WHEN mailbox_state.latest_received_at IS NULL
				  OR EXCLUDED.latest_received_at > mailbox_state.latest_received_at
				  OR (EXCLUDED.latest_received_at = mailbox_state.latest_received_at AND EXCLUDED.latest_email_id > mailbox_state.latest_email_id)
				THEN EXCLUDED.latest_received_at ELSE mailbox_state.latest_received_at END,
			email_count = mailbox_state.email_count + 1,
			expires_at = EXCLUDED.expires_at,
			keep_forever = EXCLUDED.keep_forever,
			updated_at = NOW()
		 RETURNING mailbox_id
		 )
		 SELECT m.id, m.full_address, e.id, e.sender, e.subject, e.has_attachments,
		        e.parsed_code, e.parsed_code_source, e.parsed_link, e.parsed_link_source, e.received_at
		 FROM upsert_mailbox m
		 JOIN inserted_email e ON e.mailbox_id = m.id
		 JOIN state_upsert state ON state.mailbox_id = m.id`,
		delivery.AccountID, localPart, delivery.DomainID, recipient, delivery.MailboxTTLMinutes,
		delivery.Sender, delivery.Subject, delivery.BodyText, delivery.BodyHTML, delivery.Raw, delivery.SizeBytes, delivery.HasAttachments,
		parsedCode, parsedCodeSource, parsedLink, parsedLinkSource, domainName,
	).Scan(
		&result.MailboxID, &result.FullAddress, &result.Email.ID,
		&result.Email.Sender, &result.Email.Subject, &result.Email.HasAttachments,
		&result.Email.ParsedCode, &result.Email.ParsedCodeSource, &result.Email.ParsedLink, &result.Email.ParsedLinkSource,
		&result.Email.ReceivedAt,
	)
	if err != nil {
		if errors.Is(err, pgx.ErrNoRows) {
			return nil, false, nil
		}
		return nil, false, err
	}
	result.Email.SizeBytes = delivery.SizeBytes
	return &result, true, nil
}
