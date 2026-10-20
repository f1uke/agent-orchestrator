package releasetrain

import (
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

// KindName names a fact kind, e.g. "gitlab.milestones".
type KindName string

// SyncMode says how a source refreshes a kind.
type SyncMode string

// Sync modes.
const (
	Snapshotted SyncMode = "snapshot"
	Incremental SyncMode = "incremental"
)

type coverMode int

const (
	perApp coverMode = iota + 1
	global
	perEntity
	perBoard
	perSprint
)

type kindMeta struct {
	Name    KindName
	Label   string
	Version int
	Mode    SyncMode
	Budget  time.Duration
	cover   coverMode
	lookup  bool
	decode  func([]byte) (any, error)
}

// KindInfo is what the poller and the store need to know about a kind by name.
type KindInfo struct {
	Name    KindName
	Version int
	Mode    SyncMode
	Budget  time.Duration
}

// ErrUnknownKind is a stored kind no Kind handle declares any more.
var ErrUnknownKind = errors.New("releasetrain: unknown fact kind")

// LookupKind returns a registered kind's metadata.
func LookupKind(name KindName) (KindInfo, bool) {
	m, ok := kinds[name]
	return KindInfo{Name: m.Name, Version: m.Version, Mode: m.Mode, Budget: m.Budget}, ok
}

// Keyed is a fact that names its own item key within its resource.
type Keyed interface{ ItemKey() string }

// Item is one encoded fact ready to store.
type Item struct {
	Key             string
	Body            []byte
	SourceUpdatedAt *time.Time
}

// EncodeItem encodes one fact of kind k.
func EncodeItem[T Keyed](k Kind[T], v T, sourceUpdatedAt *time.Time) (Item, error) {
	body, err := k.Encode(v)
	if err != nil {
		return Item{}, err
	}
	return Item{Key: v.ItemKey(), Body: body, SourceUpdatedAt: sourceUpdatedAt}, nil
}

// ReadKinds is every kind a rule or a stage row reads, sorted.
func ReadKinds() []KindName {
	seen := map[KindName]bool{}
	for _, r := range ruleTable() {
		for _, k := range r.Reads {
			seen[k] = true
		}
	}
	for _, row := range stageTable {
		for _, k := range row.reads {
			seen[k] = true
		}
	}
	out := make([]KindName, 0, len(seen))
	for k := range seen {
		out = append(out, k)
	}
	sort.Slice(out, func(i, j int) bool { return out[i] < out[j] })
	return out
}

// Kind is the typed handle for one fact kind; it owns encoding, decoding and versioning of T.
type Kind[T any] struct{ kindMeta }

// Decode parses one stored item body.
func (k Kind[T]) Decode(body []byte) (T, error) {
	var v T
	if err := json.Unmarshal(body, &v); err != nil {
		return v, fmt.Errorf("decode %s: %w", k.Name, err)
	}
	return v, nil
}

// Encode serialises one item body.
func (k Kind[T]) Encode(v T) ([]byte, error) {
	b, err := json.Marshal(v)
	if err != nil {
		return nil, fmt.Errorf("encode %s: %w", k.Name, err)
	}
	return b, nil
}

// asLookup marks a kind as a slow identity lookup: its reads show in a source's issues
// but never set the source's age, which is the age of the data the page is built from.
func asLookup[T any](k Kind[T]) Kind[T] {
	k.lookup = true
	kinds[k.Name] = k.kindMeta
	return k
}

func newKind[T any](name KindName, label string, mode SyncMode, budget time.Duration, cover coverMode) Kind[T] {
	k := Kind[T]{kindMeta{Name: name, Label: label, Version: 1, Mode: mode, Budget: budget, cover: cover}}
	k.decode = func(b []byte) (any, error) { return k.Decode(b) }
	kinds[name] = k.kindMeta
	return k
}

var kinds = map[KindName]kindMeta{}
