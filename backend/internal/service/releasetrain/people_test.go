package releasetrain

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

func TestPeopleLoaderAbsentFileMeansNoLinks(t *testing.T) {
	l := NewPeopleLoader(filepath.Join(t.TempDir(), PeopleFileName))
	if st := l.Load(); st.State != PeopleAbsent || st.Links != nil || st.Path == "" {
		t.Fatalf("status = %+v", st)
	}
}

func TestPeopleLoaderReadsLinksAndReReadsOnlyAfterAChange(t *testing.T) {
	path := filepath.Join(t.TempDir(), PeopleFileName)
	at := time.Date(2026, 10, 14, 3, 0, 0, 0, time.UTC)
	writeConfig(t, path, `{"people":[{"jiraAccountId":"acc-ann","gitlabUsername":"ann","name":"Ann Example"}]}`, at)
	l := NewPeopleLoader(path)
	want := []rt.PersonLink{{JiraAccountID: "acc-ann", GitLabUsername: "ann", Name: "Ann Example"}}
	if st := l.Load(); st.State != PeopleOK || !reflect.DeepEqual(st.Links, want) {
		t.Fatalf("status = %+v", st)
	}

	writeConfig(t, path, `{"people":[{"jiraAccountId":"acc-bob","gitlabUsername":"bob","name":"Bob Example"}]}`, at)
	if st := l.Load(); !reflect.DeepEqual(st.Links, want) {
		t.Fatalf("a same-mtime, same-size file was read again: %+v", st.Links)
	}
	writeConfig(t, path, `{"people":[{"jiraAccountId":"acc-bob","gitlabUsername":"bob","name":"Bob Example"}]}`, at.Add(time.Second))
	if st := l.Load(); len(st.Links) != 1 || st.Links[0].GitLabUsername != "bob" {
		t.Fatalf("an edited file was not read again: %+v", st)
	}
	if err := os.Remove(path); err != nil {
		t.Fatal(err)
	}
	if st := l.Load(); st.State != PeopleAbsent || st.Links != nil {
		t.Fatalf("a removed file = %+v, want absent and no links", st)
	}
}

func TestPeopleLoaderRefusesAFileItCannotTrust(t *testing.T) {
	for name, body := range map[string]string{
		"not JSON":             `{"people":`,
		"a typo in a key":      `{"people":[{"jiraAccountId":"acc-ann","gitlabUsername":"ann","nmae":"Ann"}]}`,
		"a missing gitlab":     `{"people":[{"jiraAccountId":"acc-ann"}]}`,
		"a missing jira":       `{"people":[{"gitlabUsername":"ann"}]}`,
		"a wrong top-level":    `[{"jiraAccountId":"acc-ann","gitlabUsername":"ann"}]`,
		"an unknown top-level": `{"people":[],"extra":1}`,
	} {
		t.Run(name, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), PeopleFileName)
			writeConfig(t, path, body, time.Now())
			st := NewPeopleLoader(path).Load()
			if st.State != PeopleInvalid || st.Links != nil || strings.TrimSpace(st.Message) == "" {
				t.Fatalf("status = %+v, want invalid with a message and no links", st)
			}
		})
	}
}
