package narrative

// Golden tests: each synthetic fixture is a full 48 h horizon fed through
// Compose with the shipped DefaultBank, asserting the EXACT composed output.
//
// Variant selection is YearDay % len(variants), so each fixture's date pins
// which phrasing renders. The fixture dates are deliberately spread across
// residues 0–4 of the five-variant keys, and TestVariantRotation walks a
// single situation across consecutive days, so the goldens exercise the bank
// rather than one corner of it. 28 July — the date on the approved v3 mockup
// — lands on residue 4, where the approved copy sits.

import (
	"testing"
	"time"

	"github.com/kuitang/whentorun/internal/domain"
)

// muggyStormFixture reproduces the approved v3 mockup's situation: Tuesday
// 3 PM in a July heat advisory, storms closing the evening, the front
// clearing overnight, and a clean before-work window Wednesday.
func muggyStormFixture(loc *time.Location) Input {
	start := time.Date(2026, 7, 28, 15, 0, 0, 0, loc) // Tue 3 PM

	wbgtTue := map[int]float64{15: 88, 16: 89, 17: 87, 18: 85, 19: 82, 20: 79, 21: 78, 22: 77, 23: 76}
	wbgtWed := map[int]float64{
		0: 75, 1: 75, 2: 74, 3: 74, 4: 74,
		5: 74, 6: 74, 7: 75, 8: 76, 9: 80.5,
		10: 84, 11: 88, 12: 89, 13: 88, 14: 87, 15: 87,
		16: 82, 17: 81, 18: 80, 19: 80, 20: 79, 21: 78, 22: 77, 23: 76,
	}

	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		hr := lt.Hour()
		var wbgt, dew float64
		switch lt.Day() {
		case 28: // Tuesday: hot, oppressive, storms at 7–8 PM
			wbgt, dew = wbgtTue[hr], 74
			switch hr {
			case 19:
				h.ThunderProb = metric(60)
			case 20:
				h.ThunderProb = metric(45)
			}
		case 29: // Wednesday: drier behind the front, heat rebuilding midday
			wbgt = wbgtWed[hr]
			switch {
			case hr < 2:
				dew = 70
			case hr < 9:
				dew = 69
			case hr < 16:
				dew = 70
			case hr < 20:
				dew = 69
			default:
				dew = 68
			}
		default: // Thursday tail of the horizon
			wbgt, dew = 75, 69
			if hr >= 9 {
				wbgt = 80
			}
		}
		h.WBGTF = metric(wbgt)
		h.DewPointF = metric(dew)
		h.TempF = metric(wbgt + 4)
		h.ApparentF = metric(wbgt + 8) // firmly warm season
		h.AQI = metric(55)
	})

	return Input{
		Hours: hours,
		Alerts: []domain.Alert{{
			ID:    "heat-adv",
			Event: "Heat Advisory",
			Onset: time.Date(2026, 7, 28, 11, 0, 0, 0, loc),
			Ends:  time.Date(2026, 7, 28, 20, 0, 0, 0, loc),
		}},
		Now: start,
		Loc: loc,
	}
}

func TestGoldenMuggyStormDay(t *testing.T) {
	loc := nyT(t)
	got := Compose(muggyStormFixture(loc), DefaultBank)

	// The approved v3 mockup's narrative shape: advisory+storms → skip
	// today → (front clears) → before work tomorrow.
	want := "Storms close this evening and the Heat Advisory holds until 8 PM — skip today. " +
		"The front clears overnight. Run before work tomorrow, 5–8 AM."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}

	wantBreaks := []Break{
		{After: time.Date(2026, 7, 28, 18, 0, 0, 0, loc), Text: "thunder moves in around 7 PM"},
		{After: time.Date(2026, 7, 28, 20, 0, 0, 0, loc), Text: "storms pass by 9 PM"},
		{After: time.Date(2026, 7, 29, 8, 0, 0, 0, loc), Text: "heat rebuilds toward midday"},
		{After: time.Date(2026, 7, 29, 15, 0, 0, 0, loc), Text: "clearing and drier after the front"},
	}
	assertBreaks(t, got.TableBreaks, wantBreaks)
}

