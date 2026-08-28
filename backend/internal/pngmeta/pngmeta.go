// Package pngmeta reads and writes PNG text chunks, so a screenshot can carry a
// fact about itself.
//
// It exists for one fact in particular: which build of the app was on the device
// when the frame was captured. Putting it INSIDE the file rather than beside it
// is the whole point - evidence gets moved, attached, downloaded and dragged
// into the desktop app by a person who was never told there was a sidecar to
// bring along, and a fact that travels separately is a fact that will one day
// arrive without its picture.
//
// The format is the PNG spec's tEXt chunk: a Latin-1 keyword, a NUL, and a
// Latin-1 value. Every PNG reader in existence ignores chunks it does not know,
// so a screenshot with one of these in it opens exactly as it did before.
package pngmeta

import (
	"bytes"
	"encoding/binary"
	"errors"
	"fmt"
	"hash/crc32"
	"os"
)

// ErrNotPNG means the file does not start with the PNG signature.
var ErrNotPNG = errors.New("pngmeta: not a PNG")

// signature is the 8 bytes every PNG starts with.
var signature = []byte{0x89, 'P', 'N', 'G', '\r', '\n', 0x1a, '\n'}

const (
	textChunk = "tEXt"
	// maxKeywordLen is the spec's limit on a tEXt keyword.
	maxKeywordLen = 79
	// chunkHeaderLen is the 4-byte length plus the 4-byte type; chunkCRCLen is
	// the trailing CRC.
	chunkHeaderLen = 8
	chunkCRCLen    = 4
)

// Set writes key=value into the PNG at path, replacing any tEXt chunk that
// already carries that keyword.
//
// The chunk goes immediately after IHDR, which is where the spec says textual
// data about the image may live and where a reader will find it without having
// to walk the image data. The file is rewritten in place through a temporary
// file next to it, so a failure leaves the original screenshot intact rather
// than half-written - a truncated piece of evidence being strictly worse than
// one with no build recorded.
func Set(path, key, value string) error {
	if err := validKeyword(key); err != nil {
		return err
	}
	raw, err := os.ReadFile(path) //nolint:gosec // the caller names the file it just wrote
	if err != nil {
		return fmt.Errorf("read png: %w", err)
	}
	chunks, err := split(raw)
	if err != nil {
		return err
	}
	out := bytes.NewBuffer(make([]byte, 0, len(raw)+len(key)+len(value)+chunkHeaderLen+chunkCRCLen))
	out.Write(signature)
	inserted := false
	for _, chunk := range chunks {
		if chunk.kind == textChunk {
			if existing, _, ok := parseText(chunk.data); ok && existing == key {
				// Replaced, not appended: two chunks with one keyword would
				// leave a reader choosing which build the picture is of.
				continue
			}
		}
		writeChunk(out, chunk.kind, chunk.data)
		if !inserted && chunk.kind == "IHDR" {
			writeChunk(out, textChunk, textData(key, value))
			inserted = true
		}
	}
	if !inserted {
		return ErrNotPNG
	}
	tmp := path + ".pngmeta"
	if err := os.WriteFile(tmp, out.Bytes(), 0o600); err != nil {
		return fmt.Errorf("write png: %w", err)
	}
	if err := os.Rename(tmp, path); err != nil {
		_ = os.Remove(tmp)
		return fmt.Errorf("replace png: %w", err)
	}
	return nil
}

// Get returns the value of a tEXt keyword. ok=false means the file has no such
// chunk - which is the ordinary state of a PNG from anywhere else, and never an
// error.
func Get(path, key string) (string, bool) {
	raw, err := os.ReadFile(path) //nolint:gosec // the caller names the file it is inspecting
	if err != nil {
		return "", false
	}
	return GetBytes(raw, key)
}

// GetBytes is Get over bytes already in hand.
func GetBytes(raw []byte, key string) (string, bool) {
	chunks, err := split(raw)
	if err != nil {
		return "", false
	}
	for _, chunk := range chunks {
		if chunk.kind != textChunk {
			continue
		}
		if found, value, ok := parseText(chunk.data); ok && found == key {
			return value, true
		}
	}
	return "", false
}

type chunk struct {
	kind string
	data []byte
}

// split walks the chunk stream. A file that does not decode as PNG chunks is
// reported as not a PNG rather than partially rewritten.
func split(raw []byte) ([]chunk, error) {
	if len(raw) < len(signature) || !bytes.Equal(raw[:len(signature)], signature) {
		return nil, ErrNotPNG
	}
	chunks := []chunk{}
	for offset := len(signature); offset < len(raw); {
		if offset+chunkHeaderLen+chunkCRCLen > len(raw) {
			return nil, ErrNotPNG
		}
		size := int(binary.BigEndian.Uint32(raw[offset : offset+4]))
		kind := string(raw[offset+4 : offset+chunkHeaderLen])
		end := offset + chunkHeaderLen + size + chunkCRCLen
		if size < 0 || end > len(raw) {
			return nil, ErrNotPNG
		}
		chunks = append(chunks, chunk{kind: kind, data: raw[offset+chunkHeaderLen : offset+chunkHeaderLen+size]})
		offset = end
	}
	if len(chunks) == 0 {
		return nil, ErrNotPNG
	}
	return chunks, nil
}

func writeChunk(out *bytes.Buffer, kind string, data []byte) {
	var size [4]byte
	binary.BigEndian.PutUint32(size[:], uint32(len(data))) //nolint:gosec // chunk data is bounded by the file it came from
	out.Write(size[:])
	sum := crc32.NewIEEE()
	out.WriteString(kind)
	sum.Write([]byte(kind))
	out.Write(data)
	sum.Write(data)
	var crc [4]byte
	binary.BigEndian.PutUint32(crc[:], sum.Sum32())
	out.Write(crc[:])
}

func textData(key, value string) []byte {
	data := make([]byte, 0, len(key)+1+len(value))
	data = append(data, key...)
	data = append(data, 0)
	return append(data, value...)
}

func parseText(data []byte) (key, value string, ok bool) {
	sep := bytes.IndexByte(data, 0)
	if sep < 0 {
		return "", "", false
	}
	return string(data[:sep]), string(data[sep+1:]), true
}

// validKeyword enforces the spec's rule for a tEXt keyword: 1 to 79 printable
// Latin-1 characters, with no leading or trailing space.
func validKeyword(key string) error {
	if key == "" || len(key) > maxKeywordLen {
		return fmt.Errorf("pngmeta: keyword must be 1-%d characters, got %d", maxKeywordLen, len(key))
	}
	if key[0] == ' ' || key[len(key)-1] == ' ' {
		return errors.New("pngmeta: keyword must not start or end with a space")
	}
	for i := range len(key) {
		c := key[i]
		if (c < 32 || c > 126) && (c < 161) {
			return fmt.Errorf("pngmeta: keyword must be printable Latin-1, got byte %d", c)
		}
	}
	return nil
}
