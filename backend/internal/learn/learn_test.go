package learn

import (
	"os"
	"path/filepath"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/msgdelivery"
)

func TestDeliveryAuthorFor(t *testing.T) {
	cases := []struct {
		trigger, body string
		want          domain.DeliveryAuthor
	}{
		{msgdelivery.TriggerSend, "please run the script", domain.DeliveryAuthorHuman},
		{"", "please run the script", domain.DeliveryAuthorHuman},
		{msgdelivery.TriggerSend, "[from @proj-3] qa done", domain.DeliveryAuthorAgent},
		{msgdelivery.TriggerNudge, "CI is failing on PR #1.", domain.DeliveryAuthorAO},
		{msgdelivery.TriggerCrewNotice, "qa joined", domain.DeliveryAuthorAO},
		{msgdelivery.TriggerCommentDispatch, "A reviewer left...", domain.DeliveryAuthorAO},
	}
	for _, c := range cases {
		if got := DeliveryAuthorFor(c.trigger, c.body); got != c.want {
			t.Errorf("DeliveryAuthorFor(%q, %q) = %s, want %s", c.trigger, c.body, got, c.want)
		}
	}
}

func TestDictionary(t *testing.T) {
	store := t.TempDir()
	if err := os.MkdirAll(filepath.Join(store, "accounts"), 0o750); err != nil {
		t.Fatal(err)
	}
	write := func(name, body string) {
		if err := os.WriteFile(filepath.Join(store, "accounts", name), []byte(body), 0o600); err != nil {
			t.Fatal(err)
		}
	}
	write("nter.json", `{"_doc":"x","accounts":[{"id":"nter-uat-1","env":"uat","email":"a@b.co","password":"Pw-12345","state":"active","checked":"2026-09-01"}]}`)
	write("nter.example.json", `{"accounts":[{"id":"example","email":"placeholder@example.com","password":"changeme"}]}`)
	write("broken.json", `{not json`)

	projects := []domain.ProjectRecord{
		{ID: "nter", Config: domain.ProjectConfig{
			MobileScripts: &domain.MobileScriptsConfig{Product: "nter", Platform: "ios", Store: store},
			Env:           map[string]string{"JIRA_API_TOKEN": "ATATT3xyzxyzxyz", "BUILD_FLAVOR": "uat-debug", "SHORT_TOKEN": "x"},
		}},
	}
	values := Dictionary(projects, []string{"GITHUB_TOKEN=" + "gh" + "p_abcdefghijklmnop", "HOME=/Users/x", "EMPTY_SECRET="})
	got := map[string]string{}
	for _, v := range values {
		got[v.Value] = v.Placeholder
	}
	want := map[string]string{
		"a@b.co":                    "[account:nter-uat-1]",
		"Pw-12345":                  "[account:nter-uat-1]",
		"ATATT3xyzxyzxyz":           "[secret:JIRA_API_TOKEN]",
		"gh" + "p_abcdefghijklmnop": "[secret:GITHUB_TOKEN]",
	}
	for v, p := range want {
		if got[v] != p {
			t.Errorf("value %q -> %q, want %q", v, got[v], p)
		}
	}
	for _, leaked := range []string{"active", "uat", "2026-09-01", "uat-debug", "/Users/x", "placeholder@example.com", "x"} {
		if _, ok := got[leaked]; ok {
			t.Errorf("%q must not be in the dictionary: it is not a secret, and would blank ordinary text", leaked)
		}
	}
}
