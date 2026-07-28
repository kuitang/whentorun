package narrative

// bank_test.go enforces the house rules documented at the top of bank.go, so
// a future edit cannot quietly break the voice: variant counts, clause
// length, sentence case, the banned production vocabulary the Playwright
// audit also checks (e2e/tests/ui-check.spec.ts), the slots each key is
// actually given, and — the one that matters — that no phrasing variant of a
// skip-the-run situation softens the verdict.

import (
	"strings"
	"testing"
	"unicode"
	"unicode/utf8"
)

// maxClauseRunes: clauses get joined into one paragraph, so each has to
// stand alone at a readable length.
const maxClauseRunes = 140

// bankSlots is the exact set of placeholders Compose fills for each key. A
// template may use fewer, never others: an unknown {slot} renders literally
// on the page.
var bankSlots = map[SituationKey][]string{
	SitIcing:              nil,
	SitSnow:               nil,
	SitDangerousChill:     {"chill"},
	SitBitterChill:        {"chill"},
	SitVeryCold:           {"chill"},
	SitCold:               {"chill"},
	SitHeatAdvisoryStorms: {"alert", "alert_end"},
	SitStormsActive:       {"time"},
	SitStormsApproaching:  {"time", "clear"},
	SitStormsCleared:      {"time"},
	SitHeatWarning:        {"alert", "alert_end"},
	SitHeatAdvisory:       {"alert", "alert_end"},
	SitAirSmoke:           {"aqi", "aqi_category"},
	SitAirUnhealthy:       {"aqi", "aqi_category"},
	SitAirSensitive:       {"aqi", "aqi_category"},
	SitAirModerate:        {"aqi"},
	SitOppressiveDew:      {"dew"},
	SitBeforeWorkOut:      {"day"},
	SitAfterWorkOut:       {"day"},
	SitRunBeforeWorkBest:  {"day", "range"},
	SitRunBeforeWorkGood:  {"day", "range"},
	SitRunMidday:          {"day", "range"},
	SitRunAfterWorkBest:   {"day", "range"},
	SitRunAfterWorkGood:   {"day", "range"},
	SitNextViableDay:      {"day", "range", "span"},
	SitNoWindow:           nil,
	SitRainLikely:         {"pop"},
	SitFrontClearing:      nil,
	SitHeatRebuilding:     nil,
	SitBreezy:             {"wind", "gust", "dir"},
	SitCleanMorning:       nil,
	BreakStormsArrive:     {"time"},
	BreakStormsPass:       {"time"},
	BreakRainArrives:      {"time"},
	BreakHeatRebuilds:     nil,
	BreakEveningEases:     {"time"},
	BreakClearing:         nil,
}

// vetoKeys are the situations where the answer is "not today". Every variant
// must carry the instruction; the wording rotates, the verdict never does.
var vetoKeys = []SituationKey{
	SitIcing, SitDangerousChill, SitHeatAdvisoryStorms,
	SitStormsActive, SitStormsApproaching,
}

// vetoPhrases are the acceptable ways to say it plainly.
var vetoPhrases = []string{
	"skip", "indoors", "inside", "stay in", "off the table",
	"wait it out", "not the roads", "no run", "no window", "nothing outdoors",
}

// bannedCopy mirrors the production-copy blocklist in the Playwright audit,
// plus the WBGT category language that must never appear inside a table row
// (the divider templates render as rows).
var bannedCopy = []string{
	"no composite score", "no single number", "not a stop order", "not a veto",
	"band's ink", "one clock", "fig. 1", "fig 1",
	"nyc running conditions", "new york running conditions",
	"vetoed", "changes ink", "scrub",
	"extreme caution", "heat stress", "moderate heat",
}

func TestBankCoversEverySituation(t *testing.T) {
	for key := range bankSlots {
		if len(DefaultBank[key]) == 0 {
			t.Errorf("%s: no variants in DefaultBank", key)
		}
	}
	for key := range DefaultBank {
		if _, ok := bankSlots[key]; !ok {
			t.Errorf("%s: in DefaultBank but not declared in bankSlots", key)
		}
	}
}

func TestBankVariantCount(t *testing.T) {
	// Four or five variants: enough that the page does not read the same
	// two days running, few enough that every line stays authored.
	for key, variants := range DefaultBank {
		if len(variants) < 4 || len(variants) > 5 {
			t.Errorf("%s: %d variants, want 4 or 5", key, len(variants))
		}
		seen := map[Template]bool{}
		for _, v := range variants {
			if seen[v] {
				t.Errorf("%s: duplicate variant %q", key, v)
			}
			seen[v] = true
		}
	}
}

