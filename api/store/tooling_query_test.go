package store

import (
	"strings"
	"testing"

	"github.com/google/uuid"
)

func TestBuildMailboxSearchWhereUsesOnlyRequestedPredicates(t *testing.T) {
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	creatorID := uuid.MustParse("00000000-0000-0000-0000-000000000002")

	where, args := buildMailboxSearchWhere(accountID, &creatorID, "otp", "example.com", true, 0)
	if len(args) != 4 {
		t.Fatalf("expected account, creator, query and domain args, got %d", len(args))
	}
	for _, unwanted := range []string{"$5", "$6", "NOT $", "OR $"} {
		if strings.Contains(where, unwanted) {
			t.Fatalf("optional predicate leaked into query: %q", unwanted)
		}
	}
	for _, expected := range []string{
		"m.account_id = $1",
		"m.creator_token_id = $2",
		"m.full_address ILIKE $3 OR m.address ILIKE $3",
		"split_part(m.full_address, '@', 2) = $4",
		"m.keep_forever = TRUE",
	} {
		if !strings.Contains(where, expected) {
			t.Fatalf("where clause missing %q: %s", expected, where)
		}
	}
	if got := args[2]; got != "%otp%" {
		t.Fatalf("query pattern = %#v, want %%otp%%", got)
	}
}

func TestBuildMailboxSearchWhereExpiringFilter(t *testing.T) {
	accountID := uuid.MustParse("00000000-0000-0000-0000-000000000001")
	where, args := buildMailboxSearchWhere(accountID, nil, "", "", false, 24)
	if len(args) != 2 {
		t.Fatalf("expected account and hour args, got %d", len(args))
	}
	if !strings.Contains(where, "m.keep_forever = FALSE") || !strings.Contains(where, "make_interval(hours => $2)") {
		t.Fatalf("unexpected expiring predicate: %s", where)
	}
}
