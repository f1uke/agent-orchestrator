package testiny

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	rcloneadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/rclone"
	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/qaevidence"
)

// commentCall is one comment the fake was asked to post.
type commentCall struct {
	run       domain.TestinyRunID
	caseID    int64
	projectID int64
	text      string
}

func (f *fakeTestiny) ResultComments(_ context.Context, run domain.TestinyRunID) ([]testinyadapter.ResultComment, error) {
	if err := f.note(fmt.Sprintf("comment find %d", run)); err != nil {
		return nil, err
	}
	f.mu.Lock()
	defer f.mu.Unlock()
	return slices.Clone(f.comments[run]), nil
}

// CommentOnResult keeps the comment, so a later read sees it, unless
// commentFail names the case.
func (f *fakeTestiny) CommentOnResult(_ context.Context, run domain.TestinyRunID, caseID, projectID int64, text string) (int64, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	if err := f.commentFail[caseID]; err != nil {
		return 0, err
	}
	f.posted = append(f.posted, commentCall{run: run, caseID: caseID, projectID: projectID, text: text})
	id := int64(9000 + len(f.posted))
	f.addComment(run, id, caseID, strings.Split(text, "\n")...)
	return id, nil
}

// addComment is a comment on a result, posted by AO or by a person. The
// caller holds f.mu.
func (f *fakeTestiny) addComment(run domain.TestinyRunID, id, caseID int64, urls ...string) {
	if f.comments == nil {
		f.comments = map[domain.TestinyRunID][]testinyadapter.ResultComment{}
	}
	f.comments[run] = append(f.comments[run], testinyadapter.ResultComment{ID: id, CaseID: caseID, URLs: urls})
}

func (s *fakeStore) AppendTestinyEvidence(_ context.Context, entries []domain.TestinyEvidenceEntry) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.evidence = append(s.evidence, entries...)
	return nil
}

func (s *fakeStore) TestinyEvidenceLinked(_ context.Context, run domain.TestinyRunID) ([]domain.TestinyEvidenceEntry, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []domain.TestinyEvidenceEntry
	for _, e := range s.evidence {
		if e.RunID == run && e.Event == domain.TestinyEvidenceLinked {
			out = append(out, e)
		}
	}
	return out, nil
}

func (s *fakeStore) evidenceLog(event domain.TestinyEvidenceEvent) []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	var out []string
	for _, e := range s.evidence {
		if e.Event == event {
			out = append(out, e.File)
		}
	}
	return out
}

// fakeDrive is rclone with one remote: a folder on it holds files by name,
// each with a Drive id. A copy sends what a folder lacks.
type fakeDrive struct {
	mu      sync.Mutex
	remotes []string
	folders map[string]map[string]string
	nextID  int
	copies  int
	// extra are files listed besides the folder's, e.g. a second file with
	// one name.
	extra     map[string][]rcloneadapter.File
	copyErr   error
	copyFirst int
}

func newFakeDrive() *fakeDrive {
	return &fakeDrive{remotes: []string{"finnomena"}, folders: map[string]map[string]string{}, extra: map[string][]rcloneadapter.File{}}
}

func (d *fakeDrive) HasRemote(_ context.Context, name string) (bool, error) {
	return slices.Contains(d.remotes, name), nil
}

func (d *fakeDrive) Copy(_ context.Context, src, dst string, names []string) ([]string, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.copies++
	if d.folders[dst] == nil {
		d.folders[dst] = map[string]string{}
	}
	var copied []string
	for _, n := range names {
		if _, err := os.Stat(filepath.Join(src, n)); err != nil {
			return copied, fmt.Errorf("%w: %s is not in %s", rcloneadapter.ErrUnavailable, n, src)
		}
		if d.copyErr != nil && len(copied) == d.copyFirst {
			return copied, d.copyErr
		}
		if _, have := d.folders[dst][n]; have {
			continue
		}
		d.nextID++
		d.folders[dst][n] = fmt.Sprintf("1drive%d", d.nextID)
		copied = append(copied, n)
	}
	return copied, nil
}

func (d *fakeDrive) List(_ context.Context, dir string) ([]rcloneadapter.File, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	var out []rcloneadapter.File
	for name, id := range d.folders[dir] {
		out = append(out, rcloneadapter.File{Name: name, ID: id})
	}
	return append(out, d.extra[dir]...), nil
}

