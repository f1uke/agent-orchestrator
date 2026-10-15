package releasetrain

import (
	"fmt"
	"strconv"
	"strings"
	"testing"
	"time"
)

const fixtureYear = 2026

func fixtureConfig() Config {
	return Config{
		Timezone:    "Asia/Bangkok",
		HistoryFrom: Day{fixtureYear, time.April, 1},
		AgeOutDays:  7,
		FixWrites:   "off",
		GitLabHost:  "gitlab.example.com",
		Jira:        JiraConfig{Project: "DEMO", TechRequestProject: "DEMOREQ", ForceUpdateSummary: "force update"},
		Boards:      []BoardConfig{{ID: 42, Key: "DEMO", OwnsSprintBranch: true}, {ID: 43, Key: "OTHER"}},
		Mobile:      MobileConfig{Projects: []string{"DEMO"}, SubtaskPattern: `^(iOS|Android)\b`},
		Families: []Family{
			{ID: "demo", Label: "Demo", TrainLabel: "Release Train", Apps: []AppID{"di", "da"}},
			{ID: "pilot", Label: "Pilot", TrainLabel: "Pilot Release Train", Apps: []AppID{"pi", "pa"}},
		},
		Apps: []App{
			{ID: "di", Name: "Demo iOS", Platform: PlatformIOS, GitLabProject: 1001, JiraVersionPrefix: "iOS"},
			{ID: "da", Name: "Demo Android", Platform: PlatformAndroid, GitLabProject: 1002, JiraVersionPrefix: "Android"},
			{ID: "pi", Name: "Pilot iOS", Platform: PlatformIOS, GitLabProject: 1003, JiraVersionPrefix: "iOS Pilot"},
			{ID: "pa", Name: "Pilot Android", Platform: PlatformAndroid, GitLabProject: 1004, JiraVersionPrefix: "Android Pilot"},
		},
	}
}

type fixture struct {
	t          *testing.T
	cfg        Config
	loc        *time.Location
	now        time.Time
	nextID     int
	milestones []*MilestoneFact
	mrs        []*MRFact
	details    []*MRDetailFact
	jobs       []*JobsFact
	tags       []*TagFact
	compares   []*CompareFact
	versions   []*JiraVersionFact
	related    []*RelatedWorkFact
	issues     map[string][]*IssueFact
	techreq    []*ForceUpdateFact
	pages      []*PageFact
	pageCards  map[string][]*IssueFact
	myself     *JiraMyselfFact
	gitlabUser *GitLabUserFact
	subtasks   []*IssueFact
	llbCards   []*IssueFact
	sprints    []*SprintFact
	sprintIss  map[int][]*IssueFact
	thisSprint map[int][]string
	branchIss  []*IssueFact
	versionApp map[string]AppID
	statuses   map[KindName]func(*ResourceStatus)
	human      HumanState
}

func newFixture(t *testing.T, today string) *fixture {
	t.Helper()
	cfg := fixtureConfig()
	loc, err := cfg.Validate()
	if err != nil {
		t.Fatalf("fixture config: %v", err)
	}
	d := day(t, today)
	return &fixture{
		t: t, cfg: cfg, loc: loc,
		now:        time.Date(d.Y, d.M, d.D, 10, 0, 0, 0, loc),
		nextID:     100,
		issues:     map[string][]*IssueFact{},
		pageCards:  map[string][]*IssueFact{},
		sprintIss:  map[int][]*IssueFact{},
		thisSprint: map[int][]string{},
		versionApp: map[string]AppID{},
		statuses:   map[KindName]func(*ResourceStatus){},
		human:      HumanState{RuleSettings: map[RuleID]bool{}, Hides: map[HitID]Hide{}},
	}
}

func day(t *testing.T, s string) Day {
	t.Helper()
	d, err := ParseDay(s)
	if err != nil {
		t.Fatalf("ParseDay(%q): %v", s, err)
	}
	return d
}

func dayPtr(t *testing.T, s string) *Day {
	t.Helper()
	d := day(t, s)
	return &d
}

func (f *fixture) id() int {
	f.nextID++
	return f.nextID
}

func (f *fixture) app(id AppID) App {
	for _, a := range f.cfg.Apps {
		if a.ID == id {
			return a
		}
	}
	f.t.Fatalf("no app %q", id)
	return App{}
}

func (f *fixture) family(id AppID) Family {
	for _, fam := range f.cfg.Families {
		for _, a := range fam.Apps {
			if a == id {
				return fam
			}
		}
	}
	f.t.Fatalf("no family for %q", id)
	return Family{}
}

func (f *fixture) env() Env { return Env{Now: f.now, Loc: f.loc, AgeOut: f.cfg.AgeOutDays} }

func (f *fixture) reconcile() Board { return Reconcile(f.inputs(), f.env()) }

