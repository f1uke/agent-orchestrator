package releasetrain

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"
)

// RuleID names a rule, e.g. "E1".
type RuleID string

// Severity orders hits; a smaller value is more severe.
type Severity int

// Severities from most to least severe.
const (
	SevBlock Severity = iota + 1
	SevData
	SevRisk
	SevHygiene
	SevInfo
)

var severityNames = map[Severity]string{
	SevBlock: "block", SevData: "data", SevRisk: "risk", SevHygiene: "hygiene", SevInfo: "info",
}

// String names the severity.
func (s Severity) String() string { return severityNames[s] }

// MarshalJSON writes the severity's name.
func (s Severity) MarshalJSON() ([]byte, error) { return json.Marshal(s.String()) }

// UnmarshalJSON reads a severity's name.
func (s *Severity) UnmarshalJSON(b []byte) error {
	var name string
	if err := json.Unmarshal(b, &name); err != nil {
		return err
	}
	sev, ok := ParseSeverity(name)
	if !ok {
		return fmt.Errorf("unknown severity %q", name)
	}
	*s = sev
	return nil
}

// MoreSevereThan reports whether s outranks o.
func (s Severity) MoreSevereThan(o Severity) bool { return s < o }

// HitID is rule + "~" + subject key; it never contains display text.
type HitID string

func hitID(rule RuleID, s Subject) HitID { return HitID(fmt.Sprintf("%s~%s", rule, s.subjectKey())) }

// Rule is the rule the id names, the part before "~".
func (id HitID) Rule() RuleID {
	rule, _, _ := strings.Cut(string(id), "~")
	return RuleID(rule)
}

// ParseSeverity reads a severity's name back.
func ParseSeverity(s string) (Severity, bool) {
	for sev, name := range severityNames {
		if name == s {
			return sev, true
		}
	}
	return 0, false
}

// Evidence is the structured facts a hit's text renders from.
type Evidence map[string]any

// Link is where a hit sends the human.
type Link struct {
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
}

// Hit is one rule finding about one subject.
type Hit struct {
	ID       HitID        `json:"id"`
	Rule     RuleID       `json:"rule"`
	Subject  Subject      `json:"-"`
	Severity Severity     `json:"severity"`
	Train    *TrainID     `json:"train,omitempty"`
	App      AppID        `json:"app,omitempty"`
	AgesOn   *Day         `json:"agesOn,omitempty"`
	Evidence Evidence     `json:"evidence"`
	Text     string       `json:"text"`
	Link     Link         `json:"link"`
	AsOf     time.Time    `json:"asOf"`
	Mine     []MineReason `json:"mine,omitempty"`
}

// Fingerprint hashes the evidence, so a hide can tell when the facts behind a hit changed.
func (h Hit) Fingerprint() string {
	b, err := json.Marshal(h.Evidence)
	if err != nil {
		return ""
	}
	sum := sha256.Sum256(b)
	return hex.EncodeToString(sum[:8])
}
