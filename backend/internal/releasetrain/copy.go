package releasetrain

import "fmt"

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

// plural writes n and noun, adding "s" unless n is 1.
func plural(n int, noun string) string {
	if n == 1 {
		return fmt.Sprintf("%d %s", n, noun)
	}
	return fmt.Sprintf("%d %ss", n, noun)
}