func (d *fakeDrive) id(dir, name string) string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return d.folders[dir][name]
}

type fakeEvidenceSettings struct{ s qaevidence.Settings }

func (f *fakeEvidenceSettings) Get() qaevidence.Settings { return f.s }

const (
	run625Rel   = "MOBILITY/2026/Sprint 2026-20/TP-193 - Chat session logout/TR-625 - MOBILITY-4901 Chat logout storm - iOS"
	run625Drive = "finnomena:QA/" + run625Rel
)

type evidenceRig struct {
	*rig
	drive    *fakeDrive
	settings *fakeEvidenceSettings
	folder   string
}

// newEvidenceRig is run 625 (plan 193, milestone 80, three cases) linked to
// app-1, its folder in the QA Evidence tree holding README.md and the given
// files, and the Drive folder set to finnomena:QA.
func newEvidenceRig(t *testing.T, files ...string) *evidenceRig {
	t.Helper()
	r := &evidenceRig{rig: newRig(t, on), drive: newFakeDrive(), settings: &fakeEvidenceSettings{qaevidence.Settings{DriveFolder: "finnomena:QA"}}}
	r.tny.results[625] = testinyadapter.Results{
		Cases: []testinyadapter.Case{
			{ID: 3818, Title: "Login", Status: "PASSED"},
			{ID: 3819, Title: "Logout", Status: "FAILED"},
			{ID: 3820, Title: "Relogin", Status: "NOTRUN"},
		},
		Summary: map[string]int{"PASSED": 1, "FAILED": 1, "NOTRUN": 1},
	}
	link(r.rig, 625)
	r.newService(time.UTC)
	r.folder = filepath.Join(r.home, "Desktop", "QA Evidence", filepath.FromSlash(run625Rel))
	r.write(t, append([]string{"README.md"}, files...)...)
	return r
}

func (r *evidenceRig) newService(zone *time.Location) {
	r.svc = New(r.tny, r.store, r.store, Options{Home: r.home, Now: r.clock.Now, Drive: r.drive, Evidence: r.settings, Zone: zone})
}

func (r *evidenceRig) write(t *testing.T, names ...string) {
	t.Helper()
	for _, n := range names {
		p := filepath.Join(r.folder, n)
		if err := os.MkdirAll(filepath.Dir(p), 0o755); err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(p, []byte(n), 0o600); err != nil {
			t.Fatal(err)
		}
	}
}

func (r *evidenceRig) upload(by string) (domain.TestinyEvidenceReport, error) {
	return r.svc.UploadEvidence(context.Background(), "app-1", 625, by)
}

func (r *evidenceRig) posted() []commentCall {
	r.tny.mu.Lock()
	defer r.tny.mu.Unlock()
	return slices.Clone(r.tny.posted)
}

func (r *evidenceRig) url(name string) string {
	return domain.DriveFileURL(r.drive.id(run625Drive, name))
}

