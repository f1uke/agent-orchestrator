package controllers

import (
	"crypto/rand"
	"encoding/hex"
	"errors"
	"io"
	"net/http"
	"os"
	"path/filepath"
	"strings"
	"unicode"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/apispec"
	"github.com/aoagents/agent-orchestrator/backend/internal/httpd/envelope"
	"github.com/aoagents/agent-orchestrator/backend/internal/sessionhost"
)

const maxChatAttachmentBytes = 50 << 20

// attach saves a file the human attached in the chat composer under the session's attachments folder,
// which the session host gives claude with --add-dir, and answers with the path to tell claude.
func (c *ChatFilesController) attach(w http.ResponseWriter, r *http.Request) {
	if c.Sessions == nil || c.DataDir == "" {
		apispec.NotImplemented(w, r, "POST", "/api/v1/sessions/{sessionId}/chat/attachments")
		return
	}
	name := r.URL.Query().Get("name")
	if !attachmentName(name) {
		chatBadRequest(w, r, "INVALID_NAME", "name must be a file name: no path, no control characters, at most 255 bytes")
		return
	}
	id := sessionID(r)
	sess, err := c.Sessions.Get(r.Context(), id)
	if err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	if sess.Transport != domain.TransportChat {
		envelope.WriteAPIError(w, r, http.StatusNotFound, "not_found", "CHAT_SESSION_NOT_FOUND", "Attachments go to chat sessions only", nil)
		return
	}
	data, err := io.ReadAll(http.MaxBytesReader(w, r.Body, maxChatAttachmentBytes))
	if err != nil {
		var tooLarge *http.MaxBytesError
		if errors.As(err, &tooLarge) {
			envelope.WriteAPIError(w, r, http.StatusRequestEntityTooLarge, "payload_too_large", "FILE_TOO_LARGE", "A file is at most 50 MiB", nil)
			return
		}
		chatBadRequest(w, r, "INVALID_BODY", "Could not read the file")
		return
	}
	// A fresh folder per upload keeps two files with one name apart.
	var nonce [8]byte
	if _, err := rand.Read(nonce[:]); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	dir := filepath.Join(sessionhost.AttachmentsDir(c.DataDir, string(id)), hex.EncodeToString(nonce[:]))
	if err := os.MkdirAll(dir, 0o700); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	path := filepath.Join(dir, name)
	if err := os.WriteFile(path, data, 0o600); err != nil {
		envelope.WriteError(w, r, err)
		return
	}
	envelope.WriteJSON(w, http.StatusOK, ChatAttachment{Path: path, Name: name, Size: int64(len(data))})
}

func attachmentName(name string) bool {
	if name == "" || name == "." || name == ".." || len(name) > 255 || strings.ContainsAny(name, `/\`) {
		return false
	}
	return !strings.ContainsFunc(name, unicode.IsControl)
}
