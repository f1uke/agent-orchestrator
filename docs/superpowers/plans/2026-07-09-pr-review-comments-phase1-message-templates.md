# PR Review Comments — Phase 1: Editable message templates Implementation Plan

> **For agentic workers:** REQUIRED SUB-SKILL: Use superpowers:subagent-driven-development (recommended) or superpowers:executing-plans to implement this plan task-by-task. Steps use checkbox (`- [ ]`) syntax for tracking.

**Goal:** Make every runtime nudge AO sends into a worker's pane render from a globally-editable template, so operators can customize them from the Settings page — without changing any nudge's default behavior.

**Architecture:** A new `messagetemplates` package holds each nudge's built-in default (Go `text/template`), placeholder docs, and a `Renderer` that resolves override-or-default and falls back to the default if a user edit fails to render. Overrides persist in the existing `promptoverrides` JSON store (new `Templates map[string]string`). A new `/api/v1/settings/message-templates` API (code-first) and a `MessageTemplatesSection` in Global Settings edit them. `lifecycle.reactions` renders each nudge through the `Renderer` (injected via the existing `Option` pattern) instead of hardcoded strings. This is a pure refactor: defaults reproduce today's exact messages.

**Tech Stack:** Go (backend, `text/template`, chi, swaggest code-first OpenAPI via `specgen`), React + TanStack Query + openapi-fetch (frontend), vitest + testing-library.

## Global Constraints

