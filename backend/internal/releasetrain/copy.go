package releasetrain

import (
	"fmt"
	"strings"
)

// Copy is what a hit says: What is wrong, then what to Do about it.
type Copy struct {
	What string `json:"what"`
	Do   string `json:"do"`
}

// DueEvent names the date a hit's deadline is.
type DueEvent string

// The dates a hit can count down to.
const (
	DueFreeze    DueEvent = "freeze"
	DuePublish   DueEvent = "publish"
	DueSprintEnd DueEvent = "sprintEnd"
)

// Enum lists the due events for the API spec.
func (DueEvent) Enum() []any { return []any{DueFreeze, DuePublish, DueSprintEnd} }

// Due is the deadline a hit's time chip counts down to.
type Due struct {
	On    Day      `json:"on"`
	Event DueEvent `json:"event"`
	// WorkDays is the working days from today to On; negative once On has passed.
	WorkDays int `json:"workDays"`
	// Missed marks a deadline the subject can no longer meet, even when On is still ahead.
	Missed bool `json:"missed,omitempty"`
}

func dueOn(today, on Day, event DueEvent) *Due {
	return &Due{On: on, Event: event, WorkDays: workDaysTo(today, on)}
}

// workDaysTo counts the working days from today to end, negative when end is past.
func workDaysTo(today, end Day) int {
	if end.Before(today) {
		return -workDaysLeft(end, today)
	}
	return workDaysLeft(today, end)
}

// Action is the one thing a hit's card asks for. A Fix action opens the rule's fix; any other opens URL.
type Action struct {
	Label string `json:"label"`
	URL   string `json:"url,omitempty"`
	Fix   bool   `json:"fix,omitempty"`
}

func linkAction(l Link) *Action { return &Action{Label: l.Label, URL: l.URL} }

func fixAction(label string) *Action { return &Action{Label: label, Fix: true} }

// plural writes n and noun, adding "s" (or "es" after ch, sh, s or x) unless n is 1.
func plural(n int, noun string) string {
	switch {
	case n == 1:
		return fmt.Sprintf("%d %s", n, noun)
	case strings.HasSuffix(noun, "ch"), strings.HasSuffix(noun, "sh"), strings.HasSuffix(noun, "s"), strings.HasSuffix(noun, "x"):
		return fmt.Sprintf("%d %ses", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}

func isAre(n int) string {
	if n == 1 {
		return "is"
	}
	return "are"
}

func hasHave(n int) string {
	if n == 1 {
		return "has"
	}
	return "have"
}

func itThem(n int) string {
	if n == 1 {
		return "it"
	}
	return "them"
}

// short is the train's number as people say it: "22", or "20-1" for an irregular train.
func (t TrainID) short() string { return strings.TrimPrefix(t.String(), "#") }

// before writes "before <event> <date>", or just "before <event>" when the date is unknown.
func before(event string, on *Day) string {
	if on == nil {
		return "before " + event
	}
	return "before " + event + " " + on.label()
}

// open makes the first link the hit's primary action and the rest its secondary links.
func (h *Hit) open(links ...Link) *Hit {
	if len(links) == 0 {
		return h
	}
	return h.promote(links, 0)
}

// promote makes links[i] the hit's primary action and leaves the other links secondary.
func (h *Hit) promote(links []Link, i int) *Hit {
	h.Primary = linkAction(links[i])
	h.Links = append(append([]Link{}, links[:i]...), links[i+1:]...)
	return h
}

// fix makes the rule's fix the hit's primary action; links stay secondary.
func (h *Hit) fix(label string, links ...Link) *Hit {
	h.Primary, h.Links = fixAction(label), links
	return h
}

// dueAt sets the hit's deadline chip; an unknown date leaves it off.
func (h *Hit) dueAt(today Day, on *Day, event DueEvent) *Hit {
	if on != nil {
		h.Due = dueOn(today, *on, event)
	}
	return h
}
