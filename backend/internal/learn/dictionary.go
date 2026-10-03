// Package learn holds what learning capture knows beyond one transcript: the
// exact secret values it must take out of every captured turn.
package learn

import (
	"encoding/json"
	"os"
	"path/filepath"
	"regexp"
	"sort"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/learn/redact"
)

// accountFieldRE names the account fields whose values are credentials or
// personal data. It is an allow-list on purpose: an account also carries fields
// like state "active" or env "uat", and replacing every "active" in the human's
// text with a placeholder would wreck exactly the text capture exists to keep.
var accountFieldRE = regexp.MustCompile(`(?i)(e-?mail|mail|user|login|pass|pin|phone|mobile|tel|otp|secret|token|citizen|national)`)

// secretEnvRE names env variables whose values are secrets.
var secretEnvRE = regexp.MustCompile(`(?i)(TOKEN|SECRET|PASSWORD|PASSWD|API_?KEY|ACCESS_?KEY|PRIVATE_?KEY|CREDENTIAL)`)

// minEnvSecretBytes skips env values too short to be a real secret ("1",
// "true", "dev") that a name match would otherwise turn into a placeholder
// everywhere they appear.
const minEnvSecretBytes = 8

// Dictionary returns the exact values capture must replace in every stored
// string: the test-account credentials in each mobile project's script store,
// and the values of secret-named env variables - each project's configured Env
// and the daemon's own environment (environ, as os.Environ returns it).
//
// A store file that cannot be read or parsed contributes nothing and is not an
// error: redaction must never stop capture, and the pattern rules still apply.
func Dictionary(projects []domain.ProjectRecord, environ []string) []redact.Value {
	var out []redact.Value
	stores := map[string]bool{}
	for _, p := range projects {
		if ms := p.Config.MobileScripts; ms != nil {
			stores[expandHome(ms.StoreOrDefault())] = true
		}
		for k, v := range p.Config.Env {
			out = appendEnvSecret(out, k, v)
		}
	}
	for _, kv := range environ {
		if k, v, ok := strings.Cut(kv, "="); ok {
			out = appendEnvSecret(out, k, v)
		}
	}
	dirs := make([]string, 0, len(stores))
	for d := range stores {
		dirs = append(dirs, d)
	}
	sort.Strings(dirs)
	for _, d := range dirs {
		out = append(out, accountValues(filepath.Join(d, "accounts"))...)
	}
	return out
}

func appendEnvSecret(out []redact.Value, key, value string) []redact.Value {
	if !secretEnvRE.MatchString(key) || len(strings.TrimSpace(value)) < minEnvSecretBytes {
		return out
	}
	return append(out, redact.Value{Value: value, Placeholder: "[secret:" + key + "]", Kind: "secret"})
}

// accountValues reads every accounts/*.json in a script store. The shape is the
// store's own: {"accounts": [{"id": "...", "email": "...", "password": "..."}]}.
// An example file ships placeholder values and is skipped.
func accountValues(dir string) []redact.Value {
	files, err := filepath.Glob(filepath.Join(dir, "*.json"))
	if err != nil {
		return nil
	}
	var out []redact.Value
	for _, f := range files {
		if strings.HasSuffix(f, ".example.json") {
			continue
		}
		b, err := os.ReadFile(f) //nolint:gosec // a file in the script store a project's own config names
		if err != nil {
			continue
		}
		var doc struct {
			Accounts []map[string]any `json:"accounts"`
		}
		if json.Unmarshal(b, &doc) != nil {
			continue
		}
		for _, acc := range doc.Accounts {
			id, _ := acc["id"].(string)
			placeholder := "[account]"
			if id != "" {
				placeholder = "[account:" + id + "]"
			}
			for k, v := range acc {
				s, ok := v.(string)
				if !ok || k == "id" || !accountFieldRE.MatchString(k) {
					continue
				}
				out = append(out, redact.Value{Value: s, Placeholder: placeholder, Kind: "account"})
			}
		}
	}
	return out
}

func expandHome(p string) string {
	if p == "~" || strings.HasPrefix(p, "~/") {
		if home, err := os.UserHomeDir(); err == nil {
			return filepath.Join(home, strings.TrimPrefix(p, "~"))
		}
	}
	return p
}
