package reflinks

import (
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

func TestNewStore_AbsentFile_IsEmpty(t *testing.T) {
	st, err := NewStore(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	got := st.Get()
	if got.JiraBaseURL != "" || got.GitLabBaseURL != "" || got.GitLabDefaultRepo != "" || len(got.GitLabRepoAliases) != 0 {
		t.Fatalf("defaults = %+v, want everything empty", got)
	}
	if got.GitLabRepoAliases == nil {
		t.Fatal("aliases must be a non-nil map so the wire shape is {} not null")
	}
}

func TestSet_NormalizesPersistsAndReloads(t *testing.T) {
	dir := t.TempDir()
	st, _ := NewStore(dir)
	err := st.Set(Settings{
		JiraBaseURL:       "  https://jira.example.com/  ",
		GitLabBaseURL:     "https://gitlab.example.com/",
		GitLabDefaultRepo: "/group/project/",
		GitLabRepoAliases: map[string]string{" XYZ ": "group/sub/other"},
	})
	if err != nil {
		t.Fatal(err)
	}
	want := Settings{
		JiraBaseURL:       "https://jira.example.com",
		GitLabBaseURL:     "https://gitlab.example.com",
		GitLabDefaultRepo: "group/project",
		GitLabRepoAliases: map[string]string{"XYZ": "group/sub/other"},
	}
	if got := st.Get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("in-memory = %+v, want %+v", got, want)
	}
	st2, _ := NewStore(dir)
	if got := st2.Get(); !reflect.DeepEqual(got, want) {
		t.Fatalf("reloaded = %+v, want %+v", got, want)
	}
}

func TestSet_EmptyClearsEverything(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{JiraBaseURL: "https://jira.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(Settings{}); err != nil {
		t.Fatal(err)
	}
	if got := st.Get(); got.JiraBaseURL != "" {
		t.Fatalf("after clear = %+v", got)
	}
}

func TestSet_RejectsInvalid(t *testing.T) {
	cases := map[string]Settings{
		"jira not a url":        {JiraBaseURL: "jira.example.com"},
		"jira javascript":       {JiraBaseURL: "javascript:alert(1)"},
		"gitlab with query":     {GitLabBaseURL: "https://gitlab.example.com?x=1"},
		"gitlab with userinfo":  {GitLabBaseURL: "https://me:pw@gitlab.example.com"},
		"repo with space":       {GitLabDefaultRepo: "group/my project"},
		"repo with dotdot":      {GitLabDefaultRepo: "group/../x"},
		"alias starts with dig": {GitLabRepoAliases: map[string]string{"9x": "g/p"}},
		"alias empty path":      {GitLabRepoAliases: map[string]string{"XYZ": " "}},
		"alias bad path":        {GitLabRepoAliases: map[string]string{"XYZ": "g/p?x"}},
		"alias case clash":      {GitLabRepoAliases: map[string]string{"XYZ": "g/a", "xyz": "g/b"}},
	}
	for name, in := range cases {
		t.Run(name, func(t *testing.T) {
			st, _ := NewStore(t.TempDir())
			if err := st.Set(in); err == nil {
				t.Fatalf("Set(%+v) succeeded, want an error", in)
			}
		})
	}
}

func TestSet_ErrorLeavesPreviousValue(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{JiraBaseURL: "https://jira.example.com"}); err != nil {
		t.Fatal(err)
	}
	if err := st.Set(Settings{JiraBaseURL: "nope"}); err == nil {
		t.Fatal("want error")
	}
	if got := st.Get().JiraBaseURL; got != "https://jira.example.com" {
		t.Fatalf("JiraBaseURL = %q, want the previous value kept", got)
	}
}

func TestNewStore_InvalidFile_DegradesToEmpty(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, fileName), []byte(`{"jiraBaseUrl":"ftp://x"}`), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got := st.Get().JiraBaseURL; got != "" {
		t.Fatalf("JiraBaseURL = %q, want empty for a file that does not validate", got)
	}
}

func TestGet_ReturnsACopy(t *testing.T) {
	st, _ := NewStore(t.TempDir())
	if err := st.Set(Settings{GitLabRepoAliases: map[string]string{"XYZ": "g/p"}}); err != nil {
		t.Fatal(err)
	}
	got := st.Get()
	got.GitLabRepoAliases["XYZ"] = "mutated"
	if st.Get().GitLabRepoAliases["XYZ"] != "g/p" {
		t.Fatal("mutating the returned map changed the store")
	}
}

func TestNewStore_EmptyDir_Errors(t *testing.T) {
	if _, err := NewStore(""); err == nil || !strings.Contains(err.Error(), "data dir") {
		t.Fatalf("err = %v, want a data dir error", err)
	}
}
