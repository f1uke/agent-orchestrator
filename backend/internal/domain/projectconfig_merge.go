package domain

import (
	"fmt"
	"reflect"
	"strings"
)

// MergeConfigFields returns stored with ONLY the named fields overwritten from
// incoming. Every other key - including keys no caller can even name, such as
// simProfile or reviewers - is carried through byte for byte.
//
// It exists because a partial write must not be expressible as a whole-config
// write. `ao project set-config <id> --pause-before-implementing` used to send a
// config built from flag variables alone, so every field whose flag was absent
// arrived as its zero value and replaced what was stored: one flag destroyed
// five keys and emptied a sixth, and the command exited 0. The caller now sends
// what it wants written plus the list of fields it actually named, and the
// merge happens here, on the daemon, inside the same read-modify-write that
// stores the result.
//
// fields are dotted JSON names, exactly as the config is spelled on the wire and
// in `ao project get --json`: "defaultBranch", "gitConvention.branchPrefix",
// "worker.agent". They are resolved by reflection against ProjectConfig's own
// json tags rather than a hand-written switch, so a field added to the config is
// mergeable the day it exists and there is no parallel list to fall out of sync.
//
// A field is copied WHOLE at the leaf the path names: a slice or map replaces
// its stored counterpart rather than being appended to, so `--env A=B` states the
// env, and removing an entry stays possible. Naming a parent instead of a leaf
// ("gitConvention") is legal and replaces the whole subtree.
//
// A path that names no field is an error, never a silent skip. Silently
// dropping it is how the original bug behaved: a write that reports success
// while doing something other than what was asked.
func MergeConfigFields(stored, incoming ProjectConfig, fields []string) (ProjectConfig, error) {
	merged := stored
	dst := reflect.ValueOf(&merged).Elem()
	src := reflect.ValueOf(incoming)
	for _, path := range fields {
		dstField, srcField, err := resolveConfigField(dst, src, path)
		if err != nil {
			return ProjectConfig{}, err
		}
		dstField.Set(srcField)
	}
	return merged, nil
}

// resolveConfigField walks one dotted JSON path down both configs in lockstep
// and returns the two values it names. Walking both together is what keeps the
// copy type-safe: the fields are found by the same index in the same type, so
// the Set can never be a mismatched assignment.
func resolveConfigField(dst, src reflect.Value, path string) (reflect.Value, reflect.Value, error) {
	if strings.TrimSpace(path) == "" {
		return reflect.Value{}, reflect.Value{}, fmt.Errorf("mergeFields: empty field path")
	}
	for _, segment := range strings.Split(path, ".") {
		// Pointer fields (SimProfile) are replaceable whole but not walked
		// into: nil and present-but-empty mean different things there, and
		// merging into a subtree that may not exist would have to invent one.
		if dst.Kind() != reflect.Struct {
			return reflect.Value{}, reflect.Value{}, fmt.Errorf(
				"mergeFields: %q names a field inside %s, which is not a config object", path, dst.Kind())
		}
		index, ok := configFieldIndex(dst.Type(), segment)
		if !ok {
			return reflect.Value{}, reflect.Value{}, fmt.Errorf("mergeFields: %q is not a project config field", path)
		}
		dst, src = dst.Field(index), src.Field(index)
	}
	return dst, src, nil
}

// configFieldIndex finds the struct field carrying the given JSON name.
func configFieldIndex(t reflect.Type, name string) (int, bool) {
	for i := 0; i < t.NumField(); i++ {
		field := t.Field(i)
		if !field.IsExported() {
			continue
		}
		tag, _, _ := strings.Cut(field.Tag.Get("json"), ",")
		if tag == "-" {
			continue
		}
		if tag == "" {
			tag = field.Name
		}
		if tag == name {
			return i, true
		}
	}
	return 0, false
}
