package testiny

import (
	"context"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"golang.org/x/sync/errgroup"

	rcloneadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/rclone"
	testinyadapter "github.com/aoagents/agent-orchestrator/backend/internal/adapters/testiny"
	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/qaevidence"
)

// UploadEvidence uploads a linked run's QA Evidence folder to Google Drive as
// it is, and posts each evidence file's Drive link on its case's result in the
// run. It never touches a result's status, and never renames, moves or
// deletes anything, here or on Drive.
//
// by is the session id of the agent running it, or "" for a person in the
// app; an agent must be one that may record the task's results (mayWrite).
// The folder must sit where the names Testiny gives put it, and hold only
// files the naming table allows: every problem is reported at once.
//
// It converges: rclone sends only what Drive lacks, and a case's result gets
// one new comment with the links whose Drive file no comment on that result
// names yet. A link a person pasted counts. Whatever reached Drive or Testiny
// before a failure is logged before the error is returned, and running it
// again finishes the job.
func (s *Service) UploadEvidence(ctx context.Context, task domain.SessionID, id domain.TestinyRunID, by string) (domain.TestinyEvidenceReport, error) {
	cfg, err := s.config(ctx, task)
	if err != nil {
		return domain.TestinyEvidenceReport{}, err
	}
	var setting qaevidence.Settings
	if s.evidence != nil {
		setting = s.evidence.Get()
	}
	if s.drive == nil || setting.DriveFolder == "" {
		return domain.TestinyEvidenceReport{}, ErrEvidenceOff
	}
	link, err := s.linkOf(ctx, task, id)
	if err != nil {
		return domain.TestinyEvidenceReport{}, err
	}
	if by != "" {
		if err := s.mayWrite(ctx, task, domain.SessionID(by)); err != nil {
			return domain.TestinyEvidenceReport{}, err
		}
	}

	run, err := s.reader.Run(ctx, id)
	if errors.Is(err, testinyadapter.ErrNotFound) {
		return domain.TestinyEvidenceReport{}, fmt.Errorf("%w: %s", ErrRunNotFound, id)
	}
	if err != nil {
		return domain.TestinyEvidenceReport{}, err
	}
	if run.Closed {
		return domain.TestinyEvidenceReport{}, fmt.Errorf("%w: %s is closed, and a closed run takes no more links. Evidence goes up before the run is closed; ask the human to reopen it", ErrRunClosed, id)
	}
	var missing []string
	if run.PlanID == 0 {
		missing = append(missing, "test plan")
	}
	if run.MilestoneID == 0 {
		missing = append(missing, "milestone")
	}
	if len(missing) > 0 {
		return domain.TestinyEvidenceReport{}, fmt.Errorf("%w: %s has no %s in Testiny, and its evidence folder is named after its milestone and plan. Ask the human to attach the run to them",
			domain.ErrBadTestinyEvidence, id, strings.Join(missing, " and no "))
	}

	tree, inRun, err := s.evidenceTree(ctx, run)
	if err != nil {
		return domain.TestinyEvidenceReport{}, err
	}
	folder, err := s.findEvidenceFolder(id, filepath.Join(s.evidenceRoot(), filepath.FromSlash(tree.Rel())))
	if err != nil {
		return domain.TestinyEvidenceReport{}, err
	}
	entries, err := os.ReadDir(folder)
	if err != nil {
		return domain.TestinyEvidenceReport{}, fmt.Errorf("read %s: %w", folder, err)
	}
	files, err := domain.CheckTestinyEvidenceFolder(entries, inRun)
	if err != nil {
		return domain.TestinyEvidenceReport{}, fmt.Errorf("%w (folder: %s)", err, folder)
	}
	slices.SortFunc(files, func(a, b domain.TestinyEvidenceFile) int { return strings.Compare(a.Name, b.Name) })

	remote := setting.Remote()
	if ok, err := s.drive.HasRemote(ctx, remote); err != nil {
		return domain.TestinyEvidenceReport{}, err
	} else if !ok {
		return domain.TestinyEvidenceReport{}, fmt.Errorf("%w: rclone has no remote named %q, which the QA evidence Drive folder %s names. Set it up with `rclone config`, or fix the setting",
			rcloneadapter.ErrRemoteMissing, remote, setting.DriveFolder)
	}
	drivePath := setting.Join(tree.Rel())
	names := []string{domain.TestinyEvidenceReadme}
	caseOf := map[string]int64{}
	for _, f := range files {
		names = append(names, f.Name)
		caseOf[f.Name] = f.CaseID
	}

	uploaded, copyErr := s.drive.Copy(ctx, folder, drivePath, names)
	ids := map[string]string{}
	var listErr error
	if copyErr == nil {
		ids, listErr = s.driveIDs(ctx, drivePath, names)
	}
	if err := s.logUploads(ctx, task, id, uploaded, caseOf, ids, by); err != nil {
		return domain.TestinyEvidenceReport{}, err
	}
	if copyErr != nil {
		return domain.TestinyEvidenceReport{}, copyErr
	}
	if listErr != nil {
		return domain.TestinyEvidenceReport{}, listErr
	}

	s.writeMu.Lock()
	cases, linkErr := s.linkEvidence(ctx, task, run, files, ids, by)
	s.writeMu.Unlock()
	s.forget(id)
	if linkErr != nil {
		return domain.TestinyEvidenceReport{}, linkErr
	}

	fresh, err := s.fetchByID(ctx, id)
	s.remember(id, fresh, err)
	view, err := s.viewOf(ctx, link, cfg)
	if err != nil {
		return domain.TestinyEvidenceReport{}, err
	}
	if uploaded == nil {
		uploaded = []string{}
	}
	return domain.TestinyEvidenceReport{Folder: folder, Drive: drivePath, Uploaded: uploaded, Cases: cases, Run: view}, nil
}