func TestUploadEvidenceUploadsTheFolderAndLinksEachCase(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png", "TC-3819 FAIL MOBILITY-1 - iPhone.mp4", "TC-3819 FAIL MOBILITY-1 - Pixel.mp4", ".DS_Store")
	report, err := r.upload("")
	if err != nil {
		t.Fatalf("UploadEvidence: %v", err)
	}
	if report.Folder != r.folder || report.Drive != run625Drive {
		t.Fatalf("folder %q, drive %q", report.Folder, report.Drive)
	}
	wantUploaded := []string{"README.md", "TC-3818 pass.png", "TC-3819 FAIL MOBILITY-1 - Pixel.mp4", "TC-3819 FAIL MOBILITY-1 - iPhone.mp4"}
	if !reflect.DeepEqual(report.Uploaded, wantUploaded) {
		t.Fatalf("uploaded = %q, want %q (the dotfile left out)", report.Uploaded, wantUploaded)
	}

	wantPosted := []commentCall{
		{run: 625, caseID: 3818, projectID: 1, text: r.url("TC-3818 pass.png")},
		{run: 625, caseID: 3819, projectID: 1, text: r.url("TC-3819 FAIL MOBILITY-1 - Pixel.mp4") + "\n" + r.url("TC-3819 FAIL MOBILITY-1 - iPhone.mp4")},
	}
	if got := r.posted(); !reflect.DeepEqual(got, wantPosted) {
		t.Fatalf("posted =\n%+v\nwant one comment per case, one link per line in file name order\n%+v", got, wantPosted)
	}
	wantCases := []domain.TestinyEvidenceCaseLinks{
		{CaseID: 3818, Linked: []string{"TC-3818 pass.png"}, CommentID: 9001, AlreadyLinked: []string{}},
		{CaseID: 3819, Linked: []string{"TC-3819 FAIL MOBILITY-1 - Pixel.mp4", "TC-3819 FAIL MOBILITY-1 - iPhone.mp4"}, CommentID: 9002, AlreadyLinked: []string{}},
	}
	if !reflect.DeepEqual(report.Cases, wantCases) {
		t.Fatalf("cases =\n%+v\nwant\n%+v", report.Cases, wantCases)
	}
	if got := r.store.evidenceLog(domain.TestinyEvidenceUploaded); !reflect.DeepEqual(got, wantUploaded) {
		t.Fatalf("uploads logged = %q, want %q", got, wantUploaded)
	}
	if got := r.store.evidenceLog(domain.TestinyEvidenceLinked); len(got) != 3 {
		t.Fatalf("links logged = %q, want the three evidence files", got)
	}

	c := caseByID(t, report.Run, 3819)
	want := []domain.TestinyEvidenceLink{
		{URL: r.url("TC-3819 FAIL MOBILITY-1 - Pixel.mp4"), DriveID: r.drive.id(run625Drive, "TC-3819 FAIL MOBILITY-1 - Pixel.mp4"), File: "TC-3819 FAIL MOBILITY-1 - Pixel.mp4"},
		{URL: r.url("TC-3819 FAIL MOBILITY-1 - iPhone.mp4"), DriveID: r.drive.id(run625Drive, "TC-3819 FAIL MOBILITY-1 - iPhone.mp4"), File: "TC-3819 FAIL MOBILITY-1 - iPhone.mp4"},
	}
	if !reflect.DeepEqual(c.Evidence, want) {
		t.Fatalf("the fresh run's TC-3819 evidence =\n%+v\nwant\n%+v", c.Evidence, want)
	}
	if got := caseByID(t, report.Run, 3820).Evidence; got == nil || len(got) != 0 {
		t.Fatalf("a case with no evidence has %#v, want an empty list", got)
	}
	if r.tny.count("run results") == 0 || c.Status != "FAILED" {
		t.Fatal("the report's run was not read fresh, or a status changed")
	}
}

// Each run's evidence goes under its own Testiny project, in Drive and in the
// comments: a task's STAR run lands under STAR while its other run is MOB.
func TestUploadEvidenceGoesUnderTheRunsOwnProject(t *testing.T) {
	r := newEvidenceRig(t)
	r.tny.runs[700] = testinyadapter.Run{ID: 700, Title: "STAR-2413 Order summary - web", ProjectID: 3, PlanID: 193, MilestoneID: 80}
	r.clock.advance(time.Second)
	link(r.rig, 700)
	const starRel = "STAR/2026/Sprint 2026-20/TP-193 - Chat session logout/TR-700 - STAR-2413 Order summary - web"
	r.folder = filepath.Join(r.home, "Desktop", "QA Evidence", filepath.FromSlash(starRel))
	r.write(t, "README.md", "TC-9001 pass.png")

	report, err := r.svc.UploadEvidence(context.Background(), "app-1", 700, "")
	if err != nil {
		t.Fatalf("UploadEvidence: %v", err)
	}
	if report.Drive != "finnomena:QA/"+starRel {
		t.Fatalf("drive = %q, want it under STAR", report.Drive)
	}
	want := []commentCall{{run: 700, caseID: 9001, projectID: star.ID, text: domain.DriveFileURL(r.drive.id(report.Drive, "TC-9001 pass.png"))}}
	if got := r.posted(); !reflect.DeepEqual(got, want) {
		t.Fatalf("posted = %+v, want the link on STAR's run in STAR", got)
	}
}