// worstCaseSlots are the longest values Compose can supply, so the length
// rule is checked against the copy as it will actually render, not against
// the template.
var worstCaseSlots = map[string]string{
	"alert":        "Excessive Heat Warning",
	"alert_end":    "further notice",
	"aqi":          "215",
	"aqi_category": "Unhealthy for Sensitive Groups",
	"chill":        "-18°F",
	"dew":          "75°F",
	"day":          "tomorrow",
	"range":        "11 AM–1 PM",
	"span":         "before work",
	"time":         "10:30 PM",
	"clear":        "10:30 PM",
	"pop":          "70",
	"wind":         "22",
	"gust":         "34",
	"dir":          "WNW",
}

func TestBankHouseStyle(t *testing.T) {
	for key, variants := range DefaultBank {
		for i, v := range variants {
			s := string(v)
			if n := utf8.RuneCountInString(fill(v, worstCaseSlots)); n > maxClauseRunes {
				t.Errorf("%s[%d]: %d runes filled, want <= %d: %q", key, i, n, maxClauseRunes, s)
			}
			if strings.Contains(s, "!") {
				t.Errorf("%s[%d]: exclamation mark: %q", key, i, s)
			}
			if strings.TrimSpace(s) != s {
				t.Errorf("%s[%d]: leading or trailing space: %q", key, i, s)
			}
			low := strings.ToLower(s)
			for _, bad := range bannedCopy {
				if strings.Contains(low, bad) {
					t.Errorf("%s[%d]: banned copy %q in %q", key, i, bad, s)
				}
			}
			checkSentenceCase(t, key, i, s)
		}
	}
}

// checkSentenceCase: above-fold clauses open with a capital (or a numeral,
// as in "70% rain during the window") and close with a period. Divider rows
// are asides inside a table: lower case, no terminal period, and no em
// dashes of their own — the ledger template wraps each row in a pair, so a
// template that brought its own would render doubled.
func checkSentenceCase(t *testing.T, key SituationKey, i int, s string) {
	t.Helper()
	if strings.HasPrefix(string(key), "break-") {
		if strings.Contains(s, "—") {
			t.Errorf("%s[%d]: the ledger template supplies the em dashes: %q", key, i, s)
		}
		if r, _ := utf8.DecodeRuneInString(s); unicode.IsUpper(r) {
			t.Errorf("%s[%d]: divider rows stay lower case: %q", key, i, s)
		}
		if strings.HasSuffix(s, ".") {
			t.Errorf("%s[%d]: divider rows take no terminal period: %q", key, i, s)
		}
		return
	}
	r, _ := utf8.DecodeRuneInString(s)
	if !unicode.IsUpper(r) && !unicode.IsDigit(r) && r != '{' {
		t.Errorf("%s[%d]: sentence case, please: %q", key, i, s)
	}
	if !strings.HasSuffix(s, ".") {
		t.Errorf("%s[%d]: clause must end in a period: %q", key, i, s)
	}
}

func TestBankPlaceholdersAreFilled(t *testing.T) {
	for key, variants := range DefaultBank {
		allowed := map[string]bool{}
		for _, slot := range bankSlots[key] {
			allowed[slot] = true
		}
		for i, v := range variants {
			for _, name := range placeholders(string(v)) {
				if !allowed[name] {
					t.Errorf("%s[%d]: {%s} is never filled by Compose: %q", key, i, name, v)
				}
			}
		}
	}
}

// TestBankNamesTheNumber: any key Compose hands a value to must actually use
// it. Copy that says "storms later" when it was given the clock time is the
// failure this catches.
func TestBankNamesTheNumber(t *testing.T) {
	for key, slots := range bankSlots {
		if len(slots) == 0 {
			continue
		}
		for i, v := range DefaultBank[key] {
			if len(placeholders(string(v))) == 0 {
				t.Errorf("%s[%d]: names no value though %v are available: %q", key, i, slots, v)
			}
		}
	}
}

// TestVetoCopyIsNeverSoftened is the rule that outranks style: a variant may
// change how the hazard is described, never whether the answer is "not
// today".
func TestVetoCopyIsNeverSoftened(t *testing.T) {
	for _, key := range vetoKeys {
		for i, v := range DefaultBank[key] {
			low := strings.ToLower(string(v))
			found := false
			for _, p := range vetoPhrases {
				if strings.Contains(low, p) {
					found = true
					break
				}
			}
			if !found {
				t.Errorf("%s[%d]: no unambiguous stop instruction: %q", key, i, v)
			}
		}
	}
}

// placeholders lists the {name} slots used by a template, in order.
func placeholders(s string) []string {
	var out []string
	for {
		i := strings.Index(s, "{")
		if i < 0 {
			return out
		}
		j := strings.Index(s[i:], "}")
		if j < 0 {
			return out
		}
		out = append(out, s[i+1:i+j])
		s = s[i+j+1:]
	}
}
