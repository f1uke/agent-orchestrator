package releasetrain

import (
	"math"
	"sort"
)

const (
	forecastWindow  = 10
	forecastMinimum = 3
)

var forecastKeys = []StageKey{StageFreeze, StageScan, StageApprove, StageTag, StagePublish}

// Forecast is the train after the last known one, dated from the trains
// already seen.
type Forecast struct {
	ID       TrainID           `json:"id"`
	Label    string            `json:"label"`
	Freeze   Day               `json:"freeze"`
	Publish  Day               `json:"publish"`
	Versions []ForecastVersion `json:"versions"`
}

type ForecastVersion struct {
	App     AppID `json:"app"`
	Freeze  Day   `json:"freeze"`
	Publish Day   `json:"publish"`
	// Cadence is the median number of days between this app's trains.
	Cadence int `json:"cadence"`
	// Basis is the trains the dates come from, oldest first.
	Basis  []TrainID       `json:"basis"`
	Stages []ForecastStage `json:"stages"`
}

type ForecastStage struct {
	Key  StageKey `json:"key"`
	Date Day      `json:"date"`
}

func forecast(b *Board, cfg Config, snap Snapshot) *Forecast {
	var last *Train
	for i := range b.Trains {
		if b.Trains[i].ID.regular() {
			last = &b.Trains[i]
		}
	}
	if last == nil {
		return nil
	}
	next := TrainID{Number: last.ID.Number + 1, Year: last.ID.Year}
	out := &Forecast{ID: next, Label: next.String(), Versions: []ForecastVersion{}}
	for _, app := range cfg.Apps {
		if last.version(app.ID) == nil {
			continue
		}
		if fv, ok := forecastVersion(b.Trains, app.ID, next); ok {
			out.Versions = append(out.Versions, fv)
		}
	}
	if len(out.Versions) == 0 {
		return nil
	}
	out.Freeze, out.Publish = out.Versions[0].Freeze, out.Versions[0].Publish
	for _, v := range out.Versions[1:] {
		out.Freeze, out.Publish = *minDay(&out.Freeze, &v.Freeze), *maxDay(&out.Publish, &v.Publish)
	}
	if out.Publish.Before(b.Today) || namedElsewhere(next, out.Freeze, b.Mismatches, snap) {
		return nil
	}
	return out
}

type forecastSample struct {
	id     TrainID
	freeze Day
	stages []Stage
}

func forecastVersion(trains []Train, app AppID, next TrainID) (ForecastVersion, bool) {
	var hist []forecastSample
	for i := range trains {
		t := &trains[i]
		v := t.version(app)
		if !t.ID.regular() || t.ID.Year != next.Year || v == nil {
			continue
		}
		if f := freezeOf(t, v); f != nil && dueOf(t, v) != nil {
			hist = append(hist, forecastSample{t.ID, *f, v.Stages})
		}
	}
	if len(hist) > forecastWindow {
		hist = hist[len(hist)-forecastWindow:]
	}
	if len(hist) < forecastMinimum {
		return ForecastVersion{}, false
	}
	var steps []float64
	for i := 1; i < len(hist); i++ {
		gap := hist[i].id.Number - hist[i-1].id.Number
		steps = append(steps, float64(hist[i].freeze.Sub(hist[i-1].freeze))/float64(gap))
	}
	cadence := int(math.Round(median(steps)))
	if cadence <= 0 {
		return ForecastVersion{}, false
	}
	tail := hist[len(hist)-1]
	fv := ForecastVersion{App: app, Cadence: cadence, Basis: []TrainID{}, Stages: []ForecastStage{}}
	fv.Freeze = tail.freeze.Add(cadence * (next.Number - tail.id.Number))
	for _, s := range hist {
		fv.Basis = append(fv.Basis, s.id)
	}
	for _, key := range forecastKeys {
		var offsets []float64
		for _, s := range hist {
			for _, st := range s.stages {
				if st.Key == key && st.Date != nil {
					offsets = append(offsets, float64(st.Date.Sub(s.freeze)))
				}
			}
		}
		if len(offsets) == 0 {
			continue
		}
		date := fv.Freeze.Add(int(math.Round(median(offsets))))
		fv.Stages = append(fv.Stages, ForecastStage{Key: key, Date: date})
		if key == StagePublish {
			fv.Publish = date
		}
	}
	return fv, true
}

func namedElsewhere(id TrainID, freeze Day, mismatches []Mismatch, snap Snapshot) bool {
	for _, m := range mismatches {
		if m.Kind == MismatchNoMilestone && m.Train != nil && *m.Train == id {
			return true
		}
	}
	stub := Train{ID: id, Freeze: &freeze}
	for _, p := range AllItems(snap, KindPages) {
		if p.onTrain(&stub) {
			return true
		}
	}
	return false
}

func median(xs []float64) float64 {
	s := append([]float64(nil), xs...)
	sort.Float64s(s)
	n := len(s)
	if n%2 == 1 {
		return s[n/2]
	}
	return (s[n/2-1] + s[n/2]) / 2
}