func (f *fixture) inputs() Inputs { return Inputs{Config: f.cfg, Snap: f.snapshot(), Human: f.human} }

func (f *fixture) fresh(kind KindName, scope Scope, app AppID) ResourceStatus {
	at := f.now.Add(-30 * time.Second)
	st := ResourceStatus{Kind: kind, Scope: scope, App: app, CompleteAt: &at, LastAttemptAt: &at}
	if mutate, ok := f.statuses[kind]; ok {
		mutate(&st)
	}
	return st
}

func (f *fixture) stale(kind KindName) {
	f.statuses[kind] = func(st *ResourceStatus) {
		at := f.now.Add(-24 * time.Hour)
		st.CompleteAt = &at
		st.ErrorClass = ErrUnavailable
		st.Failures = 3
	}
}

func (f *fixture) neverComplete(kind KindName) {
	f.statuses[kind] = func(st *ResourceStatus) {
		st.CompleteAt = nil
		st.ErrorClass = ErrAuth
	}
}

func (f *fixture) snapshot() Snapshot {
	var s Snapshot
	for _, a := range f.cfg.Apps {
		scope := ProjectScope(a.GitLabProject)
		Put(&s, KindMilestones, f.fresh(KindMilestones.Name, scope, a.ID), deref(f.milestones, func(m *MilestoneFact) bool { return m.Project == a.GitLabProject }))
		Put(&s, KindMRs, f.fresh(KindMRs.Name, scope, a.ID), deref(f.mrs, func(m *MRFact) bool { return m.Project == a.GitLabProject }))
		Put(&s, KindTags, f.fresh(KindTags.Name, scope, a.ID), deref(f.tags, func(m *TagFact) bool { return m.Project == a.GitLabProject }))
	}
	Put(&s, KindJiraVersions, f.fresh(KindJiraVersions.Name, "project", ""), deref(f.versions, nil))
	Put(&s, KindTechReq, f.fresh(KindTechReq.Name, "techreq", ""), deref(f.techreq, nil))
	for _, d := range f.details {
		Put(&s, KindMRDetail, f.fresh(KindMRDetail.Name, MRScope(d.Project, d.IID), f.appOf(d.Project)), []MRDetailFact{*d})
	}
	for _, j := range f.jobs {
		Put(&s, KindJobs, f.fresh(KindJobs.Name, PipelineScope(j.Project, j.Pipeline), f.appOf(j.Project)), []JobsFact{*j})
	}
	for _, c := range f.compares {
		Put(&s, KindCompare, f.fresh(KindCompare.Name, CompareScope(c.Project, c.From, c.To), f.appOf(c.Project)), []CompareFact{*c})
	}
	for _, r := range f.related {
		Put(&s, KindRelatedWork, f.fresh(KindRelatedWork.Name, VersionScope(r.VersionID), f.appOfVersion(r.VersionID)), []RelatedWorkFact{*r})
	}
	for _, v := range f.versions {
		Put(&s, KindCards, f.fresh(KindCards.Name, VersionScope(v.ID), f.appOfVersion(v.ID)), deref(f.issues[v.ID], nil))
	}
	Put(&s, KindPages, f.fresh(KindPages.Name, SpaceScope("DEMOSPACE"), ""), deref(f.pages, nil))
	for _, p := range f.pages {
		Put(&s, KindPageCards, f.fresh(KindPageCards.Name, PageScope(p.ID), ""), deref(f.pageCards[p.ID], nil))
	}
	if f.myself != nil {
		Put(&s, KindJiraMyself, f.fresh(KindJiraMyself.Name, MyselfScope, ""), []JiraMyselfFact{*f.myself})
	}
	if f.gitlabUser != nil {
		Put(&s, KindGitLabUser, f.fresh(KindGitLabUser.Name, MyselfScope, ""), []GitLabUserFact{*f.gitlabUser})
	}
	Put(&s, KindMySubtasks, f.fresh(KindMySubtasks.Name, MySubtasksScope, ""), deref(f.subtasks, nil))
	Put(&s, KindLLBCards, f.fresh(KindLLBCards.Name, LLBCardsScope, ""), deref(f.llbCards, nil))
	for _, b := range f.cfg.Boards {
		Put(&s, KindBoardSprints, f.fresh(KindBoardSprints.Name, BoardScope(b.ID), ""), deref(f.sprints, func(sp *SprintFact) bool { return sp.Board == b.ID }))
	}
	for _, sp := range f.sprints {
		Put(&s, KindSprintCards, f.fresh(KindSprintCards.Name, SprintScope(sp.ID), ""), deref(f.sprintIss[sp.ID], nil))
		members := []SprintMemberFact{}
		for _, k := range f.thisSprint[sp.ID] {
			members = append(members, SprintMemberFact{Key: k})
		}
		Put(&s, KindThisSprint, f.fresh(KindThisSprint.Name, SprintScope(sp.ID), ""), members)
	}
	Put(&s, KindBranchCards, f.fresh(KindBranchCards.Name, BranchCardsScope, ""), deref(f.branchIss, nil))
	return s
}

