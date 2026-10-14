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

// Unknown means the rule could not trust what it saw: a read never completed, or it found nothing in a stale read.
type Unknown struct {
	Missing []ResourceStatus `json:"missing"`
}

func (Clean) outcome()   {}
func (Fired) outcome()   {}
func (Unknown) outcome() {}

type coverage struct {
	asOf     time.Time
	blind    []ResourceStatus
	degraded []ResourceStatus
}

func (c coverage) fresh() bool { return len(c.blind) == 0 && len(c.degraded) == 0 }

func (c coverage) missing() []ResourceStatus {
	return append(append([]ResourceStatus{}, c.blind...), c.degraded...)
}

func evaluate(hits []Hit, cov coverage) Outcome {
	if len(cov.blind) > 0 {
		return Unknown{Missing: cov.missing()}
	}
	if len(hits) > 0 {
		for i := range hits {
			hits[i].AsOf = cov.asOf
		}
		return Fired{Hits: hits, AsOf: cov.asOf, Degraded: cov.degraded}
	}
	if len(cov.degraded) > 0 {
		return Unknown{Missing: cov.degraded}
	}
	return Clean{CheckedAt: cov.asOf}
}

type readSet struct {
	snap   Snapshot
	cfg    Config
	now    time.Time
	today  Day
	ageOut int
}

// SettledBudget is how often a settled (aged-out) version's per-entity reads refetch.
const SettledBudget = 6 * time.Hour

func (r *readSet) live(due *Day) bool { return due == nil || r.today.Sub(*due) <= r.ageOut }

// liveEntities is every per-entity resource a live version reads; any other
// per-entity resource belongs to a settled version and is fresh for SettledBudget.
func (r *readSet) liveEntities(trains []Train) map[resKey]bool {
	out := map[resKey]bool{}
	for i := range trains {
		for _, v := range trains[i].Versions {
			if !r.live(v.Milestone.Due) {
				continue
			}
			for name, meta := range kinds {
				if meta.cover != perEntity {
					continue
				}
				for _, scope := range entityScopes(name, v, r.cfg.app(v.App).GitLabProject) {
					out[resKey{name, scope}] = true
				}
			}
		}
	}
	return out
}

func entityScopes(kind KindName, v *AppVersion, project int) []Scope {
	switch kind {
	case KindMRDetail.Name:
		if v.ReleaseMR != nil {
			return []Scope{MRScope(project, v.ReleaseMR.MR.IID)}
		}
	case KindJobs.Name:
		if v.ReleaseMR != nil && v.ReleaseMR.Detail != nil && v.ReleaseMR.Detail.Pipeline != nil {
			return []Scope{PipelineScope(project, v.ReleaseMR.Detail.Pipeline.ID)}
		}
	case KindCompare.Name:
		if v.Tag != nil {
			return []Scope{CompareScope(project, v.Tag.Name, "main")}
		}
	case KindRelatedWork.Name, KindCards.Name:
		if v.Jira != nil {
			return []Scope{VersionScope(v.Jira.ID)}
		}
	}
	return nil
}

func (r *readSet) coverageFor(reads []KindName, app AppID, versions []*AppVersion) coverage {
	var cov coverage
	note := func(st ResourceStatus, entity bool) {
		switch {
		case st.CompleteAt == nil && !entity:
			cov.blind = append(cov.blind, st)
		case st.CompleteAt == nil, r.now.Sub(*st.CompleteAt) > st.budget():
			cov.degraded = append(cov.degraded, st)
		}
		if st.CompleteAt != nil && (cov.asOf.IsZero() || st.CompleteAt.Before(cov.asOf)) {
			cov.asOf = *st.CompleteAt
		}
	}
	for _, k := range reads {
		switch kinds[k].cover {
		case perApp:
			for _, a := range r.cfg.Apps {
				if app != "" && a.ID != app {
					continue
				}
				scope := ProjectScope(a.GitLabProject)
				st, ok := r.snap.Status(k, scope)
				if !ok {
					st = ResourceStatus{Kind: k, Scope: scope, App: a.ID, ErrorClass: ErrUnconfigured}
				}
				note(st, false)
			}
		case global:
			sts := r.snap.Statuses(k)
			if len(sts) == 0 {
				sts = []ResourceStatus{{Kind: k, ErrorClass: ErrUnconfigured}}
			}
			for _, st := range sts {
				note(st, false)
			}
		case perEntity:
			for _, v := range versions {
				if !r.live(v.Milestone.Due) {
					continue
				}
				a := r.cfg.app(v.App)
				for _, scope := range entityScopes(k, v, a.GitLabProject) {
					st, ok := r.snap.Status(k, scope)
					if !ok {
						st = ResourceStatus{Kind: k, Scope: scope, App: v.App}
					}
					note(st, true)
				}
			}
		}
	}
	return cov
}

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