func TestGoldenCleanFallMorning(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 10, 6, 4, 0, 0, 0, loc)
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		app := 44.0
		if hr := lt.Hour(); hr >= 8 && hr < 20 {
			app = 55 // daytime mean 55°F → cold-season comparator
		}
		h.TempF = metric(48)
		h.ApparentF = metric(app)
		h.DewPointF = metric(48)
		h.AQI = metric(35)
	})
	in := Input{Hours: hours, Now: start.Add(30 * time.Minute), Loc: loc}

	// A window that clears cleanWindow takes the -best phrasing, which
	// already carries the "as good as it gets" beat, so no texture clause
	// repeats it.
	got := Compose(in, DefaultBank)
	want := "The best of the next 48 hours is before work today, 5–10 AM."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
	assertBreaks(t, got.TableBreaks, nil)
}

func TestGoldenSmokeEvent(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 7, 7, 5, 0, 0, 0, loc)
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		wbgt := 72.0
		if hr := lt.Hour(); hr >= 9 && hr < 18 {
			wbgt = 78
		}
		h.WBGTF = metric(wbgt)
		h.TempF = metric(76)
		h.ApparentF = metric(78)
		h.DewPointF = metric(62)
		h.AQI = metric(168) // wildfire smoke: Unhealthy, but below the 201 veto
		h.AQIPollutant = "PM2.5"
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	// PM2.5 at 168 is smoke, and the copy is allowed to say so.
	got := Compose(in, DefaultBank)
	want := "AQI 168 on wildfire smoke, Unhealthy. Skip the outdoor miles today. " +
		"Run before work today, 5–10 AM. Everything later reads worse."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
	assertBreaks(t, got.TableBreaks, nil)
}

func TestGoldenIceEvent(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 1, 17, 6, 0, 0, 0, loc)
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.WxFreezingRain = true // vetoes every candidate window
		h.TempF = metric(30)
		h.ApparentF = metric(22)
		h.WindChillF = metric(22)
		h.AQI = metric(40)
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	got := Compose(in, DefaultBank)
	want := "Ice on the pavement today. Treadmill, bike or rest day, but not the roads. " +
		"The next 48 hours hold no window worth taking outdoors."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
	assertBreaks(t, got.TableBreaks, nil)
}

func TestGoldenDSTFallBackDay(t *testing.T) {
	loc := nyT(t)
	// Sat Oct 31 2026, 3 PM EDT; the horizon crosses the fall-back
	// transition (Sun Nov 1 has 25 local hours, with a repeated 1 AM).
	start := time.Date(2026, 10, 31, 15, 0, 0, 0, loc)
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.TempF = metric(45)
		h.ApparentF = metric(45)
		h.DewPointF = metric(40)
		h.AQI = metric(30)
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	got := Compose(in, DefaultBank)
	want := "The best window is after work today, 4–11 PM."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
	assertBreaks(t, got.TableBreaks, nil)
}

// ---- Scenario fixtures for the rest of the bank ----

// TestGoldenLightningNow: the line is overhead right now. The verdict must
// be unambiguous and today must not be offered.
func TestGoldenLightningNow(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 8, 13, 14, 0, 0, 0, loc) // yearDay 225
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.WBGTF = metric(78)
		h.TempF = metric(84)
		h.ApparentF = metric(86)
		h.DewPointF = metric(66)
		h.AQI = metric(44)
		h.PoP = metric(10)
		if lt.Day() == 13 && lt.Hour() >= 16 && lt.Hour() <= 18 {
			h.WxThunder = true
			h.PoP = metric(80)
		}
	})
	in := Input{Hours: hours, Now: start.Add(3 * time.Hour), Loc: loc} // 5 PM, mid-line

	got := Compose(in, DefaultBank)
	want := "Thunderstorms are overhead now and should clear by 7 PM. Stay in until they do. " +
		"Before work tomorrow, 5–10 AM, is the window to take."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
	wantBreaks := []Break{
		{After: time.Date(2026, 8, 13, 15, 0, 0, 0, loc), Text: "storms arrive by 4 PM"},
		{After: time.Date(2026, 8, 13, 18, 0, 0, 0, loc), Text: "the line clears around 7 PM"},
	}
	assertBreaks(t, got.TableBreaks, wantBreaks)
}