func TestUploadEvidenceAgainPostsNothing(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png", "TC-3819 FAIL MOBILITY-1.mp4")
	if _, err := r.upload(""); err != nil {
		t.Fatal(err)
	}
	report, err := r.upload("")
	if err != nil {
		t.Fatalf("second upload: %v", err)
	}
	if len(report.Uploaded) != 0 || len(r.posted()) != 2 {
		t.Fatalf("second upload sent %q and posted %d comments in all, want nothing new", report.Uploaded, len(r.posted()))
	}
	want := []domain.TestinyEvidenceCaseLinks{
		{CaseID: 3818, Linked: []string{}, AlreadyLinked: []string{"TC-3818 pass.png"}},
		{CaseID: 3819, Linked: []string{}, AlreadyLinked: []string{"TC-3819 FAIL MOBILITY-1.mp4"}},
	}
	if !reflect.DeepEqual(report.Cases, want) {
		t.Fatalf("cases = %+v, want everything already linked", report.Cases)
	}
}

func TestUploadEvidenceLinksOnlyANewFile(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png")
	if _, err := r.upload(""); err != nil {
		t.Fatal(err)
	}
	r.write(t, "TC-3818 pass - iPad.png")
	report, err := r.upload("")
	if err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(report.Uploaded, []string{"TC-3818 pass - iPad.png"}) {
		t.Fatalf("uploaded = %q, want only the new file", report.Uploaded)
	}
	posted := r.posted()
	if len(posted) != 2 || posted[1].text != r.url("TC-3818 pass - iPad.png") {
		t.Fatalf("posted = %+v, want a second comment with only the new link", posted)
	}
	if got := report.Cases[0]; !reflect.DeepEqual(got.AlreadyLinked, []string{"TC-3818 pass.png"}) || !reflect.DeepEqual(got.Linked, []string{"TC-3818 pass - iPad.png"}) {
		t.Fatalf("TC-3818 = %+v", got)
	}
}

func TestUploadEvidenceCountsALinkAPersonPasted(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png", "TC-3819 FAIL MOBILITY-1.mp4")
	r.drive.folders[run625Drive] = map[string]string{"TC-3818 pass.png": "1pasted"}
	r.tny.addComment(625, 100, 3818, "https://drive.google.com/open?id=1pasted")
	// The same file linked on another case's result does not count for this one.
	r.tny.addComment(625, 101, 3820, "https://drive.google.com/open?id=1other")
	report, err := r.upload("")
	if err != nil {
		t.Fatal(err)
	}
	posted := r.posted()
	if len(posted) != 1 || posted[0].caseID != 3819 {
		t.Fatalf("posted = %+v, want only TC-3819's link", posted)
	}
	if got := report.Cases[0]; got.CaseID != 3818 || got.CommentID != 0 || !reflect.DeepEqual(got.AlreadyLinked, []string{"TC-3818 pass.png"}) {
		t.Fatalf("TC-3818 = %+v, want it already linked by the person", got)
	}
	ev := caseByID(t, report.Run, 3818).Evidence
	if len(ev) != 1 || ev[0].DriveID != "1pasted" || ev[0].File != "" {
		t.Fatalf("TC-3818 evidence = %+v, want the person's link, its file unnamed", ev)
	}
}

func TestUploadEvidenceByAnAgent(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png")
	if _, err := r.upload("app-1"); err != nil {
		t.Fatalf("the task's own worker: %v", err)
	}
	r.store.mu.Lock()
	by := r.store.evidence[0].SetBy
	r.store.mu.Unlock()
	if by != "app-1" {
		t.Fatalf("logged by %q, want app-1", by)
	}
}

func TestUploadEvidenceReadsTheYearInTheZone(t *testing.T) {
	r := newEvidenceRig(t)
	r.tny.msStart[80] = time.Date(2026, 12, 31, 17, 0, 0, 0, time.UTC)
	r.newService(time.FixedZone("ICT", 7*3600))
	_, err := r.upload("")
	if !errors.Is(err, domain.ErrBadTestinyEvidence) || !strings.Contains(err.Error(), "/MOBILITY/2027/Sprint 2026-20/") {
		t.Fatalf("err = %v, want the folder under 2027, the year the sprint starts in Bangkok", err)
	}
}