// evidenceTree reads what Testiny names the run's folders after, and which
// cases are in the run.
func (s *Service) evidenceTree(ctx context.Context, run testinyadapter.Run) (domain.TestinyEvidenceTree, map[int64]bool, error) {
	var (
		plan      testinyadapter.Ref
		milestone testinyadapter.Milestone
		project   domain.TestinyProject
		results   testinyadapter.Results
		g         errgroup.Group
	)
	g.Go(func() (err error) { plan, err = s.reader.Plan(ctx, run.PlanID); return err })
	g.Go(func() (err error) { milestone, err = s.reader.Milestone(ctx, run.MilestoneID); return err })
	g.Go(func() (err error) { project, err = s.reader.ProjectByID(ctx, run.ProjectID); return err })
	g.Go(func() (err error) { results, err = s.reader.Results(ctx, run.ID); return err })
	if err := g.Wait(); err != nil {
		return domain.TestinyEvidenceTree{}, nil, err
	}
	tree, err := domain.NewTestinyEvidenceTree(project.Name,
		domain.TestinyEvidenceYear(milestone.StartAt, milestone.CreatedAt, s.zone),
		domain.TestinyRef{ID: milestone.ID, Title: milestone.Title},
		domain.TestinyRef{ID: plan.ID, Title: plan.Title}, run.ID, run.Title)
	if err != nil {
		return domain.TestinyEvidenceTree{}, nil, err
	}
	inRun := make(map[int64]bool, len(results.Cases))
	for _, c := range results.Cases {
		inRun[c.ID] = true
	}
	return tree, inRun, nil
}

// findEvidenceFolder is the run's folder, which must be the one at expected
// and the only one named as the run's. AO never moves it.
func (s *Service) findEvidenceFolder(id domain.TestinyRunID, expected string) (string, error) {
	dirs := s.evidenceDirs(id)
	switch {
	case len(dirs) == 0:
		return "", fmt.Errorf("%w: %s has no evidence folder yet: create %s, put README.md and the evidence files in it, then run this again",
			domain.ErrBadTestinyEvidence, id, expected)
	case len(dirs) > 1:
		return "", fmt.Errorf("%w: %s has %d evidence folders (%s); keep only the one at %s",
			domain.ErrBadTestinyEvidence, id, len(dirs), strings.Join(dirs, ", "), expected)
	case dirs[0] != expected:
		return "", fmt.Errorf("%w: %s's evidence folder is at %s, but the names Testiny gives put it at %s: move it there. Names are read from Testiny, never composed",
			domain.ErrBadTestinyEvidence, id, dirs[0], expected)
	}
	return expected, nil
}

// driveIDs lists the run's Drive folder after the upload and returns each
// uploaded file's Drive id. Drive allows two files with one name, and then a
// link could name either: that is refused, as AO never deletes on Drive.
func (s *Service) driveIDs(ctx context.Context, drivePath string, names []string) (map[string]string, error) {
	listing, err := s.drive.List(ctx, drivePath)
	if err != nil {
		return nil, err
	}
	ids := map[string]string{}
	var dups []string
	for _, f := range listing {
		if !slices.Contains(names, f.Name) {
			continue
		}
		if _, seen := ids[f.Name]; seen && !slices.Contains(dups, f.Name) {
			dups = append(dups, f.Name)
		}
		ids[f.Name] = f.ID
	}
	if len(dups) > 0 {
		slices.Sort(dups)
		for _, d := range dups {
			delete(ids, d)
		}
		return ids, fmt.Errorf("%w: %s holds more than one file named %q. AO never deletes on Drive: ask the human to remove the extra copies, then run this again",
			ErrDriveDuplicate, drivePath, dups)
	}
	for _, n := range names {
		if ids[n] == "" {
			return ids, fmt.Errorf("%w: %s does not list %q after the upload", rcloneadapter.ErrUnavailable, drivePath, n)
		}
	}
	return ids, nil
}

