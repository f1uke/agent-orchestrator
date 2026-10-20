package releasetrain

import (
	"fmt"
	"strings"
)

const slowPipelineMinutes = 60

var semgrepNotPassed = map[string]bool{"missing": true, "failed": true, "canceled": true, "skipped": true}

var ruleC1 = rule{
	ID: "C1", Group: "C", Severity: SevBlock, DefaultOn: true, Scope: scopePerApp, Reads: readsReleaseJobs,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Title:    "Release MR pipeline failed on a required job",
	Check: func(b *Board) []Hit {
		return openReleaseMRHits(b, func(t *Train, v *AppVersion, r *ReleaseMR) *Hit {
			failed := r.failedRequired()
			if len(failed) == 0 {
				return nil
			}
			return versionHit(t, v, fmt.Sprintf("!%d failed: %s", r.MR.IID, joinNames(failed)),
				Evidence{"mr": r.MR.IID, "jobs": failed}, pipelineLink(r))
		})
	},
}

var ruleC2 = rule{
	ID: "C2", Group: "C", Severity: SevInfo, DefaultOn: false, Scope: scopePerApp, Reads: readsReleaseJobs,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Title:    "Release MR pipeline failed only on allowed-to-fail jobs",
	Check: func(b *Board) []Hit {
		return openReleaseMRHits(b, func(t *Train, v *AppVersion, r *ReleaseMR) *Hit {
			allowed := r.failedAllowed()
			if len(allowed) == 0 {
				return nil
			}
			return versionHit(t, v, fmt.Sprintf("!%d allowed failures: %s", r.MR.IID, joinNames(allowed)),
				Evidence{"mr": r.MR.IID, "jobs": allowed}, pipelineLink(r))
		})
	},
}

var ruleC3 = rule{
	ID: "C3", Group: "C", Severity: SevInfo, DefaultOn: false, Scope: scopePerApp, Reads: readsReleaseMR,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Title:    "Release pipeline took longer than 60 minutes",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			r := v.ReleaseMR
			if r == nil || r.Detail == nil || r.Detail.Pipeline == nil || r.Detail.Pipeline.Duration == nil {
				return nil
			}
			minutes := (*r.Detail.Pipeline.Duration + 30) / 60
			if minutes <= slowPipelineMinutes {
				return nil
			}
			return versionHit(t, v, fmt.Sprintf("!%d pipeline took %d min", r.MR.IID, minutes),
				Evidence{"mr": r.MR.IID, "minutes": minutes}, pipelineLink(r))
		})
	},
}

var ruleC5 = rule{
	ID: "C5", Group: "C", Severity: SevBlock, DefaultOn: true, Scope: scopePerApp, Reads: readsReleaseMR,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Title:    "Release MR has conflicts",
	Check: func(b *Board) []Hit {
		return openReleaseMRHits(b, func(t *Train, v *AppVersion, r *ReleaseMR) *Hit {
			if r.Detail == nil || !r.Detail.Conflicts {
				return nil
			}
			return versionHit(t, v, fmt.Sprintf("!%d has merge conflicts", r.MR.IID),
				Evidence{"mr": r.MR.IID}, Link{Label: "Open release MR", URL: r.MR.URL})
		})
	},
}

var ruleC6 = rule{
	ID: "C6", Group: "C", Severity: SevBlock, DefaultOn: true, Scope: scopePerApp, Reads: readsReleaseJobs,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Title:    "semgrep-sast missing or not passed on the release pipeline",
	Check: func(b *Board) []Hit {
		return openReleaseMRHits(b, func(t *Train, v *AppVersion, r *ReleaseMR) *Hit {
			if r.Jobs == nil {
				return nil
			}
			state := "missing"
			for _, j := range r.Jobs.Jobs {
				if j.Name == "semgrep-sast" {
					state = j.Status
				}
			}
			if !semgrepNotPassed[state] {
				return nil
			}
			return versionHit(t, v, fmt.Sprintf("semgrep-sast is %s on !%d", state, r.MR.IID),
				Evidence{"mr": r.MR.IID, "semgrep": state}, pipelineLink(r))
		})
	},
}

var ruleC7 = rule{
	ID: "C7", Group: "C", Severity: SevRisk, DefaultOn: true, Scope: scopePerApp,
	Subjects: []SubjectKind{SubjectVersion},
	Mine:     MineOwner,
	Reads:    []KindName{KindMilestones.Name, KindTags.Name, KindCompare.Name},
	Title:    "Branches merged into main after the version tag",
	Check: func(b *Board) []Hit {
		return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
			if v.Tag == nil || !v.Tag.Newest || len(v.Tag.MergesAfter) == 0 {
				return nil
			}
			parts := make([]string, 0, len(v.Tag.MergesAfter))
			titles := make([]string, 0, len(v.Tag.MergesAfter))
			for _, c := range v.Tag.MergesAfter {
				parts = append(parts, fmt.Sprintf("%s (%s)", mergedBranch(c.Title), c.CreatedOn.label()))
				titles = append(titles, c.Title)
			}
			return versionHit(t, v, fmt.Sprintf("After tag %s: %s", v.Tag.Name, joinNames(parts)),
				Evidence{"tag": v.Tag.Name, "merges": titles}, Link{Label: "Compare", URL: v.Tag.CompareURL})
		})
	},
}

func mergedBranch(title string) string {
	rest, ok := strings.CutPrefix(title, "Merge branch '")
	if !ok {
		return title
	}
	if branch, _, ok := strings.Cut(rest, "'"); ok {
		return branch
	}
	return title
}

func openReleaseMRHits(b *Board, fn func(t *Train, v *AppVersion, r *ReleaseMR) *Hit) []Hit {
	return forEachVersion(b, func(t *Train, v *AppVersion) *Hit {
		if v.ReleaseMR == nil || v.ReleaseMR.MR.State != MROpened {
			return nil
		}
		return fn(t, v, v.ReleaseMR)
	})
}

func pipelineLink(r *ReleaseMR) Link {
	if r.Detail == nil || r.Detail.Pipeline == nil {
		return Link{Label: "Open release MR", URL: r.MR.URL}
	}
	return Link{Label: "Open pipeline", URL: r.Detail.Pipeline.URL}
}