// TestGoldenStormsCleared: the all-clear. The copy reports the line moving
// through without promising a window the ranking did not find.
func TestGoldenStormsCleared(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 8, 14, 14, 0, 0, 0, loc) // yearDay 226
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.WBGTF = metric(76)
		h.TempF = metric(82)
		h.ApparentF = metric(84)
		h.DewPointF = metric(64)
		h.AQI = metric(38)
		if lt.Day() == 14 && lt.Hour() >= 16 && lt.Hour() <= 18 {
			h.WxThunder = true
		}
	})
	in := Input{Hours: hours, Now: start.Add(6 * time.Hour), Loc: loc} // 8 PM, an hour after

	// The evening window has already elapsed, so the ranking looks to
	// tomorrow on its own; the all-clear says nothing about a window.
	got := Compose(in, DefaultBank)
	want := "Lightning cleared the area by 7 PM; the sky is done with it. " +
		"Go early tomorrow — 5–10 AM, before work."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
}

// TestGoldenDeepFreeze: wind chill past the veto. Skip, and say so.
func TestGoldenDeepFreeze(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 1, 6, 6, 0, 0, 0, loc) // yearDay 6
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.TempF = metric(2)
		h.ApparentF = metric(-18)
		h.WindChillF = metric(-18)
		h.WindMPH = metric(22)
		h.GustMPH = metric(34)
		h.WindDirDeg = metric(315)
		h.AQI = metric(30)
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	got := Compose(in, DefaultBank)
	want := "Wind chill down to -18°F. That is frostbite territory, so today belongs indoors. " +
		"There is no safe window in the next two days. Treadmill, or wait it out."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
	assertBreaks(t, got.TableBreaks, nil)
}

// TestGoldenColdSnap: a cold-but-runnable day — the wind-chill band reads as
// dressing advice, not a hazard, and the wind gets its own beat.
func TestGoldenColdSnap(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 2, 2, 5, 0, 0, 0, loc) // yearDay 33
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.TempF = metric(31)
		h.ApparentF = metric(21)
		h.WindChillF = metric(21)
		h.WindMPH = metric(19)
		h.GustMPH = metric(29)
		h.WindDirDeg = metric(0) // due north
		h.DewPointF = metric(18)
		h.AQI = metric(28)
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	// Dry, clean and cold enough that the morning takes the -best phrasing;
	// the wind still earns the closing beat.
	got := Compose(in, DefaultBank)
	want := "Wind chill near 21°F, which is honest running weather with a hat on. " +
		"Run before work today, 5–10 AM. Cool air, clean air, no reason to wait. " +
		"Wind N 19 mph, gusts to 29. Effort, not pace, on the outbound half."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
}

// TestGoldenRainyWindow: rain does not cancel a run, and the ledger marks
// where the roads go wet.
func TestGoldenRainyWindow(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 5, 7, 4, 0, 0, 0, loc) // yearDay 127
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.WBGTF = metric(66)
		h.TempF = metric(58)
		h.ApparentF = metric(57)
		h.DewPointF = metric(52)
		h.AQI = metric(32)
		// Every candidate window on both days is wet, so the ranking cannot
		// dodge the rain and the copy has to deal with it.
		h.PoP = metric(15)
		if hr := lt.Hour(); hr >= 5 && hr < 21 {
			h.PoP = metric(70)
		}
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	// Clean air, but wet: a rainy window never takes the -best phrasing.
	got := Compose(in, DefaultBank)
	want := "Best of the board: before work today, 5–10 AM. " +
		"Expect rain, 70% at the best hours. Wet is fine; just skip the new shoes."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
	wantBreaks := []Break{
		{After: time.Date(2026, 5, 7, 4, 0, 0, 0, loc), Text: "rain starts around 5 AM"},
		{After: time.Date(2026, 5, 8, 4, 0, 0, 0, loc), Text: "rain starts around 5 AM"},
	}
	assertBreaks(t, got.TableBreaks, wantBreaks)
}