- App state resolves under `~/.ao` only (data dir = `cfg.DataDir`). Never touch OS-default app-data locations.
- API is **code-first**: edit `backend/internal/httpd/controllers/dto.go` + `backend/internal/httpd/apispec/specgen/build.go` (operation registry + `schemaNames` map), then run `npm run api` (from repo root) to regenerate `backend/internal/httpd/apispec/openapi.yaml` and `frontend/src/api/schema.ts`. Never hand-edit those two generated files. `go test ./internal/httpd/...` runs the spec-drift guard `TestBuild_MatchesEmbedded`.
- Backend test + lint gate (repo root): `npm run lint` (= `cd backend && go test ./... && golangci-lint run`).
- Frontend gates (from `frontend/`): `npm run test` (vitest) and `npm run typecheck` (`tsc --noEmit`). There is no frontend lint script; formatting follows the repo `.prettierrc` — **use tabs**, matching surrounding code.
- After any frontend build/test, revert incidental `routeTree.gen.ts` and `pnpm-lock.yaml` churn — do not commit it.
- Nudge messages reach a worker's live terminal pane. Dynamic values injected into templates (comment bodies, CI log tails, PR fields) are attacker-influenceable and MUST be passed through `domain.SanitizeControlChars` **before** being placed in the template data — exactly as the current code does. Template text itself (built-in or operator-edited) is trusted.
- Commit after each task (frequent commits). Branch: `bugfix/PROJ-2272-gitlab-mr-detection` (PR #36).

---

## File Structure

Created:
- `backend/internal/messagetemplates/templates.go` — `Name` enum, `KnownNames`, `Valid`, `Default`, `Placeholders`, the per-template data structs, and pure `Execute`.
- `backend/internal/messagetemplates/templates_test.go` — defaults render, placeholder docs, golden output tests.
- `backend/internal/messagetemplates/renderer.go` — `Renderer` (override-or-default + fallback).
- `backend/internal/messagetemplates/renderer_test.go`.
- `frontend/src/renderer/components/MessageTemplatesSection.tsx` — Global Settings card mirroring `SystemPromptsSection`.
- `frontend/src/renderer/components/MessageTemplatesSection.test.tsx`.

Modified:
- `backend/internal/promptoverrides/store.go` — add `Templates map[string]string` + `GetTemplate`/`SetTemplate`/`ClearTemplate`; back-compat load.
- `backend/internal/promptoverrides/store_test.go` — template round-trip + persistence.
- `backend/internal/httpd/controllers/dto.go` — new message-template DTOs.
- `backend/internal/httpd/controllers/settings.go` — `MessageTemplatesService` interface, controller field, routes, handlers.
- `backend/internal/httpd/controllers/settings_test.go` — handler tests.
- `backend/internal/httpd/apispec/specgen/build.go` — 3 operations + `schemaNames` entries.
- `backend/internal/httpd/api.go` — `APIDeps.MessageTemplates` field, controller wiring.
- `backend/internal/daemon/daemon.go` — pass `promptOverrides` as `MessageTemplates` dep.
- `backend/internal/lifecycle/manager.go` — `renderer` field + `WithMessageRenderer` Option.
- `backend/internal/lifecycle/reactions.go` — render nudges via the injected renderer.
- `backend/internal/lifecycle/reactions_test.go` (and/or `manager_test.go`) — assertions updated to template output.
- `backend/internal/daemon/lifecycle_wiring.go` — construct the renderer and pass it into `startLifecycle` → `lifecycle.New`.
- `frontend/src/renderer/components/GlobalSettingsForm.tsx` — render `<MessageTemplatesSection />`.
- Generated (via `npm run api`): `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`.

---

## Task 1: `messagetemplates` package — names, defaults, placeholders, pure `Execute`

**Files:**
- Create: `backend/internal/messagetemplates/templates.go`
- Test: `backend/internal/messagetemplates/templates_test.go`

**Interfaces:**
- Produces:
  - `type Name string` with consts `NameReviewCommentDispatch`, `NameCIFailing`, `NameMergeConflict`, `NameTrackerBotComment`, `NameAOReviewerBatch`, `NameAOReviewerSingle`.
  - `func KnownNames() []Name`, `func (Name) Valid() bool`, `func Default(Name) string`, `func Placeholders(Name) []string`.
  - `func Execute(tmplText string, data any) (string, error)` — pure `text/template` render.
  - Data structs: `ReviewCommentData{Comments string}`, `CIFailingData{LogTail string}`, `MergeConflictData{}`, `TrackerBotData{Comments string}`, `AOReviewItem{Index int; PRURL, Verdict, TargetSHA, ReviewID, Body string}`, `AOReviewerBatchData{Count int; Reviews []AOReviewItem}`, `AOReviewerSingleData{PRURL, Verdict, ReviewID, Body string}`.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/messagetemplates/templates_test.go`:

```go
package messagetemplates

import "testing"

func TestExecuteRendersData(t *testing.T) {
	out, err := Execute("hi {{.Comments}}", ReviewCommentData{Comments: "there"})
	if err != nil {
		t.Fatalf("Execute: %v", err)
	}
	if out != "hi there" {
		t.Fatalf("got %q, want %q", out, "hi there")
	}
}

func TestExecuteReportsParseError(t *testing.T) {
	if _, err := Execute("{{.Broken", ReviewCommentData{}); err == nil {
		t.Fatal("expected parse error, got nil")
	}
}

func TestKnownNamesValidAndHaveDefaults(t *testing.T) {
	names := KnownNames()
	if len(names) != 6 {
		t.Fatalf("want 6 templates, got %d", len(names))
	}
	for _, n := range names {
		if !n.Valid() {
			t.Fatalf("%q not Valid()", n)
		}
		if Default(n) == "" {
			t.Fatalf("%q has empty default", n)
		}
	}
	if Name("bogus").Valid() {
		t.Fatal("bogus should be invalid")
	}
}

func TestDefaultsRenderWithZeroData(t *testing.T) {
	// Every built-in default must parse+execute against its data struct so a
	// bad built-in can never strand a nudge. Zero-value data is the worst case.
	cases := map[Name]any{
		NameReviewCommentDispatch: ReviewCommentData{},
		NameCIFailing:             CIFailingData{},
		NameMergeConflict:         MergeConflictData{},
		NameTrackerBotComment:     TrackerBotData{},
		NameAOReviewerBatch:       AOReviewerBatchData{},
		NameAOReviewerSingle:      AOReviewerSingleData{},
	}
	for n, data := range cases {
		if _, err := Execute(Default(n), data); err != nil {
			t.Fatalf("default %q failed to render: %v", n, err)
		}
	}
}

func TestReviewCommentDefaultOmitsBlankComments(t *testing.T) {
	out, err := Execute(Default(NameReviewCommentDispatch), ReviewCommentData{})
	if err != nil {
		t.Fatal(err)
	}
	if out != "A reviewer left feedback on your PR. Address it and push." {
		t.Fatalf("blank-comment render = %q", out)
	}
	out, err = Execute(Default(NameReviewCommentDispatch), ReviewCommentData{Comments: "remove this"})
	if err != nil {
		t.Fatal(err)
	}
	want := "A reviewer left feedback on your PR. Address it and push.\n\nremove this"
	if out != want {
		t.Fatalf("with-comment render = %q, want %q", out, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/messagetemplates/`
Expected: FAIL — package/functions do not exist (build error).

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/messagetemplates/templates.go`:

```go
// Package messagetemplates holds the built-in default text (Go text/template)
// for every runtime nudge AO sends into a worker's pane, the documented
// placeholder set for each, and a pure Execute. The lifecycle reactor and the
// settings API read one source of truth for defaults + Reset-to-default.
package messagetemplates

import (
	"bytes"
	"fmt"
	"text/template"
)

// Name enumerates the editable nudge templates.
type Name string

const (
	NameReviewCommentDispatch Name = "review-comment-dispatch"
	NameCIFailing             Name = "ci-failing"
	NameMergeConflict         Name = "merge-conflict"
	NameTrackerBotComment     Name = "tracker-bot-comment"
	NameAOReviewerBatch       Name = "ao-reviewer-batch"
	NameAOReviewerSingle      Name = "ao-reviewer-single"
)

// KnownNames is the stable order the settings UI renders editors in.
func KnownNames() []Name {
	return []Name{
		NameReviewCommentDispatch,
		NameCIFailing,
		NameMergeConflict,
		NameTrackerBotComment,
		NameAOReviewerBatch,
		NameAOReviewerSingle,
	}
}

// Valid reports whether n is a known template name.
func (n Name) Valid() bool {
	switch n {
	case NameReviewCommentDispatch, NameCIFailing, NameMergeConflict,
		NameTrackerBotComment, NameAOReviewerBatch, NameAOReviewerSingle:
		return true
	}
	return false
}

// ReviewCommentData is the render context for NameReviewCommentDispatch.
type ReviewCommentData struct{ Comments string }

// CIFailingData is the render context for NameCIFailing.
type CIFailingData struct{ LogTail string }

// MergeConflictData is the (empty) render context for NameMergeConflict.
type MergeConflictData struct{}

// TrackerBotData is the render context for NameTrackerBotComment.
type TrackerBotData struct{ Comments string }

// AOReviewItem is one review inside an AO reviewer batch.
type AOReviewItem struct {
	Index     int
	PRURL     string
	Verdict   string
	TargetSHA string
	ReviewID  string
	Body      string
}

// AOReviewerBatchData is the render context for NameAOReviewerBatch.
type AOReviewerBatchData struct {
	Count   int
	Reviews []AOReviewItem
}

// AOReviewerSingleData is the render context for NameAOReviewerSingle.
type AOReviewerSingleData struct {
	PRURL    string
	Verdict  string
	ReviewID string
	Body     string
}

// Placeholders returns the documented template tokens for a name, for the
// settings editor. Unknown names return nil.
func Placeholders(n Name) []string {
	switch n {
	case NameReviewCommentDispatch, NameTrackerBotComment:
		return []string{"{{.Comments}}"}
	case NameCIFailing:
		return []string{"{{.LogTail}}"}
	case NameMergeConflict:
		return nil
	case NameAOReviewerBatch:
		return []string{"{{.Count}}", "{{range .Reviews}}", "{{.Index}}", "{{.PRURL}}", "{{.Verdict}}", "{{.TargetSHA}}", "{{.ReviewID}}", "{{.Body}}", "{{end}}"}
	case NameAOReviewerSingle:
		return []string{"{{.PRURL}}", "{{.Verdict}}", "{{.ReviewID}}", "{{.Body}}"}
	}
	return nil
}

// Default returns the built-in default template for a name. Unknown names
// return "". These reproduce the exact pre-templating nudge text.
func Default(n Name) string {
	switch n {
	case NameReviewCommentDispatch:
		return reviewCommentDefault
	case NameCIFailing:
		return ciFailingDefault
	case NameMergeConflict:
		return mergeConflictDefault
	case NameTrackerBotComment:
		return trackerBotDefault
	case NameAOReviewerBatch:
		return aoReviewerBatchDefault
	case NameAOReviewerSingle:
		return aoReviewerSingleDefault
	}
	return ""
}

// Execute parses and renders tmplText against data. It is pure: no override
// resolution, no fallback. Missing keys error (Option "missingkey=error") so a
// typo'd placeholder in an operator edit surfaces instead of printing "<no value>".
func Execute(tmplText string, data any) (string, error) {
	t, err := template.New("msg").Option("missingkey=error").Parse(tmplText)
	if err != nil {
		return "", fmt.Errorf("messagetemplates: parse: %w", err)
	}
	var buf bytes.Buffer
	if err := t.Execute(&buf, data); err != nil {
		return "", fmt.Errorf("messagetemplates: execute: %w", err)
	}
	return buf.String(), nil
}

const reviewCommentDefault = "A reviewer left feedback on your PR. Address it and push.{{if .Comments}}\n\n{{.Comments}}{{end}}"

const ciFailingDefault = "CI is failing on your PR. Review the output below and push a fix.{{if .LogTail}}\n\nFailing output:\n{{.LogTail}}{{end}}"

const mergeConflictDefault = "Your PR has merge conflicts. Rebase onto the base branch and resolve them."

const trackerBotDefault = "A bot left a new comment on your tracker issue. Address it and update the session.{{if .Comments}}\n\n{{.Comments}}{{end}}"

// aoReviewerBatchDefault reproduces the pre-templating loop in
// ApplyReviewBatch byte-for-byte. The leading intro line ends with "\n"; each
// review begins with a blank line ("\n" before "Review N").
const aoReviewerBatchDefault = "[AO reviewer] AO's internal code reviewer submitted {{.Count}} review(s) requesting changes.\n" +
	"{{range .Reviews}}\nReview {{.Index}}\nPR: {{.PRURL}}\nVerdict: {{.Verdict}}" +
	"{{if .TargetSHA}}\nHead commit: {{.TargetSHA}}{{end}}" +
	"{{if .ReviewID}}\nReview: {{.ReviewID}}\nOnce you have addressed it, reply on review {{.ReviewID}} with how you addressed it, then resolve the review comment threads you addressed.{{end}}" +
	"{{if .Body}}\n\nReview body:\n{{.Body}}\n{{end}}{{end}}"

// aoReviewerSingleDefault reproduces the pre-templating ApplyReviewResult text.
const aoReviewerSingleDefault = "[AO reviewer] AO's internal code reviewer submitted a review.\n\nPR: {{.PRURL}}\nVerdict: {{.Verdict}}" +
	"{{if .ReviewID}}\nReview: {{.ReviewID}}\n\nOnce you have addressed it, reply on review {{.ReviewID}} with how you addressed it, then resolve the review comment threads you addressed.{{end}}" +
	"{{if .Body}}\n\nReview body:\n{{.Body}}{{end}}"
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/messagetemplates/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/messagetemplates/templates.go backend/internal/messagetemplates/templates_test.go
git commit -m "feat(messagetemplates): nudge template registry + defaults

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 2: Golden tests locking AO-reviewer template output to the current fmt code

**Files:**
- Test: `backend/internal/messagetemplates/templates_test.go` (append)

**Interfaces:**
- Consumes: `Execute`, `Default`, `AOReviewerBatchData`, `AOReviewItem`, `AOReviewerSingleData` from Task 1.

**Why:** Task 6/7 replaces `fmt.Fprintf` message builders in `reactions.go` with these templates. A golden test proves the template output is byte-identical to today's message, so the refactor is provably behavior-preserving.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/messagetemplates/templates_test.go`:

```go
func TestAOReviewerBatchGolden(t *testing.T) {
	data := AOReviewerBatchData{
		Count: 2,
		Reviews: []AOReviewItem{
			{Index: 1, PRURL: "https://x/pr/1", Verdict: "changes_requested", TargetSHA: "abc", ReviewID: "R1", Body: "fix it"},
			{Index: 2, PRURL: "https://x/pr/2", Verdict: "changes_requested"},
		},
	}
	out, err := Execute(Default(NameAOReviewerBatch), data)
	if err != nil {
		t.Fatal(err)
	}
	want := "[AO reviewer] AO's internal code reviewer submitted 2 review(s) requesting changes.\n" +
		"\nReview 1\nPR: https://x/pr/1\nVerdict: changes_requested" +
		"\nHead commit: abc" +
		"\nReview: R1\nOnce you have addressed it, reply on review R1 with how you addressed it, then resolve the review comment threads you addressed." +
		"\n\nReview body:\nfix it\n" +
		"\nReview 2\nPR: https://x/pr/2\nVerdict: changes_requested"
	if out != want {
		t.Fatalf("batch golden mismatch:\n got %q\nwant %q", out, want)
	}
}

func TestAOReviewerSingleGolden(t *testing.T) {
	out, err := Execute(Default(NameAOReviewerSingle), AOReviewerSingleData{
		PRURL: "https://x/pr/9", Verdict: "changes_requested", ReviewID: "R9", Body: "please fix",
	})
	if err != nil {
		t.Fatal(err)
	}
	want := "[AO reviewer] AO's internal code reviewer submitted a review.\n\nPR: https://x/pr/9\nVerdict: changes_requested" +
		"\nReview: R9\n\nOnce you have addressed it, reply on review R9 with how you addressed it, then resolve the review comment threads you addressed." +
		"\n\nReview body:\nplease fix"
	if out != want {
		t.Fatalf("single golden mismatch:\n got %q\nwant %q", out, want)
	}
}
```

- [ ] **Step 2: Run test to verify it fails (or passes) and fix the template if needed**

Run: `cd backend && go test ./internal/messagetemplates/ -run Golden -v`
Expected: PASS if Task 1's default templates are correct. If it FAILS, the mismatch shows exactly which bytes differ — adjust the `aoReviewerBatchDefault` / `aoReviewerSingleDefault` constants in `templates.go` until the goldens pass. Do NOT change the `want` strings (they are transcribed from the current `reactions.go` `fmt.Fprintf` output).

- [ ] **Step 3: Run full package tests**

Run: `cd backend && go test ./internal/messagetemplates/`
Expected: PASS.

- [ ] **Step 4: Commit**

```bash
git add backend/internal/messagetemplates/templates_test.go backend/internal/messagetemplates/templates.go
git commit -m "test(messagetemplates): golden-lock AO reviewer template output

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 3: `Renderer` — override-or-default with fallback

**Files:**
- Create: `backend/internal/messagetemplates/renderer.go`
- Test: `backend/internal/messagetemplates/renderer_test.go`

**Interfaces:**
- Consumes: `Name`, `Default`, `Execute` from Task 1.
- Produces:
  - `type Renderer struct { ... }`
  - `func NewRenderer(overrides func() map[string]string) *Renderer`
  - `func (r *Renderer) Render(name Name, data any) (string, error)` — returns the rendered string (default-fallback applied) and, when a non-empty override failed to render, a non-nil error for the caller to log while still using the returned default text.

- [ ] **Step 1: Write the failing test**

Create `backend/internal/messagetemplates/renderer_test.go`:

```go
package messagetemplates

import (
	"strings"
	"testing"
)

func TestRendererUsesDefaultWhenNoOverride(t *testing.T) {
	r := NewRenderer(func() map[string]string { return nil })
	out, err := r.Render(NameMergeConflict, MergeConflictData{})
	if err != nil {
		t.Fatal(err)
	}
	if out != Default(NameMergeConflict) {
		t.Fatalf("got %q", out)
	}
}

func TestRendererUsesOverride(t *testing.T) {
	r := NewRenderer(func() map[string]string {
		return map[string]string{string(NameReviewCommentDispatch): "custom: {{.Comments}}"}
	})
	out, err := r.Render(NameReviewCommentDispatch, ReviewCommentData{Comments: "hi"})
	if err != nil {
		t.Fatal(err)
	}
	if out != "custom: hi" {
		t.Fatalf("got %q", out)
	}
}

func TestRendererFallsBackWhenOverrideFails(t *testing.T) {
	r := NewRenderer(func() map[string]string {
		return map[string]string{string(NameReviewCommentDispatch): "{{.Nonexistent}}"}
	})
	out, err := r.Render(NameReviewCommentDispatch, ReviewCommentData{Comments: "hi"})
	if err == nil {
		t.Fatal("expected an error reporting the override failure")
	}
	// Still returns a usable default render.
	if !strings.HasPrefix(out, "A reviewer left feedback") {
		t.Fatalf("fallback render = %q", out)
	}
}

func TestRendererBlankOverrideUsesDefault(t *testing.T) {
	r := NewRenderer(func() map[string]string {
		return map[string]string{string(NameCIFailing): ""}
	})
	out, err := r.Render(NameCIFailing, CIFailingData{})
	if err != nil {
		t.Fatal(err)
	}
	if out != Default(NameCIFailing) {
		t.Fatalf("blank override should use default, got %q", out)
	}
}
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/messagetemplates/ -run Renderer`
Expected: FAIL — `NewRenderer`/`Renderer` undefined.

- [ ] **Step 3: Write minimal implementation**

Create `backend/internal/messagetemplates/renderer.go`:

```go
package messagetemplates

import "fmt"

// Renderer resolves the effective template for a Name (operator override else
// built-in default) and renders it. A non-empty override that fails to parse or
// execute (a bad operator edit) never drops a nudge: Render falls back to the
// built-in default and returns the override error for the caller to log.
type Renderer struct {
	overrides func() map[string]string
}

// NewRenderer builds a Renderer over an overrides source. A nil source (or a
// source returning nil) means "always use defaults".
func NewRenderer(overrides func() map[string]string) *Renderer {
	if overrides == nil {
		overrides = func() map[string]string { return nil }
	}
	return &Renderer{overrides: overrides}
}

// Render returns the rendered message for name. The returned string is always
// usable (default applied on override failure); the error is non-nil only when
// a non-empty override failed to render.
func (r *Renderer) Render(name Name, data any) (string, error) {
	text := Default(name)
	usedOverride := false
	if ov := r.overrides(); ov != nil {
		if custom, ok := ov[string(name)]; ok && custom != "" {
			text = custom
			usedOverride = true
		}
	}
	out, err := Execute(text, data)
	if err == nil {
		return out, nil
	}
	if usedOverride {
		if def, derr := Execute(Default(name), data); derr == nil {
			return def, fmt.Errorf("messagetemplates: override for %q failed, used default: %w", name, err)
		}
	}
	return "", err
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/messagetemplates/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/messagetemplates/renderer.go backend/internal/messagetemplates/renderer_test.go
git commit -m "feat(messagetemplates): Renderer with override + default fallback

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 4: Persist template overrides in `promptoverrides`

**Files:**
- Modify: `backend/internal/promptoverrides/store.go`
- Test: `backend/internal/promptoverrides/store_test.go`

**Interfaces:**
- Produces (on `*promptoverrides.Store`):
  - `Overrides.Templates map[string]string` (json `templates,omitempty`).
  - `func (s *Store) GetTemplate(name string) (string, bool)`
  - `func (s *Store) SetTemplate(name, text string) error`
  - `func (s *Store) ClearTemplate(name string) error`
  - `Get()` continues to return a deep copy including `Templates`.

Note: the store stays a dumb key/value holder (keys are plain strings); name validity is enforced by the controller/renderer, not here — mirroring how `Overrides.Base` stores arbitrary text but callers pass validated `prompts.Kind`.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/promptoverrides/store_test.go`:

```go
func TestTemplateRoundTripPersists(t *testing.T) {
	dir := t.TempDir()
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetTemplate("ci-failing"); ok {
		t.Fatal("expected no template override initially")
	}
	if err := s.SetTemplate("ci-failing", "custom CI msg"); err != nil {
		t.Fatal(err)
	}
	got, ok := s.GetTemplate("ci-failing")
	if !ok || got != "custom CI msg" {
		t.Fatalf("GetTemplate = %q, %v", got, ok)
	}
	// Reload from disk: override survives.
	s2, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if got, ok := s2.GetTemplate("ci-failing"); !ok || got != "custom CI msg" {
		t.Fatalf("after reload GetTemplate = %q, %v", got, ok)
	}
	// Get() exposes the templates copy without aliasing internal state.
	ov := s2.Get()
	if ov.Templates["ci-failing"] != "custom CI msg" {
		t.Fatalf("Get().Templates = %v", ov.Templates)
	}
	ov.Templates["ci-failing"] = "mutated"
	if got, _ := s2.GetTemplate("ci-failing"); got != "custom CI msg" {
		t.Fatal("Get() must return a copy, not internal state")
	}
	// Clear restores default (absent key).
	if err := s2.ClearTemplate("ci-failing"); err != nil {
		t.Fatal(err)
	}
	if _, ok := s2.GetTemplate("ci-failing"); ok {
		t.Fatal("expected template override cleared")
	}
}

func TestGetHandlesLegacyFileWithoutTemplates(t *testing.T) {
	dir := t.TempDir()
	// A pre-existing overrides file with only "base" and no "templates" key.
	if err := os.WriteFile(filepath.Join(dir, "system-prompt-overrides.json"),
		[]byte(`{"base":{"worker":"x"}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	s, err := NewStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, ok := s.GetTemplate("ci-failing"); ok {
		t.Fatal("legacy file should yield no template overrides")
	}
	// Setting a template must not clobber the existing base override.
	if err := s.SetTemplate("ci-failing", "v"); err != nil {
		t.Fatal(err)
	}
	if s.Get().Base["worker"] != "x" {
		t.Fatal("base override lost when setting a template")
	}
}
```

Ensure the test file imports `os` and `path/filepath` (add to the existing import block if absent).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/promptoverrides/`
Expected: FAIL — `GetTemplate`/`SetTemplate`/`ClearTemplate` undefined.

- [ ] **Step 3: Write minimal implementation**

In `backend/internal/promptoverrides/store.go`:

Add the `Templates` field to `Overrides` (replace the struct):

```go
// Overrides maps a prompt kind to its custom global base and each message
// template name to its custom text. A missing key means the built-in default
// applies.
type Overrides struct {
	Base      map[prompts.Kind]string `json:"base,omitempty"`
	Templates map[string]string       `json:"templates,omitempty"`
}
```

In `NewStore`, initialize `Templates` and tolerate a legacy file (replace the constructor body's map init + load):

```go
	s := &Store{path: filepath.Join(dir, fileName), cur: Overrides{
		Base:      map[prompts.Kind]string{},
		Templates: map[string]string{},
	}}
	if b, err := os.ReadFile(s.path); err == nil {
		var loaded Overrides
		if json.Unmarshal(b, &loaded) == nil {
			if loaded.Base != nil {
				s.cur.Base = loaded.Base
			}
			if loaded.Templates != nil {
				s.cur.Templates = loaded.Templates
			}
		}
	}
	return s, nil
```

Update `Get` to also copy `Templates` (replace the method):

```go
// Get returns a copy of the current overrides; callers cannot mutate the store.
func (s *Store) Get() Overrides {
	s.mu.RLock()
	defer s.mu.RUnlock()
	out := Overrides{
		Base:      make(map[prompts.Kind]string, len(s.cur.Base)),
		Templates: make(map[string]string, len(s.cur.Templates)),
	}
	for k, v := range s.cur.Base {
		out.Base[k] = v
	}
	for k, v := range s.cur.Templates {
		out.Templates[k] = v
	}
	return out
}
```

Update `persistLocked` to marshal templates too (replace the marshal line's argument):

```go
func (s *Store) persistLocked() error {
	b, err := json.Marshal(s.cur)
	if err != nil {
		return fmt.Errorf("promptoverrides: marshal: %w", err)
	}
	tmp := s.path + ".tmp"
	if err := os.WriteFile(tmp, b, 0o600); err != nil {
		return fmt.Errorf("promptoverrides: write: %w", err)
	}
	if err := os.Rename(tmp, s.path); err != nil {
		return fmt.Errorf("promptoverrides: rename: %w", err)
	}
	return nil
}
```

Because `persistLocked` now serializes `s.cur` directly, update `SetBase`/`ClearBase` to mutate `s.cur` then persist (replace both methods so base and template writes share one persistence path):

```go
// SetBase stores a custom global base for a kind.
func (s *Store) SetBase(k prompts.Kind, text string) error {
	if !k.Valid() {
		return fmt.Errorf("promptoverrides: unknown kind %q", k)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev := s.cur.Base[k]
	had := s.cur.Base != nil
	if s.cur.Base == nil {
		s.cur.Base = map[prompts.Kind]string{}
	}
	s.cur.Base[k] = text
	if err := s.persistLocked(); err != nil {
		if had {
			s.cur.Base[k] = prev
		} else {
			s.cur.Base = nil
		}
		return err
	}
	return nil
}

// ClearBase removes a kind's override, restoring the built-in default.
func (s *Store) ClearBase(k prompts.Kind) error {
	if !k.Valid() {
		return fmt.Errorf("promptoverrides: unknown kind %q", k)
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cur.Base[k]
	delete(s.cur.Base, k)
	if err := s.persistLocked(); err != nil {
		if existed {
			s.cur.Base[k] = prev
		}
		return err
	}
	return nil
}
```

Add the template methods (append after `ClearBase`):

```go
// GetTemplate returns the custom override for a message template name and
// whether one exists. Absent ⇒ ("", false) ⇒ caller uses the built-in default.
func (s *Store) GetTemplate(name string) (string, bool) {
	s.mu.RLock()
	defer s.mu.RUnlock()
	v, ok := s.cur.Templates[name]
	return v, ok
}

// SetTemplate stores a custom message-template override.
func (s *Store) SetTemplate(name, text string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cur.Templates[name]
	if s.cur.Templates == nil {
		s.cur.Templates = map[string]string{}
	}
	s.cur.Templates[name] = text
	if err := s.persistLocked(); err != nil {
		if existed {
			s.cur.Templates[name] = prev
		} else {
			delete(s.cur.Templates, name)
		}
		return err
	}
	return nil
}

// ClearTemplate removes a message-template override, restoring the default.
func (s *Store) ClearTemplate(name string) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	prev, existed := s.cur.Templates[name]
	delete(s.cur.Templates, name)
	if err := s.persistLocked(); err != nil {
		if existed {
			s.cur.Templates[name] = prev
		}
		return err
	}
	return nil
}
```

Remove the now-unused `copyBaseLocked` helper (it is superseded by direct `s.cur` mutation). Delete the `func (s *Store) copyBaseLocked() ...` method.

- [ ] **Step 4: Run test to verify it passes**

Run: `cd backend && go test ./internal/promptoverrides/`
Expected: PASS (existing base tests + new template tests).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/promptoverrides/store.go backend/internal/promptoverrides/store_test.go
git commit -m "feat(promptoverrides): persist editable message-template overrides

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 5: Message-templates settings API (code-first)

**Files:**
- Modify: `backend/internal/httpd/controllers/dto.go`
- Modify: `backend/internal/httpd/controllers/settings.go`
- Modify: `backend/internal/httpd/apispec/specgen/build.go`
- Modify: `backend/internal/httpd/api.go`
- Modify: `backend/internal/daemon/daemon.go`
- Test: `backend/internal/httpd/controllers/settings_test.go`
- Regenerate: `backend/internal/httpd/apispec/openapi.yaml`, `frontend/src/api/schema.ts`

**Interfaces:**
- Consumes: `messagetemplates.KnownNames/Default/Placeholders/Name.Valid`, `*promptoverrides.Store` (`Get`, `SetTemplate`, `ClearTemplate`).
- Produces (wire/API):
  - DTOs: `MessageTemplateItem{Name string; Default string; Placeholders []string; Override *string}`, `MessageTemplatesResponse{Templates []MessageTemplateItem}`, `SetMessageTemplateRequest{Template string}`, `MessageTemplateNameParam{Name string}`.
  - Controller iface `MessageTemplatesService{ Get() promptoverrides.Overrides; SetTemplate(name, text string) error; ClearTemplate(name string) error }`.
  - Routes: `GET /api/v1/settings/message-templates`, `PUT /api/v1/settings/message-templates/{name}`, `DELETE /api/v1/settings/message-templates/{name}`.
  - `APIDeps.MessageTemplates controllers.MessageTemplatesService`.

- [ ] **Step 1: Write the failing test**

Append to `backend/internal/httpd/controllers/settings_test.go` (mirror the existing prompts tests — reuse whatever `doRequest`/server-construction helper the file already uses; the snippet below assumes a `newSettingsTestServer(t, ...)` style helper exists like the prompts tests use, and a `fakeMessageTemplates` in-memory fake):

```go
type fakeMessageTemplates struct {
	overrides map[string]string
	setErr    error
}

func (f *fakeMessageTemplates) Get() promptoverrides.Overrides {
	cp := map[string]string{}
	for k, v := range f.overrides {
		cp[k] = v
	}
	return promptoverrides.Overrides{Templates: cp}
}
func (f *fakeMessageTemplates) SetTemplate(name, text string) error {
	if f.setErr != nil {
		return f.setErr
	}
	if f.overrides == nil {
		f.overrides = map[string]string{}
	}
	f.overrides[name] = text
	return nil
}
func (f *fakeMessageTemplates) ClearTemplate(name string) error {
	delete(f.overrides, name)
	return nil
}

func TestMessageTemplatesAPI_GetListsAllWithDefaults(t *testing.T) {
	c := &SettingsController{MessageTemplates: &fakeMessageTemplates{overrides: map[string]string{"ci-failing": "custom"}}}
	r := chi.NewRouter()
	c.Register(r)
	body, status, _ := doRequest(t, r, "GET", "/settings/message-templates", "")
	if status != http.StatusOK {
		t.Fatalf("status %d: %s", status, body)
	}
	if !strings.Contains(body, `"name":"review-comment-dispatch"`) {
		t.Fatalf("missing review-comment-dispatch: %s", body)
	}
	if !strings.Contains(body, `"override":"custom"`) {
		t.Fatalf("ci-failing override not surfaced: %s", body)
	}
}

func TestMessageTemplatesAPI_SetAndClear(t *testing.T) {
	fake := &fakeMessageTemplates{}
	c := &SettingsController{MessageTemplates: fake}
	r := chi.NewRouter()
	c.Register(r)

	_, status, _ := doRequest(t, r, "PUT", "/settings/message-templates/ci-failing", `{"template":"hi"}`)
	if status != http.StatusOK {
		t.Fatalf("PUT status %d", status)
	}
	if fake.overrides["ci-failing"] != "hi" {
		t.Fatalf("override not stored: %v", fake.overrides)
	}

	_, status, _ = doRequest(t, r, "PUT", "/settings/message-templates/bogus", `{"template":"x"}`)
	if status != http.StatusBadRequest {
		t.Fatalf("unknown name should be 400, got %d", status)
	}

	_, status, _ = doRequest(t, r, "DELETE", "/settings/message-templates/ci-failing", "")
	if status != http.StatusOK {
		t.Fatalf("DELETE status %d", status)
	}
	if _, ok := fake.overrides["ci-failing"]; ok {
		t.Fatalf("override not cleared: %v", fake.overrides)
	}
}
```

Match the exact helper names the existing `settings_test.go` uses for building a controller + issuing requests; adapt the two tests to that harness if it differs (e.g. if the file uses a full `httpd` server + `/api/v1` prefix, prepend `/api/v1`).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/httpd/controllers/ -run MessageTemplates`
Expected: FAIL — `SettingsController.MessageTemplates` field and routes do not exist.

- [ ] **Step 3a: Add DTOs**

In `backend/internal/httpd/controllers/dto.go`, after the `SetSystemPromptRequest`/`PromptKindParam` block, add:

```go
// MessageTemplateItem is one editable nudge template on the wire: its built-in
// default, documented placeholders, and current override (null ⇒ default).
type MessageTemplateItem struct {
	Name         string   `json:"name"`
	Default      string   `json:"default"`
	Placeholders []string `json:"placeholders"`
	Override     *string  `json:"override"`
}

// MessageTemplatesResponse is the body of GET /api/v1/settings/message-templates.
type MessageTemplatesResponse struct {
	Templates []MessageTemplateItem `json:"templates"`
}

// SetMessageTemplateRequest is the body of PUT /api/v1/settings/message-templates/{name}.
type SetMessageTemplateRequest struct {
	Template string `json:"template"`
}

// MessageTemplateNameParam is the {name} path parameter for the
// /settings/message-templates/{name} routes.
type MessageTemplateNameParam struct {
	Name string `path:"name" description:"Editable nudge template name." enum:"review-comment-dispatch,ci-failing,merge-conflict,tracker-bot-comment,ao-reviewer-batch,ao-reviewer-single"`
}
```

- [ ] **Step 3b: Add controller iface, field, routes, handlers**

In `backend/internal/httpd/controllers/settings.go`:

Add the import (in the existing import block):

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
```

Add the service interface after `SystemPromptsService`:

```go
// MessageTemplatesService is the template-override store surface the controller
// needs. *promptoverrides.Store satisfies this directly.
type MessageTemplatesService interface {
	Get() promptoverrides.Overrides
	SetTemplate(name, text string) error
	ClearTemplate(name string) error
}
```

Add the field to `SettingsController`:

```go
	MessageTemplates MessageTemplatesService
```

Add routes in `Register` (after the prompts routes):

```go
	r.Get("/settings/message-templates", c.getMessageTemplates)
	r.Put("/settings/message-templates/{name}", c.setMessageTemplate)
	r.Delete("/settings/message-templates/{name}", c.clearMessageTemplate)
```

Add handlers (after `clearPrompt`):

```go
func (c *SettingsController) getMessageTemplates(w http.ResponseWriter, r *http.Request) {
	if c.MessageTemplates == nil {
		apispec.NotImplemented(w, r, "GET", "/api/v1/settings/message-templates")
		return
	}
	ov := c.MessageTemplates.Get()
	items := make([]MessageTemplateItem, 0, len(messagetemplates.KnownNames()))
	for _, n := range messagetemplates.KnownNames() {
		item := MessageTemplateItem{
			Name:         string(n),
			Default:      messagetemplates.Default(n),
			Placeholders: messagetemplates.Placeholders(n),
		}
		if v, ok := ov.Templates[string(n)]; ok {
			v := v
			item.Override = &v
		}
		items = append(items, item)
	}
	envelope.WriteJSON(w, http.StatusOK, MessageTemplatesResponse{Templates: items})
}

func (c *SettingsController) setMessageTemplate(w http.ResponseWriter, r *http.Request) {
	if c.MessageTemplates == nil {
		apispec.NotImplemented(w, r, "PUT", "/api/v1/settings/message-templates/{name}")
		return
	}
	name := messagetemplates.Name(chi.URLParam(r, "name"))
	if !name.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", fmt.Sprintf("unknown template name %q", name), nil)
		return
	}
	var in SetMessageTemplateRequest
	if err := decodeJSON(r, &in); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_JSON", "Invalid JSON body", nil)
		return
	}
	if err := c.MessageTemplates.SetTemplate(string(name), in.Template); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	c.getMessageTemplates(w, r)
}

func (c *SettingsController) clearMessageTemplate(w http.ResponseWriter, r *http.Request) {
	if c.MessageTemplates == nil {
		apispec.NotImplemented(w, r, "DELETE", "/api/v1/settings/message-templates/{name}")
		return
	}
	name := messagetemplates.Name(chi.URLParam(r, "name"))
	if !name.Valid() {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", fmt.Sprintf("unknown template name %q", name), nil)
		return
	}
	if err := c.MessageTemplates.ClearTemplate(string(name)); err != nil {
		envelope.WriteAPIError(w, r, http.StatusBadRequest, "bad_request", "INVALID_SETTINGS", err.Error(), nil)
		return
	}
	c.getMessageTemplates(w, r)
}
```

- [ ] **Step 3c: Register operations in the OpenAPI generator**

In `backend/internal/httpd/apispec/specgen/build.go`, in the operation registry (right after the three `settings/prompts` entries), add:

```go
		{
			method: http.MethodGet, path: "/api/v1/settings/message-templates", id: "getMessageTemplates", tag: "settings",
			summary: "Fetch the editable nudge message templates (default + override per name)",
			resps: []respUnit{
				{http.StatusOK, controllers.MessageTemplatesResponse{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodPut, path: "/api/v1/settings/message-templates/{name}", id: "setMessageTemplate", tag: "settings",
			summary:    "Set the override text for a nudge message template",
			pathParams: []any{controllers.MessageTemplateNameParam{}},
			reqBody:    controllers.SetMessageTemplateRequest{},
			resps: []respUnit{
				{http.StatusOK, controllers.MessageTemplatesResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
		{
			method: http.MethodDelete, path: "/api/v1/settings/message-templates/{name}", id: "clearMessageTemplate", tag: "settings",
			summary:    "Reset a nudge message template to its built-in default",
			pathParams: []any{controllers.MessageTemplateNameParam{}},
			resps: []respUnit{
				{http.StatusOK, controllers.MessageTemplatesResponse{}},
				{http.StatusBadRequest, envelope.APIError{}},
				{http.StatusInternalServerError, envelope.APIError{}},
			},
		},
```

In the `schemaNames` map (near build.go lines 211-218), add:

```go
	"ControllersMessageTemplateItem":      "MessageTemplateItem",
	"ControllersMessageTemplatesResponse": "MessageTemplatesResponse",
	"ControllersSetMessageTemplateRequest": "SetMessageTemplateRequest",
```

- [ ] **Step 3d: Wire the dependency**

In `backend/internal/httpd/api.go`, add the field to `APIDeps` (after `SystemPrompts`):

```go
	MessageTemplates   controllers.MessageTemplatesService
```

In `NewAPI` (the `&controllers.SettingsController{...}` construction, api.go:74), add the field:

```go
		settings:      &controllers.SettingsController{Svc: deps.Settings, SpawnConfirm: deps.SpawnConfirm, SystemPrompts: deps.SystemPrompts, MessageTemplates: deps.MessageTemplates},
```

In `backend/internal/daemon/daemon.go`, in the `httpd.APIDeps{...}` literal (after `SystemPrompts: promptOverrides,`), add:

```go
		MessageTemplates:   promptOverrides,
```

- [ ] **Step 4a: Run the controller test**

Run: `cd backend && go test ./internal/httpd/controllers/ -run MessageTemplates`
Expected: PASS.

- [ ] **Step 4b: Regenerate the spec + frontend types, run the drift guard**

Run (from repo root):
```bash
npm run api
```
Then:
```bash
cd backend && go test ./internal/httpd/...
```
Expected: PASS, including `TestBuild_MatchesEmbedded`. `git status` should show modified `backend/internal/httpd/apispec/openapi.yaml` and `frontend/src/api/schema.ts` containing the new `message-templates` paths + `MessageTemplateItem`/`MessageTemplatesResponse`/`SetMessageTemplateRequest` schemas.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/httpd/controllers/dto.go backend/internal/httpd/controllers/settings.go backend/internal/httpd/controllers/settings_test.go backend/internal/httpd/apispec/specgen/build.go backend/internal/httpd/apispec/openapi.yaml backend/internal/httpd/api.go backend/internal/daemon/daemon.go frontend/src/api/schema.ts
git commit -m "feat(settings): message-templates API (get/set/clear)

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 6: Render simple nudges via the injected `Renderer`

**Files:**
- Modify: `backend/internal/lifecycle/manager.go`
- Modify: `backend/internal/lifecycle/reactions.go`
- Modify: `backend/internal/daemon/lifecycle_wiring.go`
- Test: `backend/internal/lifecycle/reactions_test.go` (or `manager_test.go` — wherever the CI/merge-conflict/review nudge tests live)

**Interfaces:**
- Consumes: `messagetemplates.Renderer`, `messagetemplates.NewRenderer`, `messagetemplates.Name*`, data structs; `promptoverrides.Store.Get`.
- Produces: `Manager.renderer *messagetemplates.Renderer`; `func WithMessageRenderer(*messagetemplates.Renderer) Option`. A nil renderer (tests that don't set it) must still work — reactions fall back to built-in defaults via a package-level default renderer.

- [ ] **Step 1: Write the failing test**

Add to the lifecycle reactions test file. This asserts a custom override changes the CI nudge text an agent receives (proving the render path is wired). Adapt the harness (`newTestManager`, fake messenger capturing sent messages) to whatever the existing tests use:

```go
func TestApplyPRObservation_CIFailingUsesTemplateOverride(t *testing.T) {
	msgr := &captureMessenger{} // existing fake that records Send(id, msg)
	renderer := messagetemplates.NewRenderer(func() map[string]string {
		return map[string]string{string(messagetemplates.NameCIFailing): "CUSTOM CI: {{.LogTail}}"}
	})
	m := newTestManagerWithMessenger(t, msgr, lifecycle.WithMessageRenderer(renderer))
	// ... seed a non-terminal session "s1" as the existing CI-failing test does ...

	obs := ports.PRObservation{
		Fetched: true, URL: "https://x/pr/1", CI: domain.CIFailing,
		Checks: []ports.PRCheckObservation{{Name: "build", Status: domain.PRCheckFailed, LogTail: "boom"}},
	}
	if err := m.ApplyPRObservation(context.Background(), "s1", obs); err != nil {
		t.Fatal(err)
	}
	if got := msgr.lastMessage(); got != "CUSTOM CI: boom" {
		t.Fatalf("CI nudge = %q, want template override applied", got)
	}
}
```

Also update any existing assertion that checks the default CI/merge-conflict/review message so it matches the template default (identical text — the defaults reproduce the old strings, so existing assertions should already pass unchanged; only add the override test above).

- [ ] **Step 2: Run test to verify it fails**

Run: `cd backend && go test ./internal/lifecycle/ -run CIFailingUsesTemplate`
Expected: FAIL — `WithMessageRenderer` undefined / renderer not applied.

- [ ] **Step 3: Implement — Manager field + Option**

In `backend/internal/lifecycle/manager.go`, add the import:

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
```

Add the field to `Manager`:

```go
	renderer *messagetemplates.Renderer
```

Add the Option (after `WithTelemetry`):

```go
// WithMessageRenderer wires lifecycle nudges to render from editable templates.
func WithMessageRenderer(r *messagetemplates.Renderer) Option {
	return func(m *Manager) { m.renderer = r }
}
```

In `New`, after `m := &Manager{...}` and before applying opts, default the renderer so a Manager built without the option (tests) still renders built-in defaults:

```go
	m.renderer = messagetemplates.NewRenderer(nil)
```

(Place this assignment before the `for _, opt := range opts` loop so `WithMessageRenderer` can override it.)

- [ ] **Step 3b: Implement — render nudges in reactions.go**

In `backend/internal/lifecycle/reactions.go`, add the import:

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
```

Add a small helper method on `Manager` (near `sendOnce`) that renders + logs override failures:

```go
// renderNudge renders a nudge template, logging (but tolerating) a failed
// operator override — the Renderer returns the built-in default on failure.
func (m *Manager) renderNudge(name messagetemplates.Name, data any) string {
	msg, err := m.renderer.Render(name, data)
	if err != nil {
		slog.Default().Warn("lifecycle: nudge template render fell back to default", "template", name, "err", err)
	}
	return msg
}
```

Replace the **CI failing** block (currently building `msg` with the literal + LogTail append):

```go
	if o.CI == domain.CIFailing {
		for _, ch := range o.Checks {
			if ch.Status == domain.PRCheckFailed {
				logTail := ""
				if ch.LogTail != "" {
					logTail = domain.SanitizeControlChars(ch.LogTail)
				}
				msg := m.renderNudge(messagetemplates.NameCIFailing, messagetemplates.CIFailingData{LogTail: logTail})
				return m.sendOnce(ctx, id, o.URL, "ci:"+o.URL+":"+ch.Name, ch.CommitHash+":"+ch.LogTail, msg, 0)
			}
		}
	}
```

Replace the **review feedback** block:

```go
	if o.Review == domain.ReviewChangesRequest || hasUnresolvedComments(o.Comments) {
		comments, sig := reviewContent(o.Comments)
		msg := m.renderNudge(messagetemplates.NameReviewCommentDispatch, messagetemplates.ReviewCommentData{Comments: comments})
		if sig == "" {
			sig = string(o.Review)
		}
		return m.sendOnce(ctx, id, o.URL, "review:"+o.URL, sig, msg, reviewMaxNudge)
	}
```

Replace the **merge conflict** send:

```go
		msg := m.renderNudge(messagetemplates.NameMergeConflict, messagetemplates.MergeConflictData{})
		return m.sendOnce(ctx, id, o.URL, "merge-conflict:"+o.URL, string(o.Mergeability), msg, 0)
```

Replace the **tracker-bot** block in `ApplyTrackerFacts`:

```go
	if o.Changed.Comments {
		bodies, ids := newBotCommentContent(o.Comments)
		if len(ids) > 0 {
			msg := m.renderNudge(messagetemplates.NameTrackerBotComment, messagetemplates.TrackerBotData{Comments: strings.Join(bodies, "\n\n")})
			return m.sendOnce(ctx, id, "", "tracker-bot:"+o.Issue.URL, strings.Join(ids, ","), msg, 0)
		}
	}
```

Note: `reviewContent` already returns SanitizeControlChars'd bodies; `newBotCommentContent` returns raw bodies — the old tracker code injected them without sanitizing beyond the join, so behavior is preserved. (If the CI `LogTail` sanitize placement changes the dedup signature, keep the signature on `ch.LogTail` raw as shown, matching the original.)

- [ ] **Step 3c: Wire the renderer in the daemon**

In `backend/internal/daemon/lifecycle_wiring.go`, `startLifecycle` builds `lifecycle.New`. Thread the overrides source through. Change the `startLifecycle` signature to accept the store's overrides getter, and pass `WithMessageRenderer`:

```go
func startLifecycle(ctx context.Context, store *sqlite.Store, runtime ports.Runtime, messenger ports.AgentMessenger, notifier notificationSink, telemetry ports.EventSink, templates func() map[string]string, logger *slog.Logger) *lifecycleStack {
	renderer := messagetemplates.NewRenderer(templates)
	lcm := lifecycle.New(store, messenger, lifecycle.WithNotificationSink(notifier), lifecycle.WithTelemetry(telemetry), lifecycle.WithMessageRenderer(renderer))
	rp := reaper.New(lcm, store, runtime, reaper.Config{Logger: logger})
	return &lifecycleStack{LCM: lcm, reaperDone: rp.Start(ctx)}
}
```

Add the import to `lifecycle_wiring.go`:

```go
	"github.com/aoagents/agent-orchestrator/backend/internal/messagetemplates"
```

In `backend/internal/daemon/daemon.go`, the `startLifecycle(...)` call is currently after `promptOverrides` is constructed (line 141) but the call site (`lcStack := startLifecycle(...)`, ~line 120) is BEFORE line 141. Move the `promptoverrides.NewStore(cfg.DataDir)` construction ABOVE the `startLifecycle` call (it has no dependency on lifecycle), then pass `func() map[string]string { return promptOverrides.Get().Templates }`:

```go
	lcStack := startLifecycle(ctx, store, runtimeAdapter, messenger, notificationWriter, telemetrySink, func() map[string]string { return promptOverrides.Get().Templates }, log)
```

Verify ordering: `promptOverrides` must be declared before this call. If relocating the `NewStore` block, keep its error handling (`stop()`, `lcStack.Stop()`…) but note `lcStack` may not exist yet at the new location — simplify the error path to the pre-lifecycle cleanup available at that point (return the wrapped error after `stop()`), matching the surrounding early-return style.

- [ ] **Step 4: Run tests**

Run:
```bash
cd backend && go test ./internal/lifecycle/ ./internal/daemon/
```
Expected: PASS — the new override test passes; existing default-message tests pass unchanged (defaults reproduce the old text).

- [ ] **Step 5: Commit**

```bash
git add backend/internal/lifecycle/manager.go backend/internal/lifecycle/reactions.go backend/internal/lifecycle/reactions_test.go backend/internal/daemon/lifecycle_wiring.go backend/internal/daemon/daemon.go
git commit -m "refactor(lifecycle): render CI/review/conflict/tracker nudges from templates

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 7: Render AO-internal-reviewer nudges via templates

**Files:**
- Modify: `backend/internal/lifecycle/reactions.go` (`ApplyReviewBatch`, `ApplyReviewResult`)
- Test: `backend/internal/lifecycle/reactions_test.go` (existing AO-reviewer tests)

**Interfaces:**
- Consumes: `messagetemplates.NameAOReviewerBatch/Single`, `AOReviewerBatchData`, `AOReviewItem`, `AOReviewerSingleData`, `m.renderNudge`.

- [ ] **Step 1: Write/adjust the failing test**

Add an override test proving the batch template is used (adapt harness):

```go
func TestApplyReviewBatch_UsesTemplateOverride(t *testing.T) {
	msgr := &captureMessenger{}
	renderer := messagetemplates.NewRenderer(func() map[string]string {
		return map[string]string{string(messagetemplates.NameAOReviewerBatch): "OVERRIDE {{.Count}}"}
	})
	m := newTestManagerWithMessenger(t, msgr, lifecycle.WithMessageRenderer(renderer))
	// ... seed non-terminal session "s1" ...
	_, err := m.ApplyReviewBatch(context.Background(), "s1", "batch1", []lifecycle.ReviewResult{
		{RunID: "r1", PRURL: "https://x/pr/1", Verdict: domain.VerdictChangesRequested},
	})
	if err != nil {
		t.Fatal(err)
	}
	if got := msgr.lastMessage(); got != "OVERRIDE 1" {
		t.Fatalf("batch nudge = %q", got)
	}
}
```

Existing AO-reviewer tests that assert the default message text should still pass because the default template is byte-identical (golden-locked in Task 2). If any assert on exact text, leave them — they validate the default is preserved.

- [ ] **Step 2: Run to verify it fails**

Run: `cd backend && go test ./internal/lifecycle/ -run ReviewBatch_UsesTemplate`
Expected: FAIL — still using `fmt.Fprintf`.

- [ ] **Step 3: Implement — refactor `ApplyReviewBatch`**

Replace the message-building loop (the `var msg strings.Builder` … `sigParts` block) with data-struct construction + one render, keeping the sig loop:

```go
	sort.Slice(results, func(i, j int) bool {
		if results[i].PRURL != results[j].PRURL {
			return results[i].PRURL < results[j].PRURL
		}
		return results[i].RunID < results[j].RunID
	})
	data := messagetemplates.AOReviewerBatchData{Count: len(results)}
	var sigParts []string
	for i, r := range results {
		data.Reviews = append(data.Reviews, messagetemplates.AOReviewItem{
			Index:     i + 1,
			PRURL:     domain.SanitizeControlChars(r.PRURL),
			Verdict:   domain.SanitizeControlChars(string(r.Verdict)),
			TargetSHA: domain.SanitizeControlChars(r.TargetSHA),
			ReviewID:  domain.SanitizeControlChars(r.GithubReviewID),
			Body:      domain.SanitizeControlChars(r.Body),
		})
		sigParts = append(sigParts, strings.Join([]string{r.RunID, r.PRURL, r.TargetSHA, r.GithubReviewID, r.Body}, "\x00"))
	}
	msg := m.renderNudge(messagetemplates.NameAOReviewerBatch, data)
	anchorPR := results[0].PRURL
	key := "review-batch:" + anchorPR + ":" + batchID
	sig := strings.Join(sigParts, "\x01")
	if err := m.sendOnce(ctx, workerID, anchorPR, key, sig, msg, reviewMaxNudge); err != nil {
		return ReviewDeliveryNoop, err
	}
	return ReviewDeliverySent, nil
```

Note: `TargetSHA`/`ReviewID`/`Body` were sanitized inline before; the golden template in Task 2 was written against **unsanitized** sample values. Sanitizing plain ASCII test inputs is a no-op, so the batch golden test (Task 2) still holds. Real control-char inputs are sanitized here exactly as before.

- [ ] **Step 3b: Implement — refactor `ApplyReviewResult`**

Replace the `msg := fmt.Sprintf(...)` block with:

```go
	msg := m.renderNudge(messagetemplates.NameAOReviewerSingle, messagetemplates.AOReviewerSingleData{
		PRURL:    domain.SanitizeControlChars(r.PRURL),
		Verdict:  domain.SanitizeControlChars(string(r.Verdict)),
		ReviewID: domain.SanitizeControlChars(r.GithubReviewID),
		Body:     domain.SanitizeControlChars(r.Body),
	})
	key := "review:" + r.PRURL + ":ao:" + r.RunID
	sig := strings.Join([]string{r.TargetSHA, r.RunID, r.GithubReviewID, r.Body}, "\x00")
	err = m.sendOnce(ctx, workerID, r.PRURL, key, sig, msg, reviewMaxNudge)
```

Remove the now-unused `fmt` import if nothing else in the file uses it (check: `reactions.go` may still use `fmt` elsewhere — only remove if `go build` complains).

- [ ] **Step 4: Run tests**

Run: `cd backend && go test ./internal/lifecycle/`
Expected: PASS.

- [ ] **Step 5: Commit**

```bash
git add backend/internal/lifecycle/reactions.go backend/internal/lifecycle/reactions_test.go
git commit -m "refactor(lifecycle): render AO reviewer nudges from templates

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 8: Frontend — Message templates settings section

**Files:**
- Create: `frontend/src/renderer/components/MessageTemplatesSection.tsx`
- Create: `frontend/src/renderer/components/MessageTemplatesSection.test.tsx`
- Modify: `frontend/src/renderer/components/GlobalSettingsForm.tsx`

**Interfaces:**
- Consumes: generated `apiClient` paths `/api/v1/settings/message-templates` (GET), `/api/v1/settings/message-templates/{name}` (PUT/DELETE) from Task 5's regen.

- [ ] **Step 1: Write the failing test**

Create `frontend/src/renderer/components/MessageTemplatesSection.test.tsx` (mirrors `SystemPromptsSection.test.tsx`):

```tsx
import { QueryClient, QueryClientProvider } from "@tanstack/react-query";
import { render, screen, waitFor } from "@testing-library/react";
import userEvent from "@testing-library/user-event";
import { beforeEach, describe, expect, it, vi } from "vitest";

const { getMock, putMock, deleteMock } = vi.hoisted(() => ({ getMock: vi.fn(), putMock: vi.fn(), deleteMock: vi.fn() }));
vi.mock("../lib/api-client", () => ({
	apiClient: { GET: getMock, PUT: putMock, DELETE: deleteMock },
	apiErrorMessage: (e: unknown, fb = "Request failed") => (e instanceof Error ? e.message : fb),
}));

import { MessageTemplatesSection } from "./MessageTemplatesSection";

function renderSection() {
	const qc = new QueryClient({ defaultOptions: { queries: { retry: false }, mutations: { retry: false } } });
	render(
		<QueryClientProvider client={qc}>
			<MessageTemplatesSection />
		</QueryClientProvider>,
	);
}

beforeEach(() => {
	getMock.mockReset().mockResolvedValue({
		data: {
			templates: [
				{ name: "review-comment-dispatch", default: "DEFAULT RCD", placeholders: ["{{.Comments}}"], override: null },
				{ name: "ci-failing", default: "DEFAULT CI", placeholders: ["{{.LogTail}}"], override: "CUSTOM CI" },
			],
		},
		error: undefined,
	});
	putMock.mockReset().mockResolvedValue({ data: { templates: [] }, error: undefined });
	deleteMock.mockReset().mockResolvedValue({ data: { templates: [] }, error: undefined });
});

describe("MessageTemplatesSection", () => {
	it("prefills override else default and saves an edit", async () => {
		renderSection();
		const ci = (await screen.findByLabelText(/ci-failing/i)) as HTMLTextAreaElement;
		await waitFor(() => expect(ci.value).toBe("CUSTOM CI"));
		await userEvent.clear(ci);
		await userEvent.type(ci, "NEW CI");
		await userEvent.click(screen.getAllByRole("button", { name: /save/i })[1]);
		await waitFor(() =>
			expect(putMock).toHaveBeenCalledWith("/api/v1/settings/message-templates/{name}", {
				params: { path: { name: "ci-failing" } },
				body: { template: "NEW CI" },
			}),
		);
	});

	it("reset is disabled without an override and calls DELETE when present", async () => {
		renderSection();
		const resets = await screen.findAllByRole("button", { name: /reset to default/i });
		expect(resets[0]).toBeDisabled(); // review-comment-dispatch has no override
		expect(resets[1]).toBeEnabled(); // ci-failing has an override
		await userEvent.click(resets[1]);
		await waitFor(() =>
			expect(deleteMock).toHaveBeenCalledWith("/api/v1/settings/message-templates/{name}", {
				params: { path: { name: "ci-failing" } },
			}),
		);
	});
});
```

- [ ] **Step 2: Run test to verify it fails**

Run: `cd frontend && npm run test -- MessageTemplatesSection`
Expected: FAIL — component does not exist.

- [ ] **Step 3: Write the component**

Create `frontend/src/renderer/components/MessageTemplatesSection.tsx` (tabs for indentation):

```tsx
import { useEffect, useRef, useState } from "react";
import { useMutation, useQuery, useQueryClient } from "@tanstack/react-query";
import { Button } from "./ui/button";
import { Card, CardContent, CardHeader, CardTitle } from "./ui/card";
import { Label } from "./ui/label";
import { Textarea } from "./ui/textarea";
import { apiClient, apiErrorMessage } from "../lib/api-client";

type TemplateItem = { name: string; default: string; placeholders: string[]; override: string | null };
const messageTemplatesQueryKey = ["settings", "messageTemplates"] as const;

// MessageTemplatesSection is the Global Settings card for editing the runtime
// nudge messages AO sends into a worker's pane (CI failing, review feedback,
// merge conflict, tracker-bot, AO reviewer). Each shows the effective text
// (override else built-in default) and its documented placeholders. Save (PUT)
// sets a custom override; Reset-to-default (DELETE) restores the built-in.
export function MessageTemplatesSection() {
	const queryClient = useQueryClient();
	const query = useQuery({
		queryKey: messageTemplatesQueryKey,
		queryFn: async () => {
			const { data, error } = await apiClient.GET("/api/v1/settings/message-templates", {});
			if (error) throw new Error(apiErrorMessage(error));
			return (data as { templates: TemplateItem[] }).templates;
		},
	});
	const [drafts, setDrafts] = useState<Record<string, string>>({});
	const serverSnapshot = useRef<Record<string, string>>({});
	useEffect(() => {
		if (!query.data) return;
		setDrafts((prev) => {
			const next = { ...prev };
			for (const t of query.data) {
				const serverValue = t.override ?? t.default;
				const isDirty = prev[t.name] !== undefined && prev[t.name] !== serverSnapshot.current[t.name];
				if (!isDirty) next[t.name] = serverValue;
				serverSnapshot.current[t.name] = serverValue;
			}
			return next;
		});
	}, [query.data]);

	const save = useMutation({
		mutationFn: async ({ name, template }: { name: string; template: string }) => {
			const { error } = await apiClient.PUT("/api/v1/settings/message-templates/{name}", {
				params: { path: { name } },
				body: { template },
			});
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: messageTemplatesQueryKey }),
	});
	const reset = useMutation({
		mutationFn: async (name: string) => {
			const { error } = await apiClient.DELETE("/api/v1/settings/message-templates/{name}", { params: { path: { name } } });
			if (error) throw new Error(apiErrorMessage(error));
		},
		onSuccess: () => queryClient.invalidateQueries({ queryKey: messageTemplatesQueryKey }),
	});

	const items = query.data ?? [];

	return (
		<Card>
			<CardHeader>
				<CardTitle className="text-[13px]">Message templates</CardTitle>
			</CardHeader>
			<CardContent className="flex flex-col gap-5">
				<p className="text-[12px] text-muted-foreground">
					Edit the runtime messages AO sends into a worker's terminal. Dynamic values are inserted via the listed
					placeholders (Go text/template). A bad edit falls back to the built-in default.
				</p>
				{items.map((t) => (
					<div key={t.name} className="flex flex-col gap-1.5">
						<Label htmlFor={`template-${t.name}`} className="text-[12px] text-muted-foreground">
							{t.name}
						</Label>
						{t.placeholders.length > 0 && (
							<span className="text-[11px] text-muted-foreground">
								Placeholders: <code>{t.placeholders.join(" ")}</code>
							</span>
						)}
						<Textarea
							id={`template-${t.name}`}
							className="min-h-28 font-mono text-[12px]"
							value={drafts[t.name] ?? ""}
							onChange={(e) => setDrafts((d) => ({ ...d, [t.name]: e.target.value }))}
						/>
						<div className="flex items-center gap-3">
							<Button
								type="button"
								variant="primary"
								onClick={() => save.mutate({ name: t.name, template: drafts[t.name] ?? "" })}
								disabled={save.isPending}
							>
								{save.isPending ? "Saving…" : "Save changes"}
							</Button>
							<Button
								type="button"
								variant="outline"
								onClick={() => reset.mutate(t.name)}
								disabled={t.override == null || reset.isPending}
							>
								Reset to default
							</Button>
						</div>
					</div>
				))}
				{(save.isError || reset.isError) && (
					<span className="text-[12px] text-error">
						{(save.error ?? reset.error) instanceof Error ? (save.error ?? reset.error)!.message : "Save failed"}
					</span>
				)}
			</CardContent>
		</Card>
	);
}
```

- [ ] **Step 4: Run test to verify it passes**

Run: `cd frontend && npm run test -- MessageTemplatesSection`
Expected: PASS.

- [ ] **Step 5: Add to Global Settings**

In `frontend/src/renderer/components/GlobalSettingsForm.tsx`, add the import alongside the others (alphabetical, after `MigrationSection` / before `NotificationsSection` per existing ordering — match the file's actual order):

```tsx
import { MessageTemplatesSection } from "./MessageTemplatesSection";
```

Render it in the section stack, right after `<SystemPromptsSection />`:

```tsx
					<SystemPromptsSection />
					<MessageTemplatesSection />
```

- [ ] **Step 6: Typecheck + full frontend test**

Run:
```bash
cd frontend && npm run typecheck && npm run test
```
Expected: PASS. Revert any `routeTree.gen.ts` churn: `git checkout -- src/renderer/routeTree.gen.ts` if modified.

- [ ] **Step 7: Commit**

```bash
git add frontend/src/renderer/components/MessageTemplatesSection.tsx frontend/src/renderer/components/MessageTemplatesSection.test.tsx frontend/src/renderer/components/GlobalSettingsForm.tsx
git commit -m "feat(settings): Message templates section in Global Settings

Co-Authored-By: Claude Opus 4.8 <noreply@anthropic.com>"
```

---

## Task 9: Full-suite verification

- [ ] **Step 1: Backend suite + lint**

Run (repo root):
```bash
npm run lint
```
Expected: PASS (all `go test ./...` + golangci-lint).

- [ ] **Step 2: Frontend suite + typecheck**

Run:
```bash
cd frontend && npm run typecheck && npm run test
```
Expected: PASS.

- [ ] **Step 3: Confirm no generated-file churn is uncommitted incorrectly**

Run: `git status`
Expected: clean, or only intended `openapi.yaml` / `schema.ts` already committed in Task 5. Revert `pnpm-lock.yaml` / `routeTree.gen.ts` churn if present.

---

## Self-Review

**Spec coverage (Phase 1 slice):**
- "Make all runtime nudge templates editable" → Tasks 1–8 (registry, renderer, store, API, UI, reactions refactor). ✅ All six templates (`review-comment-dispatch`, `ci-failing`, `merge-conflict`, `tracker-bot-comment`, `ao-reviewer-batch`, `ao-reviewer-single`) covered.
- "Global Settings section mirroring System Prompts" → Task 8. ✅
- "promptoverrides extended with Templates map" → Task 4. ✅
- "Templates rendered where nudges are built; bad edit falls back to default" → Tasks 3, 6, 7 (`renderNudge` + `Renderer` fallback). ✅
- "Sanitization preserved" → Tasks 6, 7 keep `SanitizeControlChars` on all dynamic inputs. ✅
- Behavior change (remove auto human-review nudge), read API, diff-context, write-path, Comments tab, send-to-worker → **Phases 2–4, separate plans** (out of scope here; noted).

**Placeholder scan:** No TBD/TODO. Every code step includes full code. The only "adapt to existing harness" notes (Tasks 5, 6, 7 test harness names like `doRequest`/`captureMessenger`/`newTestManagerWithMessenger`) instruct mirroring existing test helpers — the implementer must read the current `settings_test.go` / lifecycle test files to match exact helper names; the assertions and production code are fully specified.

**Type consistency:** `messagetemplates.Name` + data structs used identically across Tasks 1/3/6/7. `Templates map[string]string` consistent across store (Task 4), controller (`ov.Templates[string(n)]`, Task 5), and renderer source (`promptOverrides.Get().Templates`, Task 6). API field names (`name`, `default`, `placeholders`, `override`, `template`) consistent between DTOs (Task 5) and frontend (Task 8). `WithMessageRenderer` defined in Task 6, used in Tasks 6/7 tests.

**Open risk flagged for the executor:** Task 6's relocation of `promptoverrides.NewStore` above `startLifecycle` in `daemon.go` must preserve the existing error-cleanup ordering; verify `go build ./...` after the move.
