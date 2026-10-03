package redact

import (
	"strings"
	"testing"
)

// fake assembles a token-shaped test value at run time, so no complete token
// literal sits in the source for a secret scanner to flag.
func fake(prefix, body string) string { return prefix + body }

func TestRedact_ReplacesSecretsAndKeepsTheTextAroundThem(t *testing.T) {
	r := New([]Value{
		{Value: "uat.tester@example.com", Placeholder: "[account:nter-uat-1]", Kind: "account"},
		{Value: "Pa55word!", Placeholder: "[account:nter-uat-1]", Kind: "account"},
	})
	cases := []struct {
		name, in, want string
	}{
		{"dictionary value", "log in as uat.tester@example.com / Pa55word! then open the coupon", "log in as [account:nter-uat-1] / [account:nter-uat-1] then open the coupon"},
		{"github token", "token " + fake("gh"+"p_", "0123456789abcdefghijklmnopqrstuvwxyz0") + " leaked", "token [token] leaked"},
		{"gitlab token", fake("glp"+"at-", "abcdefghij0123456789") + " here", "[token] here"},
		{"anthropic key", "key " + fake("sk-"+"ant-", "api03-abcdefghijklmnopqrstuvwxyz"), "key [token]"},
		{"jwt", fake("ey"+"JhbGciOiJIUzI1NiJ9.", "eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijkl"), "[token]"},
		{"bearer keeps its label", "Authorization: Bearer " + fake("abcdefghij", "klmnopqrstuvwxyz012345"), "Authorization: Bearer [token]"},
		{"env assignment keeps the name", "export JIRA_API_TOKEN=" + fake("AT", "xyz1234567890"), "export JIRA_API_TOKEN=[secret]"},
		{"env password is a secret", "DB_PASSWORD=s3cretvalue", "DB_PASSWORD=[secret]"},
		{"password with separator", "password: hunter2222", "password: [password]"},
		{"thai password", "รหัสผ่านคือ abc12345", "รหัสผ่านคือ [password]"},
		{"email", "mail dev@corp.example.co.th please", "mail [email] please"},
		{"thai mobile", "call 081-234-5678", "call [phone]"},
		{"valid thai national id", "id 1-1037-02071-81-1", "id [national-id]"},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, _ := r.Redact(c.in)
			if got != c.want {
				t.Errorf("Redact(%q) = %q, want %q", c.in, got, c.want)
			}
		})
	}
}

// Precision is the point: none of these carry a secret, and every one of them
// was mangled by somebody's "card number" or "password" rule.
func TestRedact_LeavesOrdinaryTextAlone(t *testing.T) {
	r := New(nil)
	for _, in := range []string{
		"the password reset flow is broken",
		"timestamp 1727954400123 and snowflake 1234567890123456789",
		"line 1234 of reader.go, PR #331, MR !3174",
		"13 digits that fail the checksum: 1234567890123",
		"ao sim tap --label Continue",
		"ทำให้มันเปิดปิดได้ในแอพเลย",
		"the access token is refreshed every hour",
	} {
		got, counts := r.Redact(in)
		if got != in || len(counts) != 0 {
			t.Errorf("Redact(%q) = %q (%v); want it untouched", in, got, counts)
		}
	}
}

func TestNew_IgnoresDictionaryValuesTooShortToBeDistinctive(t *testing.T) {
	r := New([]Value{{Value: "dev", Placeholder: "[secret:ENV]", Kind: "secret"}})
	if got, _ := r.Redact("the dev build"); got != "the dev build" {
		t.Errorf("a 3-byte value blanked ordinary text: %q", got)
	}
}

func TestStripBlobs(t *testing.T) {
	json := "{\n" + strings.Repeat("  \"field\": \"value\",\n", 30) + "}"
	in := "it fails with this:\n\n" + json + "\n\nwhy?"
	got, n := StripBlobs(in)
	if n != 1 || strings.Contains(got, "field") || !strings.HasPrefix(got, "it fails with this:") || !strings.HasSuffix(got, "why?") {
		t.Errorf("StripBlobs = %q (%d)", got, n)
	}
	prose := strings.Repeat("ฉันอยากให้ worker ทำงานแบบนี้ทุกครั้งเพราะมันเร็วกว่า ", 10)
	if got, n := StripBlobs(prose); n != 0 || got != prose {
		t.Errorf("long hand-written prose was collapsed")
	}
	fence := "see:\n```go\n" + strings.Repeat("x := 1\n", 60) + "```\nok"
	if got, n := StripFences(fence); n != 1 || strings.Contains(got, "x := 1") {
		t.Errorf("StripFences = %q (%d)", got, n)
	}
}

func TestClipNeverSplitsACharacter(t *testing.T) {
	s := strings.Repeat("ก", 100) // 3 bytes each
	head := Clip(s, 10)
	if !strings.HasPrefix(head, "กกก") {
		t.Errorf("Clip = %q", head)
	}
	for _, out := range []string{Clip(s, 10), ClipTail(s, 10)} {
		if !utf8Valid(out) {
			t.Errorf("clipped string is not valid UTF-8: %q", out)
		}
	}
}

func utf8Valid(s string) bool { return strings.ToValidUTF8(s, "�") == s }
