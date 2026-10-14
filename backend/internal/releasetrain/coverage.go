package releasetrain

import "time"

// Outcome is one rule's result for one cell: Clean, Fired or Unknown, built only by evaluate.
type Outcome interface{ outcome() }

// Clean means every declared read was fresh and the rule found nothing.
type Clean struct {
	CheckedAt time.Time `json:"checkedAt"`
}

// Fired means the rule found hits; Degraded lists the reads that were not fresh.
type Fired struct {
	Hits     []Hit            `json:"hits"`
	AsOf     time.Time        `json:"asOf"`
	Degraded []ResourceStatus `json:"degraded,omitempty"`
}

// Unknown means the rule found nothing but could not see everything it reads.
type Unknown struct {
	Missing []ResourceStatus `json:"missing"`
}

func (Clean) outcome()   {}
func (Fired) outcome()   {}
func (Unknown) outcome() {}

// Alerts is the rule outcomes, the hits with their bucket, and the badge.
type Alerts struct {
	Badge Badge         `json:"badge"`
	Hits  []AlertHit    `json:"hits"`
	Rules []RuleOutcome `json:"rules"`
}

// Badge counts active hits and unknown (rule, app) cells of enabled rules.
type Badge struct {
	Count   int       `json:"count"`
	Worst   *Severity `json:"worst,omitempty"`
	Unknown int       `json:"unknown"`
}

// Bucket is where a hit is listed.
type Bucket string

// Buckets a hit can land in.
const (
	BucketBadge   Bucket = "badge"
	BucketHistory Bucket = "history"
	BucketHidden  Bucket = "hidden"
)

// AlertHit is a hit of an enabled rule with its bucket.
type AlertHit struct {
	Hit
	Bucket             Bucket `json:"bucket"`
	ChangedSinceHidden bool   `json:"changedSinceHidden,omitempty"`
}

// RuleOutcome is one rule's outcome for one app, or for every app when App is empty.
type RuleOutcome struct {
	Rule    RuleID  `json:"rule"`
	App     AppID   `json:"app,omitempty"`
	On      bool    `json:"on"`
	Outcome Outcome `json:"outcome"`
}