// logUploads logs each file rclone transferred, with its Drive id when the
// listing gave one.
func (s *Service) logUploads(ctx context.Context, task domain.SessionID, id domain.TestinyRunID, uploaded []string, caseOf map[string]int64, ids map[string]string, by string) error {
	if len(uploaded) == 0 {
		return nil
	}
	at := s.now().UTC()
	entries := make([]domain.TestinyEvidenceEntry, len(uploaded))
	for i, name := range uploaded {
		entries[i] = domain.TestinyEvidenceEntry{
			SessionID: task, RunID: id, Event: domain.TestinyEvidenceUploaded, File: name,
			CaseID: caseOf[name], DriveID: ids[name], SetBy: by, CreatedAt: at,
		}
	}
	if err := s.links.AppendTestinyEvidence(ctx, entries); err != nil {
		return fmt.Errorf("%d files reached Drive but could not be logged: %w", len(uploaded), err)
	}
	return nil
}

// linkEvidence posts, on each case's result, one comment with the links of
// the case's files whose Drive id no comment on that result holds yet, one
// per line in file name order, and logs each. The caller holds writeMu.
func (s *Service) linkEvidence(ctx context.Context, task domain.SessionID, run testinyadapter.Run, files []domain.TestinyEvidenceFile, ids map[string]string, by string) ([]domain.TestinyEvidenceCaseLinks, error) {
	comments, err := s.reader.ResultComments(ctx, run.ID)
	if err != nil {
		return nil, err
	}
	onResult := map[int64]map[string]bool{}
	for _, c := range comments {
		for _, u := range c.URLs {
			if driveID, ok := domain.DriveFileID(u); ok {
				if onResult[c.CaseID] == nil {
					onResult[c.CaseID] = map[string]bool{}
				}
				onResult[c.CaseID][driveID] = true
			}
		}
	}

	byCase := map[int64][]domain.TestinyEvidenceFile{}
	for _, f := range files {
		byCase[f.CaseID] = append(byCase[f.CaseID], f)
	}
	caseIDs := make([]int64, 0, len(byCase))
	for c := range byCase {
		caseIDs = append(caseIDs, c)
	}
	slices.Sort(caseIDs)

	out := make([]domain.TestinyEvidenceCaseLinks, 0, len(caseIDs))
	for _, caseID := range caseIDs {
		entry := domain.TestinyEvidenceCaseLinks{CaseID: caseID, Linked: []string{}, AlreadyLinked: []string{}}
		var urls []string
		for _, f := range byCase[caseID] {
			if onResult[caseID][ids[f.Name]] {
				entry.AlreadyLinked = append(entry.AlreadyLinked, f.Name)
				continue
			}
			entry.Linked = append(entry.Linked, f.Name)
			urls = append(urls, domain.DriveFileURL(ids[f.Name]))
		}
		if len(urls) > 0 {
			commentID, err := s.reader.CommentOnResult(ctx, run.ID, caseID, run.ProjectID, strings.Join(urls, "\n"))
			if err != nil {
				return nil, fmt.Errorf("post the evidence links of TC-%d: %w", caseID, err)
			}
			entry.CommentID = commentID
			at := s.now().UTC()
			logged := make([]domain.TestinyEvidenceEntry, len(entry.Linked))
			for i, name := range entry.Linked {
				logged[i] = domain.TestinyEvidenceEntry{
					SessionID: task, RunID: run.ID, Event: domain.TestinyEvidenceLinked, File: name,
					CaseID: caseID, DriveID: ids[name], CommentID: commentID, SetBy: by, CreatedAt: at,
				}
			}
			if err := s.links.AppendTestinyEvidence(ctx, logged); err != nil {
				return nil, fmt.Errorf("the links of TC-%d reached Testiny in comment %d but could not be logged: %w", caseID, commentID, err)
			}
		}
		out = append(out, entry)
	}
	return out, nil
}

// driveLinks is the Google Drive links in the comments on each case's result,
// in the order they were posted, each Drive file once.
func driveLinks(comments []testinyadapter.ResultComment) map[int64][]domain.TestinyEvidenceLink {
	out := map[int64][]domain.TestinyEvidenceLink{}
	seen := map[int64]map[string]bool{}
	for _, c := range comments {
		for _, u := range c.URLs {
			driveID, ok := domain.DriveFileID(u)
			if !ok || seen[c.CaseID][driveID] {
				continue
			}
			if seen[c.CaseID] == nil {
				seen[c.CaseID] = map[string]bool{}
			}
			seen[c.CaseID][driveID] = true
			out[c.CaseID] = append(out[c.CaseID], domain.TestinyEvidenceLink{URL: u, DriveID: driveID})
		}
	}
	return out
}

// evidenceFiles is the file each Drive id AO linked in the run names.
func (s *Service) evidenceFiles(ctx context.Context, id domain.TestinyRunID) (map[string]string, error) {
	linked, err := s.links.TestinyEvidenceLinked(ctx, id)
	if err != nil || len(linked) == 0 {
		return nil, err
	}
	files := make(map[string]string, len(linked))
	for _, e := range linked {
		files[e.DriveID] = e.File
	}
	return files, nil
}
