package ingress

import (
	"strings"
	"testing"
)

var benchmarkParsedMessage parsedMessage

func TestParseMessageMultipart(t *testing.T) {
	raw := []byte("From: Sender <sender@example.com>\r\n" +
		"Subject: =?UTF-8?Q?Your_code_123456?=\r\n" +
		"Content-Type: multipart/alternative; boundary=abc\r\n" +
		"\r\n" +
		"--abc\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"Code: 123456\r\n" +
		"--abc\r\n" +
		"Content-Type: text/html; charset=utf-8\r\n" +
		"\r\n" +
		"<b>Code: 123456</b>\r\n" +
		"--abc--\r\n")
	parsed := parseMessage(raw, "bounce@example.com", 262144)
	if parsed.Sender != "sender@example.com" {
		t.Fatalf("unexpected sender %q", parsed.Sender)
	}
	if parsed.Subject != "Your code 123456" {
		t.Fatalf("unexpected subject %q", parsed.Subject)
	}
	if parsed.BodyText != "Code: 123456" {
		t.Fatalf("unexpected text body %q", parsed.BodyText)
	}
	if parsed.BodyHTML != "<b>Code: 123456</b>" {
		t.Fatalf("unexpected html body %q", parsed.BodyHTML)
	}
}

func TestParseMessageClampsStoredBody(t *testing.T) {
	raw := []byte("From: sender@example.com\r\n" +
		"Subject: clamp\r\n" +
		"Content-Type: text/plain; charset=utf-8\r\n" +
		"\r\n" +
		"1234567890")
	parsed := parseMessage(raw, "bounce@example.com", 4)
	if parsed.BodyText != "1234" {
		t.Fatalf("expected clamped text body, got %q", parsed.BodyText)
	}
}

func TestParseMessageDetectsAttachment(t *testing.T) {
	raw := []byte("From: sender@example.com\r\n" +
		"Subject: attachment\r\n" +
		"Content-Type: multipart/mixed; boundary=mixed\r\n\r\n" +
		"--mixed\r\nContent-Type: text/plain\r\n\r\nhello\r\n" +
		"--mixed\r\nContent-Type: application/pdf; name=receipt.pdf\r\n" +
		"Content-Disposition: attachment; filename=receipt.pdf\r\n\r\nJVBERi0=\r\n" +
		"--mixed--\r\n")
	parsed := parseMessage(raw, "bounce@example.com", 262144)
	if !parsed.HasAttachments {
		t.Fatal("expected attachment metadata to be detected")
	}
}

func BenchmarkParseMessage(b *testing.B) {
	cases := []struct {
		name string
		raw  []byte
	}{
		{
			name: "small_plain",
			raw:  []byte("From: sender@example.com\r\nSubject: Code 123456\r\nContent-Type: text/plain; charset=utf-8\r\n\r\nYour code is 123456\r\n"),
		},
		{
			name: "100kb_plain",
			raw: []byte("From: sender@example.com\r\nSubject: large\r\nContent-Type: text/plain; charset=utf-8\r\n\r\n" +
				strings.Repeat("verification payload ", 5120)),
		},
		{
			name: "multipart_alternative",
			raw: []byte("From: sender@example.com\r\nSubject: multipart\r\nContent-Type: multipart/alternative; boundary=bench\r\n\r\n" +
				"--bench\r\nContent-Type: text/plain\r\n\r\nCode: 123456\r\n" +
				"--bench\r\nContent-Type: text/html\r\n\r\n<b>Code: 123456</b>\r\n--bench--\r\n"),
		},
	}
	for _, test := range cases {
		b.Run(test.name, func(b *testing.B) {
			b.ReportAllocs()
			b.SetBytes(int64(len(test.raw)))
			for b.Loop() {
				benchmarkParsedMessage = parseMessage(test.raw, "bounce@example.com", 262144)
			}
		})
	}
}
