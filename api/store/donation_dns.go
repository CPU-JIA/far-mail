package store

import (
	"errors"
	"fmt"
	"net"
	"strings"
)

const (
	DonationTXTLabel       = "_far-mail-donate"
	DonationTXTValuePrefix = "far-mail-site-verification="
)

func transientDNSError(err error) bool {
	var dnsErr *net.DNSError
	return errors.As(err, &dnsErr) && (dnsErr.IsTimeout || dnsErr.IsTemporary)
}

// CheckDonationRecords requires both the receiving MX and the per-claim TXT
// proof. Missing or mismatched records are definitive; resolver failures are
// transient so active rewards are not removed by a single DNS outage.
func CheckDonationRecords(domain, serverIP, serverHostname, expectedTXT string) DonationVerification {
	domain = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(domain)), ".")
	serverIP = strings.TrimSpace(serverIP)
	serverHostname = strings.TrimSuffix(strings.ToLower(strings.TrimSpace(serverHostname)), ".")

	txtOK := false
	txtResolved := false
	txtLookupTransient := false
	expectedValues := donationExpectedTXTValues(expectedTXT)
	for _, txtName := range donationTXTNames(domain) {
		txtValues, err := net.LookupTXT(txtName)
		if err != nil {
			txtLookupTransient = txtLookupTransient || transientDNSError(err)
			continue
		}
		txtResolved = true
		for _, value := range txtValues {
			if _, ok := expectedValues[strings.TrimSpace(value)]; ok {
				txtOK = true
				break
			}
		}
		if txtOK {
			break
		}
	}
	if !txtOK {
		if !txtResolved {
			return DonationVerification{Transient: txtLookupTransient, Status: "TXT verification record is not ready"}
		}
		return DonationVerification{Status: "TXT verification record does not match"}
	}

	mxRecords, err := net.LookupMX(domain)
	if err != nil {
		return DonationVerification{Transient: transientDNSError(err), Status: "MX lookup is temporarily unavailable"}
	}
	if len(mxRecords) == 0 {
		return DonationVerification{Status: "No MX record found"}
	}
	if serverIP == "" && serverHostname == "" {
		return DonationVerification{Transient: true, Status: "The site mail server is not configured"}
	}

	lookupFailed := false
	for _, mx := range mxRecords {
		host := strings.TrimSuffix(strings.ToLower(mx.Host), ".")
		if serverHostname != "" && host == serverHostname {
			return DonationVerification{Valid: true, Status: "MX and TXT verification passed"}
		}
		if serverIP == "" {
			continue
		}
		addrs, err := net.LookupHost(host)
		if err != nil {
			lookupFailed = lookupFailed || transientDNSError(err)
			continue
		}
		for _, addr := range addrs {
			if addr == serverIP {
				return DonationVerification{Valid: true, Status: "MX and TXT verification passed"}
			}
		}
	}
	if lookupFailed {
		return DonationVerification{Transient: true, Status: "MX target resolution is temporarily unavailable"}
	}
	return DonationVerification{Status: fmt.Sprintf("MX does not point to %s", firstNonEmpty(serverHostname, serverIP))}
}

func donationTXTNames(domain string) []string {
	return []string{DonationTXTLabel + "." + domain}
}

func donationExpectedTXTValues(expected string) map[string]struct{} {
	return map[string]struct{}{strings.TrimSpace(expected): {}}
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		if strings.TrimSpace(value) != "" {
			return strings.TrimSpace(value)
		}
	}
	return "the site mail server"
}