func TestUploadEvidenceRefusals(t *testing.T) {
	cases := []struct {
		name  string
		setup func(t *testing.T, r *evidenceRig)
		by    string
		want  error
		says  []string
	}{
		{name: "setting off", setup: func(_ *testing.T, r *evidenceRig) { r.settings.s = qaevidence.Settings{} }, want: ErrEvidenceOff},
		{name: "not linked", setup: func(_ *testing.T, r *evidenceRig) { r.store.links = nil }, want: ErrRunNotLinked},
		{name: "not yours", by: "app-9", want: ErrWriteNotYours},
		{name: "closed run", setup: func(_ *testing.T, r *evidenceRig) {
			run := r.tny.runs[625]
			run.Closed = true
			r.tny.runs[625] = run
		}, want: ErrRunClosed, says: []string{"reopen"}},
		{name: "no plan or milestone", setup: func(_ *testing.T, r *evidenceRig) {
			run := r.tny.runs[625]
			run.PlanID, run.MilestoneID = 0, 0
			r.tny.runs[625] = run
		}, want: domain.ErrBadTestinyEvidence, says: []string{"has no test plan and no milestone in Testiny"}},
		{name: "a title with a slash", setup: func(_ *testing.T, r *evidenceRig) { r.tny.plans[193] = "Chat / logout" }, want: domain.ErrBadTestinyEvidence, says: []string{"change it in Testiny"}},
		{name: "no folder", setup: func(t *testing.T, r *evidenceRig) {
			if err := os.RemoveAll(r.folder); err != nil {
				t.Fatal(err)
			}
		}, want: domain.ErrBadTestinyEvidence, says: []string{"no evidence folder yet", "Desktop/QA Evidence/" + run625Rel}},
		{name: "folder elsewhere", setup: func(t *testing.T, r *evidenceRig) {
			wrong := filepath.Join(r.home, "Desktop", "QA Evidence", "MOBILITY", "2026", "Sprint-2026-20", "TP-193 - Chat session logout", "TR-625 - MOBILITY-4901 Chat logout storm - iOS")
			if err := os.MkdirAll(filepath.Dir(wrong), 0o755); err != nil {
				t.Fatal(err)
			}
			if err := os.Rename(r.folder, wrong); err != nil {
				t.Fatal(err)
			}
		}, want: domain.ErrBadTestinyEvidence, says: []string{"Sprint-2026-20", "move it there", "Desktop/QA Evidence/" + run625Rel}},
		{name: "two folders", setup: func(t *testing.T, r *evidenceRig) {
			if err := os.MkdirAll(filepath.Join(r.home, "Desktop", "QA Evidence", "MOBILITY", "2025", "x", "y", "TR-625 - old"), 0o755); err != nil {
				t.Fatal(err)
			}
		}, want: domain.ErrBadTestinyEvidence, says: []string{"2 evidence folders", "keep only the one at"}},
		{name: "bad files", setup: func(t *testing.T, r *evidenceRig) {
			r.write(t, "TC-3819 Pass.png", "TC-9999 pass.png", "sub/TC-3818 pass.png")
			if err := os.Remove(filepath.Join(r.folder, "README.md")); err != nil {
				t.Fatal(err)
			}
		}, want: domain.ErrBadTestinyEvidence, says: []string{"README.md is missing", `"TC-3819 Pass.png" writes pass in the wrong case`, "TC-9999, which is not in the run", `"sub" is a folder`, "(folder: "}},
		{name: "remote missing", setup: func(_ *testing.T, r *evidenceRig) { r.settings.s.DriveFolder = "other:QA" }, want: rcloneadapter.ErrRemoteMissing, says: []string{`"other"`, "rclone config"}},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			r := newEvidenceRig(t, "TC-3818 pass.png")
			if tc.setup != nil {
				tc.setup(t, r)
			}
			_, err := r.upload(tc.by)
			if !errors.Is(err, tc.want) {
				t.Fatalf("err = %v, want %v", err, tc.want)
			}
			for _, s := range tc.says {
				if !strings.Contains(err.Error(), s) {
					t.Errorf("%q does not say %q", err, s)
				}
			}
			if r.drive.copies != 0 || len(r.posted()) != 0 || len(r.store.evidence) != 0 {
				t.Fatalf("a refusal uploaded %d times, posted %d comments, logged %d rows", r.drive.copies, len(r.posted()), len(r.store.evidence))
			}
		})
	}
}

