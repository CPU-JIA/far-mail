package store

import "testing"

var (
	benchmarkCode   string
	benchmarkSource string
)

func TestExtractCodeSkipsHTMLTagFalsePositive(t *testing.T) {
	source := `Your temporary OpenAI verification code
<html><body><h1>OpenAI</h1><p>Enter this temporary verification code to continue:</p><div>489810</div></body></html>`
	code, by := ExtractCode(source)
	if code != "489810" {
		t.Fatalf("code = %q, by = %q; want 489810", code, by)
	}
}

func TestExtractCodeRejectsTemplateWords(t *testing.T) {
	for _, value := range []string{"html", "body", "OpenAI", "temporary", "verification", "continue", "000000"} {
		if IsLikelyVerificationCode(value) {
			t.Fatalf("%q should not be accepted as verification code", value)
		}
	}
}

func TestExtractCodeStrategiesAndBoundaries(t *testing.T) {
	tests := []struct {
		name   string
		source string
		code   string
		by     string
	}{
		{name: "keyword digits", source: "Verification code: 123456", code: "123456", by: "keyword"},
		{name: "chinese keyword", source: "您的验证码是 654321", code: "654321", by: "keyword"},
		{name: "alphanumeric token", source: "Use passcode A1B2C3", code: "A1B2C3", by: "keyword"},
		{name: "global digits", source: "Sign in with 778899", code: "778899", by: "digits"},
		{name: "word boundary", source: "Verification code: prefix123456suffix", code: "", by: ""},
		{name: "underscore boundary", source: "Verification code: value_123456_more", code: "", by: ""},
		{name: "later hint", source: "Code words only. Padding without a token. OTP 482910", code: "482910", by: "keyword"},
	}
	for _, test := range tests {
		t.Run(test.name, func(t *testing.T) {
			code, by := ExtractCode(test.source)
			if code != test.code || by != test.by {
				t.Fatalf("ExtractCode(%q) = %q/%q, want %q/%q", test.source, code, by, test.code, test.by)
			}
		})
	}
}

func BenchmarkExtractProjection(b *testing.B) {
	source := `Your temporary verification code is 489810. Continue at https://accounts.example.com/verify?code=489810.`
	b.ReportAllocs()
	for b.Loop() {
		benchmarkCode, benchmarkSource = ExtractCode(source)
		_, _ = ExtractLink(source)
	}
}
