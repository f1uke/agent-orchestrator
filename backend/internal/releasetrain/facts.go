package releasetrain

import (
	"encoding/json"
	"fmt"
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
)

type kindMeta struct {
	Name    KindName
	Version int
	Mode    SyncMode
	Budget  time.Duration
	cover   coverMode
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

func newKind[T any](name KindName, mode SyncMode, budget time.Duration, cover coverMode) Kind[T] {
	k := Kind[T]{kindMeta{Name: name, Version: 1, Mode: mode, Budget: budget, cover: cover}}
	kinds[name] = k.kindMeta
	return k
}

var kinds = map[KindName]kindMeta{}
