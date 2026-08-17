package store

import (
	"errors"
	"net"
	"testing"
)

func TestTransientDNSError(t *testing.T) {
	tests := []struct {
		name string
		err  error
		want bool
	}{
		{name: "timeout", err: &net.DNSError{IsTimeout: true}, want: true},
		{name: "temporary", err: &net.DNSError{IsTemporary: true}, want: true},
		{name: "missing record", err: &net.DNSError{IsNotFound: true}, want: false},
		{name: "non DNS error", err: errors.New("network unavailable"), want: false},
	}

	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			if got := transientDNSError(test.err); got != test.want {
				t.Fatalf("transientDNSError() = %v, want %v", got, test.want)
			}
		})
	}
}

func TestFirstNonEmpty(t *testing.T) {
	if got := firstNonEmpty("", "  mail.example.com  ", "192.0.2.10"); got != "mail.example.com" {
		t.Fatalf("firstNonEmpty() = %q", got)
	}
	if got := firstNonEmpty("", "  "); got != "the site mail server" {
		t.Fatalf("firstNonEmpty() fallback = %q", got)
	}
}

func TestDonationTXTBrand(t *testing.T) {
	names := donationTXTNames("example.com")
	if len(names) != 1 || names[0] != "_far-mail-donate.example.com" {
		t.Fatalf("donationTXTNames() = %#v", names)
	}

	values := donationExpectedTXTValues("far-mail-site-verification=challenge")
	if _, ok := values["far-mail-site-verification=challenge"]; !ok {
		t.Fatal("missing FAR Mail TXT value")
	}
	if _, ok := values["tempmail-site-verification=challenge"]; ok {
		t.Fatal("legacy TXT value must not be accepted")
	}
}
