package rules

import (
	"encoding/json"
	"strings"
	"testing"

	"github.com/aoagents/agent-orchestrator/backend/internal/domain"
)

func TestChunks_CutAtHeadingsOutsideFencesAndHashByContent(t *testing.T) {
	doc := "intro line\n\n# Title\n\nbody\n\n```sh\n# not a heading\n```\n\n## Two\n\nmore\n\n### Three stays inside Two\n"
	cs := Chunks(doc, Version)
	if len(cs) != 1 {
		t.Fatalf("a small document is one chunk, got %d", len(cs))
	}
	var headings []string
	for _, s := range cs[0].Sections {
		headings = append(headings, s.Heading)
	}
	if strings.Join(headings, "|") != "|Title|Two" {
		t.Errorf("sections = %q; a fenced # line and an H3 must not open a section", headings)
	}
	if cs[0].Text() != doc {
		t.Error("chunk text must be the document, byte for byte")
	}
	if again := Chunks(doc, Version); again[0].Hash != cs[0].Hash {
		t.Error("same text, same hash")
	}
	if other := Chunks(doc, "v-next"); other[0].Hash == cs[0].Hash {
		t.Error("a new version must invalidate the hash")
	}
}

func TestChunks_PackSectionsAndSplitOversizedOnes(t *testing.T) {
	para := strings.Repeat("word ", 1000) + "\n\n" // ~5 KB
	var b strings.Builder
	for i := 0; i < 8; i++ {
		b.WriteString("## S\n\n" + para)
	}
	b.WriteString("## Huge\n\n" + strings.Repeat(para, 12))
	cs := Chunks(b.String(), Version)
	total := 0
	for _, c := range cs {
		if len(c.Text()) > ChunkLimit {
			t.Errorf("chunk of %d bytes exceeds the limit", len(c.Text()))
		}
		total += len(c.Text())
	}
	if total != b.Len() {
		t.Errorf("chunks hold %d bytes, document %d: nothing may be lost", total, b.Len())
	}
	if len(cs) < 4 {
		t.Errorf("chunks = %d, want the 40 KB of small sections packed into 2 and the 60 KB section split", len(cs))
	}
	if cs[len(cs)-1].Heading() != "Huge" {
		t.Errorf("a split section keeps its heading, got %q", cs[len(cs)-1].Heading())
	}
}

func TestBlocks_OneAtomPerTopLevelItemOrParagraph(t *testing.T) {
	doc := "# Index\n\nWorkers read only the entries their brief names.\n\n## Gotchas\n- [a](a.md) - first entry\n  continued line\n- [b](b.md) - second entry\n---\n1. numbered item stands alone\n"
	got := Blocks(doc)
	var texts []string
	for _, a := range got {
		texts = append(texts, a.Heading+": "+a.Text)
	}
	want := []string{
		"Index: Workers read only the entries their brief names.",
		"Gotchas: - [a](a.md) - first entry\n  continued line",
		"Gotchas: - [b](b.md) - second entry",
		"Gotchas: 1. numbered item stands alone",
	}
	if strings.Join(texts, "\n") != strings.Join(want, "\n") {
		t.Errorf("blocks =\n%s\nwant\n%s", strings.Join(texts, "\n"), strings.Join(want, "\n"))
	}
}

func TestAtoms_KeepOnlyGroundedRules(t *testing.T) {
	c := Chunks("# Simulators\n\nDrive them **only** through `scripts` — never tap by hand.\n\n## Passwords\n\nPasswords are pasted, never typed.\n", Version)[0]
	answer := `{"rules":[
	 {"text":"Drive simulators only through scripts.","quote":"Drive them only through scripts - never tap by hand.","tags":["Simulator","simulator"," maestro "]},
	 {"text":"Paste passwords.","quote":"passwords are  pasted, never typed","tags":[]},
	 {"text":"Use Xcode 99.","quote":"always use Xcode 99","tags":["xcode"]},
	 {"text":"","quote":"Passwords are pasted","tags":[]}]}`
	atoms, rejected, err := Atoms(json.RawMessage(answer), c)
	if err != nil {
		t.Fatal(err)
	}
	if len(atoms) != 2 || len(rejected) != 2 {
		t.Fatalf("atoms = %+v rejected = %+v", atoms, rejected)
	}
	if atoms[0].Heading != "Simulators" || atoms[1].Heading != "Passwords" {
		t.Errorf("headings = %q, %q; each rule takes the heading its quote sits under", atoms[0].Heading, atoms[1].Heading)
	}
	if strings.Join(atoms[0].Tags, ",") != "simulator,maestro" {
		t.Errorf("tags = %q, want lowercased and deduplicated", atoms[0].Tags)
	}
	if rejected[0].Reason != "quote is not in the source" {
		t.Errorf("an invented rule must be refused, got %q", rejected[0].Reason)
	}
	if _, _, err := Atoms(json.RawMessage(`{"other":1}`), c); err == nil {
		t.Error("an answer without a rules list is an error, not an empty corpus")
	}
}

