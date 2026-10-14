//go:build live

package jira

import (
	"context"
	"os"
	"strconv"
	"testing"
	"time"
)

// TestLiveReleaseTrainReads calls each new read once against the real site,
// read-only, with the same credential chain as the app. It logs counts and
// booleans only. Run with:
//
//	RT_LIVE_JIRA_PROJECT=<KEY> RT_LIVE_BOARD=<id> go test -tags live -run TestLiveReleaseTrainReads -v ./internal/adapters/jira/
func TestLiveReleaseTrainReads(t *testing.T) {
	projectKey := os.Getenv("RT_LIVE_JIRA_PROJECT")
	board, err := strconv.Atoi(os.Getenv("RT_LIVE_BOARD"))
	if projectKey == "" || err != nil {
		t.Skip("set RT_LIVE_JIRA_PROJECT and RT_LIVE_BOARD")
	}
	c := NewClient()
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()

	versions, err := c.ProjectVersions(ctx, projectKey, 0)
	if err != nil {
		t.Fatalf("ProjectVersions: %v", err)
	}
	var withApprovers, withRelease int
	var versionID, approverID string
	for _, v := range versions {
		if v.ReleaseDate != "" {
			withRelease++
		}
		if len(v.Approvers) > 0 {
			withApprovers++
			versionID, approverID = v.ID, v.Approvers[0].AccountID
		}
	}
	t.Logf("ProjectVersions: count=%d withReleaseDate=%d withApprovers=%d", len(versions), withRelease, withApprovers)
	if versionID == "" && len(versions) > 0 {
		versionID = versions[len(versions)-1].ID
	}

	if versionID != "" {
		rw, err := c.VersionRelatedWork(ctx, versionID)
		if err != nil {
			t.Fatalf("VersionRelatedWork: %v", err)
		}
		t.Logf("VersionRelatedWork: count=%d", len(rw))

		rows, err := c.SearchAllFields(ctx, "fixVersion = "+versionID, nil, 0)
		if err != nil {
			t.Fatalf("SearchAllFields: %v", err)
		}
		var created, assigned, subtasks int
		for _, r := range rows {
			if !r.Created.IsZero() {
				created++
			}
			if r.Assignee != nil {
				assigned++
			}
			if r.Subtask {
				subtasks++
			}
		}
		t.Logf("SearchAllFields fixVersion: count=%d created=%d assigned=%d subtasks=%d", len(rows), created, assigned, subtasks)
	}

	b, err := c.Board(ctx, board)
	if err != nil {
		t.Fatalf("Board: %v", err)
	}
	t.Logf("Board: name=%t", b.Name != "")

	sprints, err := c.BoardSprints(ctx, board, []string{"active", "closed", "future"}, 0)
	if err != nil {
		t.Fatalf("BoardSprints: %v", err)
	}
	active, dated := 0, 0
	for _, s := range sprints {
		if s.State == "active" {
			active++
		}
		if s.StartDate != nil && s.EndDate != nil {
			dated++
		}
	}
	t.Logf("BoardSprints: count=%d active=%d dated=%d", len(sprints), active, dated)

	if approverID == "" {
		me, err := c.Myself(ctx)
		if err != nil {
			t.Fatalf("Myself: %v", err)
		}
		approverID = me.AccountID
	}
	u, err := c.User(ctx, approverID)
	if err != nil {
		t.Fatalf("User: %v", err)
	}
	t.Logf("User: displayName=%t avatarURL=%t", u.DisplayName != "", u.AvatarURL != "")
	if u.AvatarURL != "" {
		img, err := c.Avatar(ctx, u.AvatarURL)
		if err != nil {
			t.Fatalf("Avatar: %v", err)
		}
		t.Logf("Avatar: bytes=%d image=%t", len(img.Bytes), len(img.ContentType) > 6 && img.ContentType[:6] == "image/")
	}
}
