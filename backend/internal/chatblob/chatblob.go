// Package chatblob keeps the binary payloads of a chat session's event log
// (the base64 images a Read returns or the human attaches) in content-addressed
// files next to the log, so the log and every replay of it carry a short
// reference instead of the bytes.
//
// A stored payload is a file named <sha256 of the bytes>.<ext> in the blob
// directory. In a log payload, a base64 source
//
//	{"type":"base64","media_type":"image/png","data":"iVBOR..."}
//
// becomes
//
//	{"type":"ao_blob","media_type":"image/png","blob":"<sha256>.png","size":270258}
//
// and the copy claude also puts in tool_use_result.file.base64 becomes
// tool_use_result.file.blob.
package chatblob

import (
	"bytes"
	"crypto/sha256"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"

	"github.com/aoagents/agent-orchestrator/backend/internal/fsatomic"
)

const refType = "ao_blob"

var extByMIME = map[string]string{
	"image/png":       "png",
	"image/jpeg":      "jpg",
	"image/gif":       "gif",
	"image/webp":      "webp",
	"application/pdf": "pdf",
}

var mimeByExt = map[string]string{"bin": "application/octet-stream"}

func init() {
	for m, ext := range extByMIME {
		mimeByExt[ext] = m
	}
}

// Store is one session's blob directory, created on first write.
type Store struct{ dir string }

// ForLog returns the store kept next to the event log at logPath.
func ForLog(logPath string) Store { return Store{dir: filepath.Join(filepath.Dir(logPath), "blobs")} }

// Path returns the file of the blob named name, or false when name is not a
// blob name this package writes.
func (s Store) Path(name string) (string, bool) {
	sum, ext, ok := strings.Cut(name, ".")
	if !ok || len(sum) != sha256.Size*2 || mimeByExt[ext] == "" {
		return "", false
	}
	for _, c := range sum {
		if (c < '0' || c > '9') && (c < 'a' || c > 'f') {
			return "", false
		}
	}
	return filepath.Join(s.dir, name), true
}

// MIME is the media type of the blob named name.
func MIME(name string) string {
	_, ext, _ := strings.Cut(name, ".")
	return mimeByExt[ext]
}

// Externalize stores every base64 payload in p and returns p with references
// in their place. A payload with nothing to store comes back unchanged, byte
// for byte. Running it again on its own output changes nothing.
func (s Store) Externalize(p json.RawMessage) (json.RawMessage, error) {
	if !bytes.Contains(p, []byte(`"base64"`)) {
		return p, nil
	}
	v, ok := decode(p)
	if !ok {
		return p, nil
	}
	changed := false
	if f := resultFile(v); f != nil {
		raw, _ := f["base64"].(string)
		mime, _ := f["type"].(string)
		name, _, ok, err := s.storeBase64(raw, mime)
		if err != nil {
			return p, err
		}
		if ok {
			delete(f, "base64")
			f["blob"] = name
			changed = true
		}
	}
	sourced, err := eachSource(v, func(src map[string]any) (map[string]any, error) {
		if src["type"] != "base64" {
			return nil, nil
		}
		raw, _ := src["data"].(string)
		mime, _ := src["media_type"].(string)
		name, size, ok, err := s.storeBase64(raw, mime)
		if err != nil || !ok {
			return nil, err
		}
		return map[string]any{"type": refType, "media_type": mime, "blob": name, "size": size}, nil
	})
	if err != nil || (!changed && !sourced) {
		return p, err
	}
	return encode(v)
}

// Inline puts the stored bytes back into every source reference in p, so a
// reader that knows only base64 sources gets each image once. A reference
// whose blob is missing stays a reference. The copy in
// tool_use_result.file, inline or a reference, is dropped: nothing reads it.
func (s Store) Inline(p json.RawMessage) (json.RawMessage, error) {
	if !bytes.Contains(p, []byte(`"`+refType+`"`)) && !bytes.Contains(p, []byte(`"base64"`)) {
		return p, nil
	}
	v, ok := decode(p)
	if !ok {
		return p, nil
	}
	changed := false
	if f := resultFile(v); f != nil {
		_, inline := f["base64"]
		_, ref := f["blob"]
		delete(f, "base64")
		delete(f, "blob")
		changed = inline || ref
	}
	sourced, err := eachSource(v, func(src map[string]any) (map[string]any, error) {
		name, _ := src["blob"].(string)
		path, ok := s.Path(name)
		if src["type"] != refType || !ok {
			return nil, nil
		}
		data, err := os.ReadFile(path) //nolint:gosec // G304: Path accepts only <sha256>.<ext> names inside the store
		if errors.Is(err, fs.ErrNotExist) {
			return nil, nil
		}
		if err != nil {
			return nil, fmt.Errorf("chatblob: read %s: %w", name, err)
		}
		mime, _ := src["media_type"].(string)
		return map[string]any{"type": "base64", "media_type": mime, "data": base64.StdEncoding.EncodeToString(data)}, nil
	})
	if err != nil || (!changed && !sourced) {
		return p, err
	}
	return encode(v)
}

func resultFile(v any) map[string]any {
	top, _ := v.(map[string]any)
	r, _ := top["tool_use_result"].(map[string]any)
	f, _ := r["file"].(map[string]any)
	return f
}

func eachSource(v any, fn func(map[string]any) (map[string]any, error)) (bool, error) {
	changed := false
	var children []any
	switch v := v.(type) {
	case map[string]any:
		if src, ok := v["source"].(map[string]any); ok {
			next, err := fn(src)
			if err != nil {
				return false, err
			}
			if next != nil {
				v["source"] = next
				changed = true
			}
		}
		for _, child := range v {
			children = append(children, child)
		}
	case []any:
		children = v
	}
	for _, child := range children {
		c, err := eachSource(child, fn)
		if err != nil {
			return false, err
		}
		changed = changed || c
	}
	return changed, nil
}

func (s Store) storeBase64(raw, mime string) (string, int, bool, error) {
	if raw == "" {
		return "", 0, false, nil
	}
	data, err := base64.StdEncoding.DecodeString(raw)
	if err != nil {
		return "", 0, false, nil //nolint:nilerr // data that is not base64 stays inline as written
	}
	ext := extByMIME[mime]
	if ext == "" {
		ext = "bin"
	}
	sum := sha256.Sum256(data)
	name := hex.EncodeToString(sum[:]) + "." + ext
	path := filepath.Join(s.dir, name)
	if _, err := os.Stat(path); err == nil {
		return name, len(data), true, nil
	}
	if err := os.MkdirAll(s.dir, 0o700); err != nil {
		return "", 0, false, fmt.Errorf("chatblob: create dir: %w", err)
	}
	if err := fsatomic.WriteFile(path, data, 0o600); err != nil {
		return "", 0, false, fmt.Errorf("chatblob: write %s: %w", name, err)
	}
	return name, len(data), true, nil
}

func decode(p []byte) (any, bool) {
	dec := json.NewDecoder(bytes.NewReader(p))
	dec.UseNumber()
	var v any
	return v, dec.Decode(&v) == nil
}

func encode(v any) (json.RawMessage, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(v); err != nil {
		return nil, fmt.Errorf("chatblob: encode: %w", err)
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
