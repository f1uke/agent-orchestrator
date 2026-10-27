package releasetrain

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"sync"
	"time"

	rt "github.com/aoagents/agent-orchestrator/backend/internal/releasetrain"
)

// PeopleFileName is the optional file in the data dir that says who has which Jira and GitLab accounts.
const PeopleFileName = "release-train-people.json"

// PeopleState says whether the people file is in use.
type PeopleState string

// People file states.
const (
	PeopleAbsent  PeopleState = "absent"
	PeopleInvalid PeopleState = "invalid"
	PeopleOK      PeopleState = "ok"
)

// PeopleReport is the people file's state as the Board response shows it.
type PeopleReport struct {
	State   PeopleState `json:"state" enum:"absent,invalid,ok"`
	Message string      `json:"message,omitempty" description:"Why the file is invalid, or what it is for when absent."`
	Path    string      `json:"path"`
}

// PeopleStatus is the links the file gives, none while it is absent or invalid.
type PeopleStatus struct {
	PeopleReport
	Links []rt.PersonLink
}

type peopleFile struct {
	People []rt.PersonLink `json:"people"`
}

// PeopleLoader re-reads the people file when its mtime or size changes.
type PeopleLoader struct {
	path string

	mu     sync.Mutex
	mtime  time.Time
	size   int64
	loaded bool
	status PeopleStatus
}

// NewPeopleLoader returns a loader for path.
func NewPeopleLoader(path string) *PeopleLoader { return &PeopleLoader{path: path} }

// Load returns the current people, reading the file only when it changed.
func (l *PeopleLoader) Load() PeopleStatus {
	l.mu.Lock()
	defer l.mu.Unlock()
	info, err := os.Stat(l.path)
	switch {
	case errors.Is(err, fs.ErrNotExist):
		l.loaded = false
		l.status = PeopleStatus{PeopleReport: PeopleReport{State: PeopleAbsent, Path: l.path,
			Message: fmt.Sprintf("no people file at %s; accounts are joined by the monitor's own pair and by display name", l.path)}}
		return l.status
	case err != nil:
		return l.invalid(err.Error())
	case l.loaded && info.ModTime().Equal(l.mtime) && info.Size() == l.size:
		return l.status
	}
	l.mtime, l.size, l.loaded = info.ModTime(), info.Size(), true
	b, err := os.ReadFile(l.path)
	if err != nil {
		return l.invalid(err.Error())
	}
	var f peopleFile
	dec := json.NewDecoder(bytes.NewReader(b))
	dec.DisallowUnknownFields()
	if err := dec.Decode(&f); err != nil {
		return l.invalid("not valid JSON of the form {\"people\": [...]}: " + err.Error())
	}
	for i, p := range f.People {
		if p.JiraAccountID == "" || p.GitLabUsername == "" {
			return l.invalid(fmt.Sprintf("people[%d] needs both jiraAccountId and gitlabUsername", i))
		}
	}
	l.status = PeopleStatus{PeopleReport: PeopleReport{State: PeopleOK, Path: l.path}, Links: f.People}
	return l.status
}

func (l *PeopleLoader) invalid(msg string) PeopleStatus {
	l.status = PeopleStatus{PeopleReport: PeopleReport{State: PeopleInvalid, Path: l.path, Message: msg}}
	return l.status
}

func peopleFingerprint(p PeopleStatus) string {
	h := sha256.New()
	_, _ = fmt.Fprintf(h, "%s\x00%s\n", p.State, p.Message)
	for _, l := range p.Links {
		_, _ = fmt.Fprintf(h, "%s\x00%s\x00%s\n", l.JiraAccountID, l.GitLabUsername, l.Name)
	}
	return hex.EncodeToString(h.Sum(nil))
}
