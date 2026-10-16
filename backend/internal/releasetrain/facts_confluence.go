package releasetrain

import (
	"fmt"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"time"
)

// PageFact is one release train page as its title and storage-format body
// read. ParseError is set when the body has no freeze or publish cell, so a
// rule can say "page unreadable" instead of matching nothing.
type PageFact struct {
	ID         string   `json:"id"`
	Title      string   `json:"title"`
	URL        string   `json:"url"`
	Created    Day      `json:"created"`
	Family     FamilyID `json:"family"`
	Number     int      `json:"number"`
	Extra      int      `json:"extra,omitempty"`
	Freeze     *Day     `json:"freeze,omitempty"`
	Publish    *Day     `json:"publish,omitempty"`
	Keys       []string `json:"keys"`
	Tasks      int      `json:"tasks"`
	Ticked     int      `json:"ticked"`
	ParseError string   `json:"parseError,omitempty"`
}

// RawPage is a page as Confluence returns it.
type RawPage struct {
	ID      string
	Title   string
	URL     string
	Created time.Time
	Body    string
}

// Confluence fact kinds. jira.page_cards is the Jira read of the cards a page lists.
var (
	KindPages     = newKind[PageFact]("confluence.pages", "train pages", Snapshotted, 15*time.Minute, global)
	KindPageCards = newKind[IssueFact]("jira.page_cards", "page cards (Jira)", Snapshotted, 5*time.Minute, perEntity)
)

// ItemKey keys a PageFact within its resource.
func (f PageFact) ItemKey() string { return f.ID }

// SpaceScope is the scope of one Confluence space's train pages.
func SpaceScope(space string) Scope { return Scope("space:" + space) }

// PageScope is the scope of the Jira read of one page's cards.
func PageScope(id string) Scope { return Scope("page:" + id) }

// pageWindow is how many days before a train's freeze its page may be created.
const pageWindow = 60

var (
	pageKeyRE   = regexp.MustCompile(`\b([A-Z][A-Z0-9]+-\d+)\b`)
	tagRE       = regexp.MustCompile(`<[^>]+>`)
	timeAttrRE  = regexp.MustCompile(`datetime="(\d{4}-\d{2}-\d{2})"`)
	plainDateRE = regexp.MustCompile(`(\d{4}-\d{2}-\d{2})`)
)

// ParseTrainPage reads a page whose title names a family's train, or false
// when the title names none (another page the title search matched).
func ParseTrainPage(cfg Config, loc *time.Location, p RawPage) (PageFact, bool) {
	fam, number, extra, ok := pageTrain(cfg, p.Title)
	if !ok {
		return PageFact{}, false
	}
	f := PageFact{
		ID: p.ID, Title: p.Title, URL: p.URL, Created: DayOf(p.Created, loc), Family: fam, Number: number, Extra: extra,
		Keys: pageKeys(p.Body), Tasks: strings.Count(p.Body, "<ac:task>"), Ticked: strings.Count(p.Body, "<ac:task-status>complete"),
	}
	var missing []string
	for _, cell := range []struct {
		label string
		into  **Day
	}{{cfg.Confluence.FreezeCell, &f.Freeze}, {cfg.Confluence.PublishCell, &f.Publish}} {
		d, found := tableDate(p.Body, cell.label)
		if !found {
			missing = append(missing, fmt.Sprintf("%q", cell.label))
		}
		*cell.into = d
	}
	if len(missing) > 0 {
		f.ParseError = "no " + strings.Join(missing, " or ") + " cell in the page table"
	}
	return f, true
}

func pageTrain(cfg Config, title string) (FamilyID, int, int, bool) {
	fams := append([]Family(nil), cfg.Families...)
	sort.SliceStable(fams, func(i, j int) bool { return len(fams[i].TrainLabel) > len(fams[j].TrainLabel) })
	for _, fam := range fams {
		re := regexp.MustCompile(`(?i)(?:^|\s)` + regexp.QuoteMeta(fam.TrainLabel) + `\s+#(\d+)(?:-(\d+))?$`)
		m := re.FindStringSubmatch(title)
		if m == nil {
			continue
		}
		number, _ := strconv.Atoi(m[1])
		extra := 0
		if m[2] != "" {
			extra, _ = strconv.Atoi(m[2])
		}
		return fam.ID, number, extra, true
	}
	return "", 0, 0, false
}

func pageKeys(body string) []string {
	seen := map[string]bool{}
	keys := []string{}
	for _, k := range pageKeyRE.FindAllString(tagRE.ReplaceAllString(body, " "), -1) {
		if !seen[k] {
			seen[k] = true
			keys = append(keys, k)
		}
	}
	sort.Strings(keys)
	return keys
}

// pySpace is what Python's \s matches in a str pattern, which the reference parser uses.
const pySpace = `[\s\x{0b}\x{1c}-\x{1f}\x{85}\p{Z}]`

// tableDate finds the cell after the one labelled label and reads its date:
// a <time datetime> first, else a plain YYYY-MM-DD in the cell's text. found
// is false when no cell carries the label; a labelled cell with no date gives nil.
func tableDate(body, label string) (*Day, bool) {
	re := regexp.MustCompile(`(?s)` + regexp.QuoteMeta(label) + `.*?</t[hd]>` + pySpace + `*<td[^>]*>(.*?)</td>`)
	m := re.FindStringSubmatch(body)
	if m == nil {
		return nil, false
	}
	d := timeAttrRE.FindStringSubmatch(m[1])
	if d == nil {
		d = plainDateRE.FindStringSubmatch(tagRE.ReplaceAllString(m[1], ""))
	}
	if d == nil {
		return nil, true
	}
	day, err := ParseDay(d[1])
	if err != nil {
		return nil, true
	}
	return &day, true
}

func (p PageFact) onTrain(t *Train) bool {
	return t.ID.Number == p.Number && t.ID.Extra == p.Extra && t.Freeze != nil &&
		t.Freeze.Sub(p.Created) >= 0 && t.Freeze.Sub(p.Created) <= pageWindow
}

// attachPages gives each train the newest page per family that names it and
// was created 0 to 60 days before its freeze.
func attachPages(b *Board, cfg Config, snap Snapshot) {
	order := map[FamilyID]int{}
	for i, f := range cfg.Families {
		order[f.ID] = i
	}
	pages := AllItems(snap, KindPages)
	for i := range b.Trains {
		t := &b.Trains[i]
		byFamily := map[FamilyID]PageFact{}
		for _, p := range pages {
			if !p.onTrain(t) {
				continue
			}
			if cur, ok := byFamily[p.Family]; !ok || cur.Created.Before(p.Created) || (cur.Created == p.Created && cur.ID < p.ID) {
				byFamily[p.Family] = p
			}
		}
		t.Pages = make([]PageFact, 0, len(byFamily))
		for _, p := range byFamily {
			t.Pages = append(t.Pages, p)
		}
		sort.Slice(t.Pages, func(i, j int) bool { return order[t.Pages[i].Family] < order[t.Pages[j].Family] })
		for _, v := range t.Versions {
			if p, ok := byFamily[cfg.familyOf(v.App).ID]; ok {
				v.page = &p
			}
		}
	}
}
