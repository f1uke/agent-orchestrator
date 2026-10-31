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

// SourceType is the source type of a stored payload's reference.
const SourceType = "ao_blob"

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

// Store is one session's blob directory.
type Store struct{ dir string }

// New returns the store kept in dir. The directory is created on first write.
func New(dir string) Store { return Store{dir: dir} }

// ForLog returns the store kept next to the event log at logPath.
func ForLog(logPath string) Store { return New(filepath.Join(filepath.Dir(logPath), "blobs")) }

// Dir is the blob directory.
func (s Store) Dir() string { return s.dir }

// Path returns the file of the blob named name and its media type, or false
// when name is not a blob name this package writes.
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
	changed, err := s.externalize(v, true)
	if err != nil || !changed {
		return p, err
	}
	return encode(v)
}

// Inline puts the stored bytes back into every source reference in p, so a
// reader that knows only base64 sources gets each image once. A reference
// whose blob is missing stays a reference. The copy in
// tool_use_result.file, inline or a reference, is dropped: nothing reads it.
func (s Store) Inline(p json.RawMessage) (json.RawMessage, error) {
	if !bytes.Contains(p, []byte(`"`+SourceType+`"`)) && !bytes.Contains(p, []byte(`"base64"`)) {
		return p, nil
	}
	v, ok := decode(p)
	if !ok {
		return p, nil
	}
	changed := dropResultCopy(v)
	inlined, err := s.inline(v)
	changed = changed || inlined
	if err != nil || !changed {
		return p, err
	}
	return encode(v)
}

func (s Store) externalize(v any, top bool) (bool, error) {
	changed := false
	switch v := v.(type) {
	case map[string]any:
		if top {
			if r, ok := v["tool_use_result"].(map[string]any); ok {
				if f, ok := r["file"].(map[string]any); ok {
					mime, _ := f["type"].(string)
					if name, _, ok, err := s.storeBase64(f["base64"], mime); err != nil {
						return false, err
					} else if ok {
						delete(f, "base64")
						f["blob"] = name
						changed = true
					}
				}
			}
		}
		if src, ok := v["source"].(map[string]any); ok && src["type"] == "base64" {
			mime, _ := src["media_type"].(string)
			raw, _ := src["data"].(string)
			if name, size, ok, err := s.storeBase64(raw, mime); err != nil {
				return false, err
			} else if ok {
				v["source"] = map[string]any{"type": SourceType, "media_type": mime, "blob": name, "size": size}
				changed = true
			}
		}
		for _, child := range v {
			c, err := s.externalize(child, false)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	case []any:
		for _, child := range v {
			c, err := s.externalize(child, false)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	}
	return changed, nil
}

func dropResultCopy(v any) bool {
	top, _ := v.(map[string]any)
	r, _ := top["tool_use_result"].(map[string]any)
	f, _ := r["file"].(map[string]any)
	_, inline := f["base64"]
	_, ref := f["blob"]
	delete(f, "base64")
	delete(f, "blob")
	return inline || ref
}

func (s Store) inline(v any) (bool, error) {
	changed := false
	switch v := v.(type) {
	case map[string]any:
		if src, ok := v["source"].(map[string]any); ok && src["type"] == SourceType {
			name, _ := src["blob"].(string)
			if path, ok := s.Path(name); ok {
				data, err := os.ReadFile(path) //nolint:gosec // G304: Path accepts only <sha256>.<ext> names inside the store
				switch {
				case errors.Is(err, fs.ErrNotExist):
				case err != nil:
					return false, fmt.Errorf("chatblob: read %s: %w", name, err)
				default:
					mime, _ := src["media_type"].(string)
					v["source"] = map[string]any{"type": "base64", "media_type": mime, "data": base64.StdEncoding.EncodeToString(data)}
					changed = true
				}
			}
		}
		for _, child := range v {
			c, err := s.inline(child)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	case []any:
		for _, child := range v {
			c, err := s.inline(child)
			if err != nil {
				return false, err
			}
			changed = changed || c
		}
	}
	return changed, nil
}

// storeBase64 writes the decoded bytes of raw unless a blob with them exists,
// and returns the blob's name and size. It reports false, and writes nothing,
// when raw is not a non-empty base64 string.
func (s Store) storeBase64(raw any, mime string) (string, int, bool, error) {
	str, _ := raw.(string)
	if str == "" {
		return "", 0, false, nil
	}
	data, ok := decodeBase64(str)
	if !ok {
		return "", 0, false, nil
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

// decode parses p keeping numbers as written; false means p is not JSON
// and is passed on untouched.
// decodeBase64 reports false for data that is not standard base64, which
// is left inline as written.
func decodeBase64(s string) ([]byte, bool) {
	data, err := base64.StdEncoding.DecodeString(s)
	return data, err == nil
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