// me makes the tokens belong to Dee: Jira account acc-dee, GitLab user dee.
func (f *fixture) me() {
	f.myself = &JiraMyselfFact{AccountID: "acc-dee", DisplayName: "Dee"}
	f.gitlabUser = &GitLabUserFact{Username: "dee", Name: "Dee"}
}

var dee = &Person{AccountID: "acc-dee", Name: "Dee"}

// page adds family's train page created on created, with the dates its table gives.
func (f *fixture) page(family FamilyID, train, created, freeze, publish string) *PageFact {
	var number, extra int
	if _, err := fmt.Sscanf(train, "%d-%d", &number, &extra); err != nil {
		number, _ = strconv.Atoi(train)
	}
	id := strconv.Itoa(8000 + f.id())
	p := &PageFact{
		ID: id, Title: fmt.Sprintf("Train page #%s", train), URL: "https://wiki.example.com/pages/" + id,
		Created: day(f.t, created), Family: family, Number: number, Extra: extra, Keys: []string{},
	}
	if freeze != "" {
		p.Freeze = dayPtr(f.t, freeze)
	}
	if publish != "" {
		p.Publish = dayPtr(f.t, publish)
	}
	f.pages = append(f.pages, p)
	return p
}

// card lists key on the page, as Jira reads it, in the given fixVersions.
func (f *fixture) card(p *PageFact, key string, fixVersions ...string) *IssueFact {
	is := &IssueFact{
		Key: key, Summary: "Card " + key, Status: "In Progress", StatusCategory: "indeterminate",
		FixVersions: fixVersions, URL: "https://jira.example.com/browse/" + key,
	}
	p.Keys = append(p.Keys, key)
	f.pageCards[p.ID] = append(f.pageCards[p.ID], is)
	return is
}

func deref[T any](items []*T, keep func(*T) bool) []T {
	out := []T{}
	for _, it := range items {
		if keep == nil || keep(it) {
			out = append(out, *it)
		}
	}
	return out
}

func (f *fixture) appOf(project int) AppID {
	for _, a := range f.cfg.Apps {
		if a.GitLabProject == project {
			return a.ID
		}
	}
	return ""
}

func (f *fixture) appOfVersion(id string) AppID { return f.versionApp[id] }

type fv struct {
	f    *fixture
	app  App
	ms   *MilestoneFact
	jira *JiraVersionFact
	mr   *MRFact
	tag  *TagFact
}

func (f *fixture) label(app AppID, train string) string {
	return fmt.Sprintf("%s #%s/%d", f.family(app).TrainLabel, train, fixtureYear)
}

func (f *fixture) milestone(app AppID, title, description, start, due string) *MilestoneFact {
	a := f.app(app)
	id := f.id()
	m := &MilestoneFact{
		Project: a.GitLabProject, ID: id, Title: title, Description: description, State: "active",
		Start: dayPtr(f.t, start), Due: dayPtr(f.t, due), URL: fmt.Sprintf("https://gitlab.example.com/m/%d", id),
	}
	f.milestones = append(f.milestones, m)
	return m
}

func (f *fixture) jiraVersion(app AppID, version, description, releaseDate string) *JiraVersionFact {
	id := strconv.Itoa(5000 + f.id())
	v := &JiraVersionFact{
		ID: id, Name: f.app(app).JiraVersionPrefix + " " + version, Description: description,
		URL: "https://jira.example.com/versions/" + id, Approvers: []Approver{},
	}
	if releaseDate != "" {
		v.ReleaseDate = dayPtr(f.t, releaseDate)
	}
	f.versions = append(f.versions, v)
	f.versionApp[id] = app
	return v
}

func (f *fixture) version(app AppID, train, version, freeze, due string) *fv {
	label := f.label(app, train)
	return &fv{
		f: f, app: f.app(app),
		ms:   f.milestone(app, "Release "+version, label, freeze, due),
		jira: f.jiraVersion(app, version, label, due),
	}
}

func (f *fixture) published(app AppID, train, version, freeze, due string) *fv {
	v := f.version(app, train, version, freeze, due)
	v.approvers("Ann:APPROVED", "Bob:APPROVED")
	v.jira.Released = true
	v.relatedWork(CategoryScanPage, CategoryReleaseNote)
	v.releaseMR(freeze, MRMerged)
	v.tagOn(day(f.t, due).Add(-1).String())
	return v
}