func TestUploadEvidenceLogsAPartialCopy(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png", "TC-3819 FAIL MOBILITY-1.mp4")
	r.drive.copyErr = fmt.Errorf("%w finnomena: its sign-in to Google has expired", rcloneadapter.ErrAuth)
	r.drive.copyFirst = 1
	if _, err := r.upload(""); !errors.Is(err, rcloneadapter.ErrAuth) {
		t.Fatalf("err = %v, want ErrAuth", err)
	}
	if got := r.store.evidenceLog(domain.TestinyEvidenceUploaded); !reflect.DeepEqual(got, []string{"README.md"}) {
		t.Fatalf("uploads logged = %q, want what reached Drive", got)
	}
	if len(r.posted()) != 0 {
		t.Fatal("links were posted after a failed upload")
	}

	r.drive.copyErr = nil
	report, err := r.upload("")
	if err != nil {
		t.Fatalf("re-run: %v", err)
	}
	if !reflect.DeepEqual(report.Uploaded, []string{"TC-3818 pass.png", "TC-3819 FAIL MOBILITY-1.mp4"}) || len(r.posted()) != 2 {
		t.Fatalf("re-run uploaded %q and posted %d comments, want the rest", report.Uploaded, len(r.posted()))
	}
}

func TestUploadEvidenceRefusesTwoDriveFilesWithOneName(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png")
	r.drive.extra[run625Drive] = []rcloneadapter.File{{Name: "TC-3818 pass.png", ID: "1copy"}}
	_, err := r.upload("")
	if !errors.Is(err, ErrDriveDuplicate) || !strings.Contains(err.Error(), "TC-3818 pass.png") {
		t.Fatalf("err = %v, want ErrDriveDuplicate naming the file", err)
	}
	if len(r.posted()) != 0 {
		t.Fatal("a link was posted that could name either file")
	}
	if got := r.store.evidenceLog(domain.TestinyEvidenceUploaded); len(got) != 2 {
		t.Fatalf("uploads logged = %q, want both files that reached Drive", got)
	}
}

func TestUploadEvidenceConvergesAfterACommentFails(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png", "TC-3819 FAIL MOBILITY-1.mp4")
	r.tny.commentFail = map[int64]error{3819: fmt.Errorf("%w: connection refused", testinyadapter.ErrUnavailable)}
	if _, err := r.upload(""); !errors.Is(err, testinyadapter.ErrUnavailable) || !strings.Contains(err.Error(), "TC-3819") {
		t.Fatalf("err = %v, want the failure naming TC-3819", err)
	}
	if got := r.store.evidenceLog(domain.TestinyEvidenceLinked); !reflect.DeepEqual(got, []string{"TC-3818 pass.png"}) {
		t.Fatalf("links logged = %q, want TC-3818's, posted before the failure", got)
	}

	r.tny.commentFail = nil
	if _, err := r.upload(""); err != nil {
		t.Fatalf("re-run: %v", err)
	}
	posted := r.posted()
	if len(posted) != 2 || posted[1].caseID != 3819 {
		t.Fatalf("posted = %+v, want TC-3818 once and then TC-3819", posted)
	}
}

func TestRunsShowEachCasesDriveLinks(t *testing.T) {
	r := newEvidenceRig(t, "TC-3818 pass.png")
	if _, err := r.upload(""); err != nil {
		t.Fatal(err)
	}
	r.tny.mu.Lock()
	r.tny.addComment(625, 50, 3819, "https://jira.example.com/browse/MOBILITY-1", "https://drive.google.com/file/d/1person/view")
	r.tny.mu.Unlock()
	res, err := r.svc.Runs(context.Background(), "app-1", true)
	if err != nil {
		t.Fatal(err)
	}
	v := res[0]
	if got := caseByID(t, v, 3818).Evidence; len(got) != 1 || got[0].File != "TC-3818 pass.png" {
		t.Fatalf("TC-3818 evidence = %+v, want AO's link named by its file", got)
	}
	want := []domain.TestinyEvidenceLink{{URL: "https://drive.google.com/file/d/1person/view", DriveID: "1person"}}
	if got := caseByID(t, v, 3819).Evidence; !reflect.DeepEqual(got, want) {
		t.Fatalf("TC-3819 evidence = %+v, want only the Drive link, unnamed", got)
	}
}
