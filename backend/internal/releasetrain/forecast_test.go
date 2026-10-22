package releasetrain

import (
	"strings"
	"testing"
)

func forecastStages(fv ForecastVersion) map[StageKey]string {
	out := map[StageKey]string{}
	for _, s := range fv.Stages {
		out[s.Key] = s.Date.String()
	}
	return out
}

func forecastFor(b Board, app AppID) (ForecastVersion, bool) {
	if b.Forecast == nil {
		return ForecastVersion{}, false
	}
	for _, v := range b.Forecast.Versions {
		if v.App == app {
			return v, true
		}
	}
	return ForecastVersion{}, false
}

func TestForecastDatesTheNextTrainFromTheMedianCadenceAndStageOffsets(t *testing.T) {
	f := newFixture(t, "2026-10-08")
	f.published("di", "17", "3.1.0", "2026-09-01", "2026-09-08")
	f.published("di", "18", "3.2.0", "2026-09-15", "2026-09-22")
	f.published("di", "19", "3.3.0", "2026-10-01", "2026-10-08")
	f.version("di", "20", "3.4.0", "2026-10-13", "2026-10-20")

	b := f.reconcile()

	fv, ok := forecastFor(b, "di")
	if !ok {
		t.Fatalf("forecast = %+v, want a di version", b.Forecast)
	}
	if got := b.Forecast.Label; got != "#21" {
		t.Fatalf("forecast label = %q, want #21", got)
	}
	if fv.Cadence != 14 {
		t.Fatalf("cadence = %d, want 14 (median of 14, 16, 12)", fv.Cadence)
	}
	want := map[StageKey]string{
		StageFreeze:  "2026-10-27",
		StageScan:    "2026-10-28",
		StageApprove: "2026-10-29",
		StageTag:     "2026-11-02",
		StagePublish: "2026-11-03",
	}
	got := forecastStages(fv)
	for k, d := range want {
		if got[k] != d {
			t.Fatalf("stage %s = %q, want %q (all: %v)", k, got[k], d, got)
		}
	}
	if fv.Freeze.String() != "2026-10-27" || fv.Publish.String() != "2026-11-03" {
		t.Fatalf("window = %s → %s, want 2026-10-27 → 2026-11-03", fv.Freeze, fv.Publish)
	}
	for _, tr := range b.Trains {
		if tr.ID.Number == 21 {
			t.Fatalf("forecast train #21 is in Board.Trains, where rules would read it")
		}
	}
	for _, h := range b.Alerts.Hits {
		if strings.Contains(string(h.ID), "21-2026") {
			t.Fatalf("hit %s names the predicted train", h.ID)
		}
	}
}

func TestForecastNumbersEachAppOnTheLastTrainAndSkipsExtraTrains(t *testing.T) {
	f := newFixture(t, "2026-10-08")
	for _, app := range []AppID{"di", "pi"} {
		f.published(app, "18", "3.2.0", "2026-09-15", "2026-09-22")
		f.published(app, "19", "3.3.0", "2026-09-29", "2026-10-06")
		f.version(app, "20", "3.4.0", "2026-10-13", "2026-10-20")
	}
	f.version("pi", "20-1", "3.4.1", "2026-10-15", "2026-10-17")
	f.published("da", "17", "1.0.0", "2026-09-01", "2026-09-08")
	f.published("da", "18", "1.1.0", "2026-09-15", "2026-09-22")
	f.published("da", "19", "1.2.0", "2026-09-29", "2026-10-06")

	b := f.reconcile()

	if b.Forecast == nil || b.Forecast.Label != "#21" {
		t.Fatalf("forecast = %+v, want #21", b.Forecast)
	}
	var apps []AppID
	for _, v := range b.Forecast.Versions {
		apps = append(apps, v.App)
	}
	if strings.Join(appStrings(apps), ",") != "di,pi" {
		t.Fatalf("forecast apps = %v, want di,pi (da did not ride #20)", apps)
	}
}

func appStrings(apps []AppID) []string {
	out := make([]string, len(apps))
	for i, a := range apps {
		out[i] = string(a)
	}
	return out
}

func TestForecastMovesOnOrStepsAsideWhenRealDataNamesTheTrain(t *testing.T) {
	history := func(f *fixture) {
		f.published("di", "18", "3.2.0", "2026-09-15", "2026-09-22")
		f.published("di", "19", "3.3.0", "2026-09-29", "2026-10-06")
		f.version("di", "20", "3.4.0", "2026-10-13", "2026-10-20")
	}
	t.Run("milestone", func(t *testing.T) {
		f := newFixture(t, "2026-10-08")
		history(f)
		f.version("di", "21", "3.5.0", "2026-10-27", "2026-11-03")
		b := f.reconcile()
		if b.Forecast == nil || b.Forecast.Label != "#22" {
			t.Fatalf("forecast = %+v, want #22 once #21 has a milestone", b.Forecast)
		}
	})
	t.Run("jira release", func(t *testing.T) {
		f := newFixture(t, "2026-10-08")
		history(f)
		f.jiraVersion("di", "3.5.0", f.label("di", "21"), "2026-11-03")
		if b := f.reconcile(); b.Forecast != nil {
			t.Fatalf("forecast = %+v, want none while a Jira release names #21", b.Forecast)
		}
	})
	t.Run("confluence page", func(t *testing.T) {
		f := newFixture(t, "2026-10-08")
		history(f)
		f.page("demo", "21", "2026-10-05", "2026-10-27", "2026-11-03")
		if b := f.reconcile(); b.Forecast != nil {
			t.Fatalf("forecast = %+v, want none while a train page names #21", b.Forecast)
		}
	})
}

func TestForecastNeedsHistoryAndAFutureWindow(t *testing.T) {
	t.Run("two trains", func(t *testing.T) {
		f := newFixture(t, "2026-10-08")
		f.published("di", "19", "3.3.0", "2026-09-29", "2026-10-06")
		f.version("di", "20", "3.4.0", "2026-10-13", "2026-10-20")
		if b := f.reconcile(); b.Forecast != nil {
			t.Fatalf("forecast = %+v, want none from two trains", b.Forecast)
		}
	})
	t.Run("no trains", func(t *testing.T) {
		if b := newFixture(t, "2026-10-08").reconcile(); b.Forecast != nil {
			t.Fatalf("forecast = %+v, want none", b.Forecast)
		}
	})
	t.Run("predicted publish already past", func(t *testing.T) {
		f := newFixture(t, "2026-12-01")
		f.published("di", "17", "3.1.0", "2026-09-01", "2026-09-08")
		f.published("di", "18", "3.2.0", "2026-09-15", "2026-09-22")
		f.published("di", "19", "3.3.0", "2026-09-29", "2026-10-06")
		if b := f.reconcile(); b.Forecast != nil {
			t.Fatalf("forecast = %+v, want none: #20 would have published on 2026-10-20", b.Forecast)
		}
	})
}