func (v *fv) approvers(specs ...string) {
	v.jira.Approvers = []Approver{}
	for _, s := range specs {
		name, status, _ := strings.Cut(s, ":")
		v.jira.Approvers = append(v.jira.Approvers, Approver{AccountID: "acc-" + name, Name: name, Status: status})
	}
}

func (v *fv) relatedWork(categories ...string) {
	r := &RelatedWorkFact{VersionID: v.jira.ID, Links: []RelatedWork{}}
	for _, c := range categories {
		r.Links = append(r.Links, RelatedWork{Category: c, Title: c, URL: "https://wiki.example.com/" + c})
	}
	v.f.related = append(v.f.related, r)
}

func (v *fv) releaseMR(created, state string) *fv {
	f := v.f
	iid := f.id()
	pipeline := f.id()
	msID := v.ms.ID
	mr := &MRFact{
		Project: v.app.GitLabProject, IID: iid, Title: "Release " + v.version(), SourceBranch: "release/release-" + v.version(),
		TargetBranch: "main", State: state, Author: "Rae", CreatedOn: day(f.t, created), MilestoneID: &msID,
		URL: fmt.Sprintf("https://gitlab.example.com/mr/%d", iid),
	}
	if state == MRMerged {
		mr.MergedOn = dayPtr(f.t, created)
	}
	duration := 30 * 60
	f.mrs = append(f.mrs, mr)
	f.details = append(f.details, &MRDetailFact{
		Project: v.app.GitLabProject, IID: iid,
		Pipeline: &PipelineFact{ID: pipeline, Status: "success", URL: "https://gitlab.example.com/p/" + strconv.Itoa(pipeline), Duration: &duration},
	})
	f.jobs = append(f.jobs, &JobsFact{Project: v.app.GitLabProject, Pipeline: pipeline, Jobs: []JobFact{
		{Name: "build", Status: "success"},
		{Name: "semgrep-sast", Status: "success"},
	}})
	v.mr = mr
	return v
}

func (v *fv) version() string { return strings.TrimPrefix(v.ms.Title, "Release ") }

func (v *fv) detail() *MRDetailFact {
	for _, d := range v.f.details {
		if d.Project == v.mr.Project && d.IID == v.mr.IID {
			return d
		}
	}
	v.f.t.Fatalf("no detail for !%d", v.mr.IID)
	return nil
}

func (v *fv) jobList() *JobsFact {
	p := v.detail().Pipeline.ID
	for _, j := range v.f.jobs {
		if j.Project == v.mr.Project && j.Pipeline == p {
			return j
		}
	}
	v.f.t.Fatalf("no jobs for pipeline %d", p)
	return nil
}

func (v *fv) tagOn(on string) {
	f := v.f
	name := v.version()
	v.tag = &TagFact{Project: v.app.GitLabProject, Name: name, CommitOn: day(f.t, on), URL: "https://gitlab.example.com/t/" + name}
	f.tags = append(f.tags, v.tag)
	f.compares = append(f.compares, &CompareFact{Project: v.app.GitLabProject, From: name, To: "main", Commits: []CommitFact{}})
}

func (v *fv) compare() *CompareFact {
	for _, c := range v.f.compares {
		if c.Project == v.app.GitLabProject && c.From == v.tag.Name {
			return c
		}
	}
	v.f.t.Fatalf("no compare for %s", v.tag.Name)
	return nil
}

func (v *fv) issue(key, status, category string) {
	v.f.issues[v.jira.ID] = append(v.f.issues[v.jira.ID], &IssueFact{
		Key: key, Summary: "Card " + key, Status: status, StatusCategory: category,
		FixVersions: []string{v.jira.ID}, URL: "https://jira.example.com/browse/" + key,
	})
}

func (v *fv) forceUpdate(key, status, category, due string) *ForceUpdateFact {
	fu := &ForceUpdateFact{
		Key: key, Status: status, StatusCategory: category, Platform: v.app.Platform, Version: v.version(),
		CreatedOn: *v.ms.Start, URL: "https://jira.example.com/browse/" + key,
	}
	if due != "" {
		fu.Due = dayPtr(v.f.t, due)
	}
	v.f.techreq = append(v.f.techreq, fu)
	return fu
}

func activeHits(b Board) []AlertHit {
	var out []AlertHit
	for _, h := range b.Alerts.Hits {
		if h.Bucket == BucketBadge {
			out = append(out, h)
		}
	}
	return out
}

func hitIDs(hits []AlertHit) []string {
	out := make([]string, 0, len(hits))
	for _, h := range hits {
		out = append(out, string(h.ID))
	}
	return out
}

func outcomeOf(b Board, rule RuleID, app AppID) Outcome {
	for _, o := range b.Alerts.Rules {
		if o.Rule == rule && o.App == app {
			return o.Outcome
		}
	}
	return nil
}
