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

func workDaysTo(today, end Day) int {
	if end.Before(today) {
		return -workDaysLeft(end, today)
	}
	return workDaysLeft(today, end)
}

// Action is the one thing a hit's card asks for: open URL, open the rule's Fix, or open the hit's Details sheet.
type Action struct {
	Label   string `json:"label"`
	URL     string `json:"url,omitempty"`
	Fix     bool   `json:"fix,omitempty"`
	Details bool   `json:"details,omitempty"`
}

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

func (t TrainID) short() string { return strings.TrimPrefix(t.String(), "#") }

func before(event string, on *Day) string {
	if on == nil {
		return "before " + event
	}
	return "before " + event + " " + on.label()
}

func (h *Hit) open(links ...Link) *Hit {
	for i, l := range links {
		if l.URL != "" {
			return h.promote(links, i)
		}
	}
	h.Links = links
	return h
}

func (h *Hit) promote(links []Link, i int) *Hit {
	h.Primary = &Action{Label: links[i].Label, URL: links[i].URL}
	h.Links = append(append([]Link{}, links[:i]...), links[i+1:]...)
	return h
}

func (h *Hit) fix(label string, links ...Link) *Hit {
	h.Primary, h.Links = &Action{Label: label, Fix: true}, links
	return h
}

func (h *Hit) dueAt(today Day, on *Day, event DueEvent) *Hit {
	if on != nil {
		h.Due = &Due{On: *on, Event: event, WorkDays: workDaysTo(today, *on)}
	}
	return h
}
