package releasetrain

import (
	"fmt"
	"strings"
)

const slowPipelineMinutes = 60

var semgrepNotPassed = map[string]string{"missing": "is missing", "failed": "failed", "canceled": "was canceled", "skipped": "was skipped"}

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
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s release pipeline failed: %s", t.ID.short(), joinNames(failed)),
				Do:   failedJobsDo(len(failed)),
			}, Evidence{"mr": r.MR.IID, "jobs": failed}).fix("Retry job", pipelineLink(r)).dueAt(b.Today, dueOf(t, v), DuePublish)
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
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s release pipeline: %s failed, allowed to fail", t.ID.short(), joinNames(allowed)),
				Do:   "Nothing to do unless that job matters for this release.",
			}, Evidence{"mr": r.MR.IID, "jobs": allowed}).open(pipelineLink(r))
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
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s release pipeline took %s", t.ID.short(), plural(minutes, "minute")),
				Do:   "Nothing to do now. Look for a slow job if it happens again.",
			}, Evidence{"mr": r.MR.IID, "minutes": minutes}).open(pipelineLink(r))
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
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s release MR !%d has merge conflicts", t.ID.short(), r.MR.IID),
				Do:   "Resolve the conflicts on the release branch.",
			}, Evidence{"mr": r.MR.IID}).open(Link{Label: "Open release MR", URL: r.MR.URL}).dueAt(b.Today, dueOf(t, v), DuePublish)
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
			if semgrepNotPassed[state] == "" {
				return nil
			}
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("Train %s release pipeline: semgrep-sast %s", t.ID.short(), semgrepNotPassed[state]),
				Do:   "Make it run and pass. It is skipped when the build fails first, so retry the build.",
			}, Evidence{"mr": r.MR.IID, "semgrep": state}).fix("Retry build", pipelineLink(r)).dueAt(b.Today, dueOf(t, v), DuePublish)
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
			n := len(v.Tag.MergesAfter)
			titles := make([]string, 0, n)
			for _, c := range v.Tag.MergesAfter {
				titles = append(titles, c.Title)
			}
			return versionHit(t, v, Copy{
				What: fmt.Sprintf("%s merged into main after tag %s", plural(n, "branch"), v.Tag.Name),
				Do:   fmt.Sprintf("Tag a new version, or confirm %s can wait for the next train.", itThem(n)),
			}, Evidence{"tag": v.Tag.Name, "merges": titles}).open(Link{Label: "Compare", URL: v.Tag.CompareURL})
		})
	},
}

func failedJobsDo(n int) string {
	if n == 1 {
		return "Fix the job, or retry it once if it looks flaky."
	}
	return "Fix the jobs, or retry them once if they look flaky."
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