// TestGoldenBeforeWorkOut: dawn thunder takes the morning; the evening still
// works, and the copy names the loss before naming the window.
func TestGoldenBeforeWorkOut(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 6, 12, 4, 0, 0, 0, loc) // yearDay 163
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		wbgt := 74.0
		if hr := lt.Hour(); hr >= 10 && hr < 16 {
			wbgt = 84 // midday ranks a band worse, so the evening wins
		}
		h.WBGTF = metric(wbgt)
		h.TempF = metric(72)
		h.ApparentF = metric(74)
		h.DewPointF = metric(58)
		h.AQI = metric(40)
		if lt.Day() == 12 && lt.Hour() >= 5 && lt.Hour() <= 8 {
			h.WxThunder = true
		}
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	got := Compose(in, DefaultBank)
	want := "Before work today does not work. Run after work today, 4–11 PM. As good as the next two days get."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
}

// TestGoldenHeatWarning: warning-class heat outranks the dew-point note, and
// the phrasing stays harder than an advisory's.
func TestGoldenHeatWarning(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 7, 22, 6, 0, 0, 0, loc) // yearDay 203
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		wbgt := 82.0
		if hr := lt.Hour(); hr >= 10 && hr < 18 {
			wbgt = 89
		}
		if lt.Day() == 23 { // the warning breaks overnight
			wbgt -= 8
		}
		h.WBGTF = metric(wbgt)
		h.TempF = metric(96)
		h.ApparentF = metric(104)
		h.DewPointF = metric(73)
		h.AQI = metric(48)
	})
	in := Input{
		Hours: hours,
		Alerts: []domain.Alert{{
			ID: "heat-warn", Event: "Excessive Heat Warning",
			Onset: time.Date(2026, 7, 22, 5, 0, 0, 0, loc),
			Ends:  time.Date(2026, 7, 22, 20, 0, 0, 0, loc),
		}},
		Now: start,
		Loc: loc,
	}

	// A warning-class alert strikes out every hour it covers, so today has
	// nothing left and the window clause has to reach into tomorrow.
	got := Compose(in, DefaultBank)
	want := "The Excessive Heat Warning lasts to 8 PM. " +
		"Treat any hard effort as off the table until it lets go. " +
		"Today has nothing worth running. Wait for tomorrow before work, 5–9 AM."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
}

// TestGoldenNextViableDay: today's air rules every window out on its own —
// no hazard clause wrote the day off — so the window clause carries the news.
func TestGoldenNextViableDay(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 9, 3, 5, 0, 0, 0, loc) // yearDay 246
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.WBGTF = metric(72)
		h.TempF = metric(74)
		h.ApparentF = metric(76)
		h.DewPointF = metric(58)
		h.AQI = metric(45)
		h.AQIPollutant = "PM2.5"
		if lt.Day() == 3 {
			h.AQI = metric(215) // past the 201 veto: every window today is out
		}
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	got := Compose(in, DefaultBank)
	want := "Smoke aloft: AQI 215, Very Unhealthy. Running pulls it deep, so take today inside. " +
		"Nothing today holds up. The next real window is tomorrow before work, 5–10 AM. " +
		"Nothing to work around: cool air, clean air, no heat."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
}

