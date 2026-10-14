package releasetrain

import (
	"sort"
	"strings"
)

// Reconcile joins the observed facts into a Board as of env.Now.
func Reconcile(in Inputs, env Env) Board {
	b := Board{Today: DayOf(env.Now, env.Loc), Apps: boardApps(in.Config), Hotfixes: []Hotfix{}, Mismatches: []Mismatch{}}
	rs := &readSet{snap: in.Snap, cfg: in.Config, now: env.Now, today: b.Today, ageOut: env.AgeOut}
	joinTrains(&b, in.Config, in.Snap)
	if b.Trains == nil {
		b.Trains = []Train{}
	}
	b.Sources = rs.sources(b.Trains)
	phases(b.Trains, b.Today)
	branches, cutover := detectLLBs(in.Config, in.Snap)
	b.LLBs, b.Cutover = groupLLBs(branches, in.Human.Plans, b.Trains, in.Config), cutover
	mrs, llbs := indexMRs(in.Config, in.Snap), llbsByApp(b.LLBs)
	for i := range b.Trains {
		t := &b.Trains[i]
		t.DatesSplit = datesSplit(t)
		fromPages := pageMembers(t, in.Config, in.Snap, mrs)
		for _, v := range t.Versions {
			v.Cards = cardUnits(t, v, mrs, fromPages[v.App], llbs[v.App])
			v.Stages = stages(t, v, b.Today, rs)
		}
	}
	b.Identity = identityOf(in.Snap)
	mine(&b, meOf(b.Identity, in.Snap), mrs, in.Snap, in.Config)
	b.Alerts = alerts(&b, in, rs)
	b.Catalogue = catalogue(in.Human.RuleSettings)
	return b
}

func joinTrains(b *Board, cfg Config, snap Snapshot) {
	trains := map[TrainID]*Train{}
	for _, app := range cfg.Apps {
		fam := cfg.familyOf(app.ID)
		milestones := Items(snap, KindMilestones, ProjectScope(app.GitLabProject))
		sort.Slice(milestones, func(i, j int) bool { return milestones[i].ID < milestones[j].ID })
		for _, ms := range milestones {
			version := ""
			if v, ok := ParseVersion(ms.Title); ok {
				version = v.String()
			}
			_, id, labelled := ParseTrainLabel(ms.Description)
			if !labelled {
				if version != "" {
					b.Hotfixes = append(b.Hotfixes, Hotfix{App: app.ID, Version: version, Milestone: ms})
				}
				continue
			}
			t := trains[id]
			if t == nil {
				t = &Train{ID: id}
				trains[id] = t
			}
			if t.version(app.ID) != nil {
				continue
			}
			if !containsFamily(t.Families, fam.ID) {
				t.Families = append(t.Families, fam.ID)
			}
			t.Freeze = minDay(t.Freeze, ms.Start)
			t.Publish = maxDay(t.Publish, ms.Due)
			t.Versions = append(t.Versions, &AppVersion{Train: id, App: app.ID, Version: version, Milestone: ms})
		}
	}
	for _, t := range trains {
		b.Trains = append(b.Trains, *t)
	}
	sort.Slice(b.Trains, func(i, j int) bool { return b.Trains[i].ID.less(b.Trains[j].ID) })

	joinJira(b, cfg, snap)
	attachPages(b, cfg, snap)
	for i := range b.Trains {
		for _, v := range b.Trains[i].Versions {
			if v.Jira == nil {
				ms := v.Milestone
				train := v.Train
				b.Mismatches = append(b.Mismatches, Mismatch{Kind: MismatchNoJira, App: v.App, Train: &train, Milestone: &ms, Version: v.Version})
			}
			app := cfg.app(v.App)
			joinGitLab(v, app, snap)
			joinVersionFacts(v, snap)
			v.ForceUpdate = forceUpdateFor(app, v.Version, snap)
		}
	}
	for i := range b.Hotfixes {
		h := &b.Hotfixes[i]
		app := cfg.app(h.App)
		h.Tag = tagFor(app, h.Version, snap)
		h.ForceUpdate = forceUpdateFor(app, h.Version, snap)
	}
}

func containsFamily(fs []FamilyID, f FamilyID) bool {
	for _, x := range fs {
		if x == f {
			return true
		}
	}
	return false
}

func (t *Train) version(app AppID) *AppVersion {
	for _, v := range t.Versions {
		if v.App == app {
			return v
		}
	}
	return nil
}

func (b *Board) train(id TrainID) *Train {
	for i := range b.Trains {
		if b.Trains[i].ID == id {
			return &b.Trains[i]
		}
	}
	return nil
}

func joinJira(b *Board, cfg Config, snap Snapshot) {
	versions := AllItems(snap, KindJiraVersions)
	sort.SliceStable(versions, func(i, j int) bool { return versions[i].ID < versions[j].ID })
	for i := range versions {
		jv := withApproverNames(versions[i], snap)
		app, ok := appForJiraName(cfg, jv.Name)
		if !ok {
			continue
		}
		version := ""
		if v, ok := ParseVersion(strings.TrimPrefix(jv.Name, app.JiraVersionPrefix)); ok {
			version = v.String()
		}
		_, id, labelled := ParseTrainLabel(jv.Description)
		if !labelled {
			if !attachHotfixJira(b, app.ID, version, &jv) {
				b.Mismatches = append(b.Mismatches, Mismatch{Kind: MismatchNoLabel, App: app.ID, Jira: &jv, Version: version})
			}
			continue
		}
		var v *AppVersion
		if t := b.train(id); t != nil {
			v = t.version(app.ID)
		}
		if v == nil {
			train := id
			b.Mismatches = append(b.Mismatches, Mismatch{Kind: MismatchNoMilestone, App: app.ID, Train: &train, Jira: &jv, Version: version})
			continue
		}
		if v.Jira != nil {
			continue
		}
		v.Jira = &jv
		if v.Version != version {
			train := id
			ms := v.Milestone
			b.Mismatches = append(b.Mismatches, Mismatch{Kind: MismatchVersion, App: app.ID, Train: &train, Milestone: &ms, Jira: &jv, Version: version})
		}
	}
}