func TestSearch_RanksTheRuleThatSharesRareTerms(t *testing.T) {
	ix := NewIndex([]Doc{
		{ID: "a", Text: "Write commit messages in English."},
		{ID: "b", Text: "Drive the iOS simulator only through Maestro scripts; never ao sim tap by hand."},
		{ID: "c", Text: "Run the scripts in CI."},
	})
	hits := ix.Search("should I tap through the simulator?", 2)
	if len(hits) == 0 || hits[0].ID != "b" {
		t.Fatalf("hits = %+v, want the simulator rule first", hits)
	}
	if len(ix.Search("the and of", 5)) != 0 {
		t.Error("stop words alone match nothing")
	}
	if got := Tokens("Scripts, scripts' class ทดสอบ"); strings.Join(got, " ") != "script script class ทดสอบ" {
		t.Errorf("tokens = %q", got)
	}
}

func TestForbidden_ScopesAndBlocksOnInvalidPatterns(t *testing.T) {
	protected := []domain.LearnProtectedRule{
		{ID: 1, Text: "Drive simulators only through scripts.", Patterns: []string{`\bao sim (tap|type|drag)\b`}},
		{ID: 2, ProjectID: "nter", Text: "No Jira ids.", Patterns: []string{`STAR-\d+`}},
		{ID: 3, Text: "Broken.", Patterns: []string{`(`}},
	}
	hits := Forbidden("First AO SIM TAP the button, then file STAR-12.", "other", protected)
	if len(hits) != 2 || hits[0].RuleID != 1 || hits[0].Match != "AO SIM TAP" || hits[1].RuleID != 3 {
		t.Errorf("hits = %+v; matching is case-insensitive, a project rule stays in its project, and a broken pattern blocks", hits)
	}
	if hits := Forbidden("STAR-12", "nter", protected[1:2]); len(hits) != 1 {
		t.Errorf("a project's own rule applies in it: %+v", hits)
	}
	if _, err := CompilePatterns([]string{`a(`}); err == nil {
		t.Error("an invalid pattern must be refused on save")
	}
	if _, err := CompilePatterns([]string{""}); err == nil {
		t.Error("an empty pattern would match everything")
	}
}

func TestCorpus_GlobalPlusOwnProjectOnceEach(t *testing.T) {
	chunks := map[string]domain.LearnRuleChunk{
		"aaaaaaaaaaaaaaaa": {Atoms: []domain.LearnRuleAtom{{Text: "Never use the em dash."}}},
		"bbbbbbbbbbbbbbbb": {Atoms: []domain.LearnRuleAtom{{Text: "Drive simulators through scripts.", Tags: []string{"maestro"}}}},
		"cccccccccccccccc": {Atoms: []domain.LearnRuleAtom{{Text: "Other project rule."}}},
	}
	sources := []domain.LearnRuleSource{
		{Key: "g", Scope: domain.LearnRuleGlobal, Chunks: []domain.LearnRuleChunkRef{{Hash: "aaaaaaaaaaaaaaaa"}, {Hash: "not-atomized-yet"}}},
		{Key: "p", Scope: domain.LearnRuleProject, ProjectID: "nter", Chunks: []domain.LearnRuleChunkRef{{Hash: "bbbbbbbbbbbbbbbb"}, {Hash: "aaaaaaaaaaaaaaaa"}}},
		{Key: "o", Scope: domain.LearnRuleProject, ProjectID: "other", Chunks: []domain.LearnRuleChunkRef{{Hash: "cccccccccccccccc"}}},
	}
	var ids []string
	for _, r := range Corpus(sources, chunks, "nter") {
		ids = append(ids, r.ID+"@"+r.SourceKey)
	}
	if strings.Join(ids, " ") != "aaaaaaaaaaaa-0@g bbbbbbbbbbbb-0@p" {
		t.Errorf("corpus = %v", ids)
	}
	if n := len(Corpus(sources, chunks, "")); n != 1 {
		t.Errorf("no project means global rules only, got %d", n)
	}
	hits := Search(Corpus(sources, chunks, "nter"), "maestro", 5)
	if len(hits) != 1 || hits[0].ID != "bbbbbbbbbbbb-0" {
		t.Errorf("a tag must be searchable: %+v", hits)
	}
}