// TestGoldenModerateAirAndOppressiveDew: no hazard, but a soupy dew point
// outranks the moderate-air footnote.
func TestGoldenOppressiveDew(t *testing.T) {
	loc := nyT(t)
	start := time.Date(2026, 8, 20, 5, 0, 0, 0, loc) // yearDay 232
	hours := mkHours(start, 48, func(lt time.Time, h *domain.Hour) {
		h.WBGTF = metric(79)
		h.TempF = metric(86)
		h.ApparentF = metric(94)
		h.DewPointF = metric(75)
		h.AQI = metric(72)
	})
	in := Input{Hours: hours, Now: start, Loc: loc}

	// Moderate air (AQI 72) is real but outranked: the dew point is what
	// will actually change the run.
	got := Compose(in, DefaultBank)
	want := "The dew point is 75°F — soupy enough that effort, not pace, is the honest measure. " +
		"Best of the board: before work today, 5–10 AM."
	if got.AboveFold != want {
		t.Errorf("AboveFold:\n got: %q\nwant: %q", got.AboveFold, want)
	}
}

// TestVariantRotation: the same situation on consecutive days walks the
// whole rotation in order and returns to the top — deterministic, never
// random, and every variant is reachable.
func TestVariantRotation(t *testing.T) {
	loc := nyT(t)
	var got []string
	for i := 0; i < len(DefaultBank[SitIcing])+1; i++ {
		day := time.Date(2026, 1, 15, 6, 0, 0, 0, loc).AddDate(0, 0, i)
		hours := mkHours(day, 48, func(lt time.Time, h *domain.Hour) {
			h.WxFreezingRain = true
			h.TempF = metric(30)
			h.ApparentF = metric(24)
		})
		out := Compose(Input{Hours: hours, Now: day, Loc: loc}, DefaultBank)
		got = append(got, out.AboveFold)
	}

	// 15 January is yearDay 15; the icing key has five variants, so the run
	// starts at index 0 and wraps back to it on the sixth day.
	want := []string{
		"Ice is in the forecast, and there is no safe line through it — take today indoors.",
		"Freezing rain glazes everything it touches. Skip the run; the footing is not worth a fall.",
		"Ice on the pavement today. Treadmill, bike or rest day, but not the roads.",
		"Freezing rain is coming. Skip it — no session is worth going down on a glazed sidewalk.",
		"Freezing rain and ice in the forecast — footing is treacherous; skip it.",
		"Ice is in the forecast, and there is no safe line through it — take today indoors.",
	}
	for i := range want {
		lede := want[i] + " " + string(DefaultBank[SitNoWindow][(15+i)%len(DefaultBank[SitNoWindow])])
		if got[i] != lede {
			t.Errorf("day %d:\n got: %q\nwant: %q", i, got[i], lede)
		}
	}
}

func TestComposeEmptyInput(t *testing.T) {
	got := Compose(Input{Now: time.Date(2026, 7, 28, 15, 0, 0, 0, time.UTC)}, DefaultBank)
	if got.AboveFold != "" || len(got.TableBreaks) != 0 {
		t.Errorf("empty input: got %+v, want empty output", got)
	}
}

// TestComposeSparseBank: a bank missing keys degrades to shorter copy, never
// panics — the Opus authoring pass can land keys incrementally.
func TestComposeSparseBank(t *testing.T) {
	loc := nyT(t)
	sparse := Bank{SitRunBeforeWorkGood: {"Run before work {day}."}}
	got := Compose(muggyStormFixture(loc), sparse)
	if want := "Run before work tomorrow."; got.AboveFold != want {
		t.Errorf("AboveFold: got %q, want %q", got.AboveFold, want)
	}
	if len(got.TableBreaks) != 0 {
		t.Errorf("breaks with no break templates: got %v, want none", got.TableBreaks)
	}
}

func assertBreaks(t *testing.T, got, want []Break) {
	t.Helper()
	if len(got) != len(want) {
		t.Fatalf("breaks: got %d, want %d\n got: %+v\nwant: %+v", len(got), len(want), got, want)
	}
	for i := range want {
		if !got[i].After.Equal(want[i].After) || got[i].Text != want[i].Text {
			t.Errorf("break %d:\n got: %v %q\nwant: %v %q",
				i, got[i].After, got[i].Text, want[i].After, want[i].Text)
		}
	}
}