func withApproverNames(jv JiraVersionFact, snap Snapshot) JiraVersionFact {
	approvers := make([]Approver, len(jv.Approvers))
	for i, a := range jv.Approvers {
		if a.Name == "" {
			a.Name = a.AccountID
			for _, u := range Items(snap, KindJiraUsers, UserScope(a.AccountID)) {
				a.Name = u.DisplayName
			}
		}
		approvers[i] = a
	}
	jv.Approvers = approvers
	return jv
}

func attachHotfixJira(b *Board, app AppID, version string, jv *JiraVersionFact) bool {
	matched := false
	for i := range b.Hotfixes {
		h := &b.Hotfixes[i]
		if h.App == app && version != "" && h.Version == version {
			h.Jira = jv
			matched = true
		}
	}
	return matched
}

func appForJiraName(cfg Config, name string) (App, bool) {
	best, found := App{}, false
	for _, a := range cfg.Apps {
		if strings.HasPrefix(name, a.JiraVersionPrefix+" ") && (!found || len(a.JiraVersionPrefix) > len(best.JiraVersionPrefix)) {
			best, found = a, true
		}
	}
	return best, found
}

func joinGitLab(v *AppVersion, app App, snap Snapshot) {
	v.ReleaseMR = releaseMRFor(v.Milestone, app, snap)
	v.Tag = tagFor(app, v.Version, snap)
}

func releaseMRFor(ms MilestoneFact, app App, snap Snapshot) *ReleaseMR {
	var linked []MRFact
	for _, mr := range Items(snap, KindMRs, ProjectScope(app.GitLabProject)) {
		if mr.MilestoneID != nil && *mr.MilestoneID == ms.ID && mr.TargetBranch == "main" && strings.HasPrefix(mr.SourceBranch, "release") {
			linked = append(linked, mr)
		}
	}
	if len(linked) == 0 {
		return nil
	}
	sort.Slice(linked, func(i, j int) bool {
		if linked[i].CreatedOn != linked[j].CreatedOn {
			return linked[i].CreatedOn.Before(linked[j].CreatedOn)
		}
		return linked[i].IID < linked[j].IID
	})
	r := &ReleaseMR{MR: linked[0]}
	for _, d := range Items(snap, KindMRDetail, MRScope(app.GitLabProject, r.MR.IID)) {
		r.Detail = &d
	}
	if r.Detail != nil && r.Detail.Pipeline != nil {
		for _, j := range Items(snap, KindJobs, PipelineScope(app.GitLabProject, r.Detail.Pipeline.ID)) {
			r.Jobs = &j
		}
	}
	return r
}

func tagFor(app App, version string, snap Snapshot) *Tag {
	if version == "" {
		return nil
	}
	want, _ := parseExactVersion(version)
	var found *Tag
	newest := true
	for _, tf := range Items(snap, KindTags, ProjectScope(app.GitLabProject)) {
		tv, ok := parseExactVersion(tf.Name)
		if !ok {
			continue
		}
		if tf.Name == version {
			found = &Tag{TagFact: tf}
		} else if tv.Compare(want) > 0 {
			newest = false
		}
	}
	if found == nil {
		return nil
	}
	found.Newest = newest
	for _, c := range Items(snap, KindCompare, CompareScope(app.GitLabProject, found.Name, "main")) {
		found.CompareURL = c.URL
		found.MergesAfter = []CommitFact{}
		for _, commit := range c.Commits {
			if strings.HasPrefix(commit.Title, "Merge branch") && !strings.HasPrefix(commit.Title, "Merge branch 'release/") {
				found.MergesAfter = append(found.MergesAfter, commit)
			}
		}
	}
	return found
}

func joinVersionFacts(v *AppVersion, snap Snapshot) {
	v.Issues = []IssueFact{}
	if v.Jira == nil {
		return
	}
	scope := VersionScope(v.Jira.ID)
	for _, rw := range Items(snap, KindRelatedWork, scope) {
		v.RelatedWork = &rw
	}
	for _, is := range Items(snap, KindCards, scope) {
		switch {
		case is.ReleaseTicket:
			if v.Ticket == nil {
				is := is
				v.Ticket = &is
			}
		case !is.Subtask:
			v.Issues = append(v.Issues, is)
		}
	}
	sort.Slice(v.Issues, func(i, j int) bool { return v.Issues[i].Key < v.Issues[j].Key })
}

func forceUpdateFor(app App, version string, snap Snapshot) *ForceUpdateFact {
	if version == "" {
		return nil
	}
	var found *ForceUpdateFact
	for _, fu := range AllItems(snap, KindTechReq) {
		if fu.Platform == app.Platform && fu.Version == version && (found == nil || fu.Key < found.Key) {
			fu := fu
			found = &fu
		}
	}
	return found
}
