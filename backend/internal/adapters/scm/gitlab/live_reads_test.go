//go:build live

package gitlab

import (
	"context"
	"os"
	"strconv"
	"strings"
	"testing"
	"time"
)

// TestLiveReleaseTrainReads calls each new read once against the real host,
// read-only. It logs counts and booleans only. Run with:
//
//	AO_GITLAB_HOST=<host> RT_LIVE_GITLAB_PROJECT=<numeric id> go test -tags live -run TestLiveReleaseTrainReads -v ./internal/adapters/scm/gitlab/
func TestLiveReleaseTrainReads(t *testing.T) {
	host := strings.TrimSpace(strings.Split(os.Getenv("AO_GITLAB_HOST"), ",")[0])
	project, err := strconv.Atoi(os.Getenv("RT_LIVE_GITLAB_PROJECT"))
	if host == "" || err != nil {
		t.Skip("set AO_GITLAB_HOST and RT_LIVE_GITLAB_PROJECT")
	}
	c := NewClient(ClientOptions{
		APIBase: "https://" + host + "/api/v4",
		Token:   FallbackTokenSource{EnvTokenSource{EnvVars: []string{"AO_GITLAB_TOKEN"}}, &GlabTokenSource{Host: host}},
	})
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Minute)
	defer cancel()
	since := time.Now().AddDate(0, -3, 0)

	user, err := c.CurrentUser(ctx)
	if err != nil {
		t.Fatalf("CurrentUser: %v", err)
	}
	t.Logf("CurrentUser: username=%t name=%t", user.Username != "", user.Name != "")

	ms, err := c.ListMilestones(ctx, project, ListMilestonesOptions{UpdatedAfter: since, State: "all"})
	if err != nil {
		t.Fatalf("ListMilestones: %v", err)
	}
	dated := 0
	for _, m := range ms {
		if m.StartDate != "" && m.DueDate != "" {
			dated++
		}
	}
	t.Logf("ListMilestones: count=%d dated=%d", len(ms), dated)

	tags, err := c.ListTags(ctx, project, ListTagsOptions{OrderBy: "updated"})
	if err != nil {
		t.Fatalf("ListTags: %v", err)
	}
	t.Logf("ListTags: count=%d", len(tags))
	if len(tags) > 0 {
		cmp, err := c.Compare(ctx, project, tags[0].Name, "main")
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		t.Logf("Compare newest-tag..main: commits=%d webURL=%t", len(cmp.Commits), cmp.WebURL != "")
		cmp, err = c.Compare(ctx, project, tags[0].Name, "develop")
		if err != nil {
			t.Fatalf("Compare: %v", err)
		}
		t.Logf("Compare newest-tag..develop: commits=%d allTimed=%t", len(cmp.Commits), allTimed(cmp.Commits))
	}

	page, err := c.ListMergeRequestsPage(ctx, project, ListMRsOptions{UpdatedAfter: since, Page: 1})
	if err != nil {
		t.Fatalf("ListMergeRequestsPage: %v", err)
	}
	merged, withMilestone := 0, 0
	for _, mr := range page.Items {
		if mr.MergedAt != nil {
			merged++
		}
		if mr.MilestoneID != nil {
			withMilestone++
		}
	}
	t.Logf("ListMergeRequestsPage: count=%d full=%t merged=%d withMilestone=%d", len(page.Items), page.Full, merged, withMilestone)
	if len(page.Items) == 0 {
		t.Fatal("no MRs to read detail from")
	}

	var pipelineID int
	for i := len(page.Items) - 1; i >= 0 && pipelineID == 0; i-- {
		d, err := c.MergeRequestDetail(ctx, project, page.Items[i].IID)
		if err != nil {
			t.Fatalf("MergeRequestDetail: %v", err)
		}
		if d.HeadPipeline != nil {
			pipelineID = d.HeadPipeline.ID
			t.Logf("MergeRequestDetail: headPipeline=true status=%t duration=%t mergeStatus=%t", d.HeadPipeline.Status != "", d.HeadPipeline.Duration != nil, d.DetailedMergeStatus != "")
		}
	}
	if pipelineID == 0 {
		t.Log("MergeRequestDetail: no MR on the page has a head pipeline")
		return
	}
	jobs, err := c.ListPipelineJobs(ctx, project, pipelineID, 0)
	if err != nil {
		t.Fatalf("ListPipelineJobs: %v", err)
	}
	t.Logf("ListPipelineJobs: count=%d", len(jobs))
}

func allTimed(commits []Commit) bool {
	for _, c := range commits {
		if c.CreatedAt.IsZero() || c.WebURL == "" {
			return false
		}
	}
	return true
}
