package narrative

// bank.go: the authored phrase bank. Every situation carries 4–5 phrasing
// variants; Compose picks one by local YearDay modulo the variant count, so
// the page rotates day to day and never randomly. Nothing here is generated
// at runtime.
//
// House rules for anyone editing this file:
//
//   - Register: the approved v3 mockup line. Plain, active, editorial — a
//     knowledgeable running friend, not a system. No "conditions are
//     suboptimal", no "advisory in effect for your area".
//   - Sentence case. No exclamation marks. No em-dash pileups: one per
//     clause at most.
//   - Every clause stands on its own at 140 characters or less, because two
//     or three of them get joined into one paragraph.
//   - Name the number or the clock time. "Storms until 9 PM" beats "storms
//     later".
//   - SAFETY IS NOT A STYLE CHOICE. Every variant of a hazard key carries
//     the same instruction with the same force: a storm line always reads as
//     unambiguously off, an ice day always reads as skip. Variants change
//     the wording, never the verdict.
//   - Variant at index 4 of the five-variant keys used by the mockup fixture
//     is the copy Kui approved, so 28 July renders the approved page.
//
// Placeholders available per key (Compose fills exactly these):
//
//	icing, snow                            —
//	dangerous-chill, bitter-chill,
//	  very-cold, cold                      {chill}
//	heat-advisory-storms, heat-warning,
//	  heat-advisory                        {alert} {alert_end}
//	storms-active, storms-cleared          {time}
//	storms-approaching                     {time} {clear}
//	air-smoke, air-unhealthy,
//	  air-sensitive                        {aqi} {aqi_category}
//	air-moderate                           {aqi}
//	oppressive-dew                         {dew}
//	before-work-out, after-work-out        {day}
//	run-before-work-best/-good,
//	  run-midday, run-after-work-best/-good {day} {range}
//	next-viable-day                        {day} {range} {span}
//	no-window                              —
//	rain-likely                            {pop}
//	breezy                                 {wind} {gust} {dir}
//	front-clearing, heat-rebuilding,
//	  clean-morning                        —
//	break-storms-arrive, break-storms-pass,
//	  break-rain-arrives,
//	  break-evening-eases                  {time}
//	break-heat-rebuilds, break-clearing    —

// DefaultBank is the authored bank the site ships with.
var DefaultBank = Bank{
	// ---- Safety tier: hazards. The verdict is identical across variants.

	SitIcing: {
		"Ice is in the forecast, and there is no safe line through it — take today indoors.",
		"Freezing rain glazes everything it touches. Skip the run; the footing is not worth a fall.",
		"Ice on the pavement today. Treadmill, bike or rest day, but not the roads.",
		"Freezing rain is coming. Skip it — no session is worth going down on a glazed sidewalk.",
		"Freezing rain and ice in the forecast — footing is treacherous; skip it.",
	},

	SitSnow: {
		"Snow falls through the day, so expect soft footing and short strides.",
		"Snow today. Traction is the whole workout, so trade the pace goal for careful feet.",
		"Snow is on the way — the roads will run slow and the plows will crowd the shoulder.",
		"Snow through the forecast. Go by effort, stay lit up, and let the pace be what it is.",
		"Snow is coming in, and the untreated stretches will be slick. Keep the turnover short.",
	},

	SitDangerousChill: {
		"Wind chill hits {chill}, which is frostbite in minutes on bare skin. Skip the run.",
		"Wind chill down to {chill}. That is frostbite territory, so today belongs indoors.",
		"At {chill} wind chill, exposed skin freezes faster than you can finish a mile. Skip it.",
		"Wind chill of {chill} — no layering plan makes that safe. Take it inside today.",
		"Wind chill {chill}. Skip the roads; there is no version of this run that goes well.",
	},

	SitBitterChill: {
		"Wind chill around {chill}. Cover every inch of skin and cut the distance.",
		"Wind chill sits near {chill} — hat, gloves and something over your face, or don't go.",
		"At {chill} wind chill, bare skin is the whole risk. Layer it all and stay close to home.",
		"Wind chill of {chill}. Go out covered head to toe, and keep the loop short.",
		"Wind chill near {chill} — frostbite risk on exposed skin.",
	},

	SitVeryCold: {
		"Wind chill around {chill}, so gloves and a hat are not optional.",
		"Wind chill near {chill}. The first mile will hurt; the rest is fine once you are warm.",
		"Wind chill of {chill} — dress for the mile after next, not the one out the door.",
		"Wind chill near {chill}. Cover the hands and ears, and start slower than feels right.",
		"At {chill} wind chill, direction matters: head out into the wind and come home with it.",
	},

	SitCold: {
		"Wind chill near {chill} — a long-sleeve and light gloves cover it.",
		"Wind chill around {chill}. Cold enough for gloves, mild enough for a normal run.",
		"Wind chill of {chill}, so you will be comfortable a mile in. Dress a little light.",
		"Wind chill near {chill}, which is honest running weather with a hat on.",
		"At {chill} wind chill, tights and a long-sleeve are plenty.",
	},

	SitHeatAdvisoryStorms: {
		"A {alert} runs until {alert_end} and storms close the evening. Today is off the table.",
		"Heat and lightning both: the {alert} holds to {alert_end}, then storms. Skip today.",
		"The {alert} runs to {alert_end}, then storms take the evening. There is no window today.",
		"Between the {alert} until {alert_end} and evening storms, today has nothing. Skip it.",
		"Storms close this evening and the {alert} holds until {alert_end} — skip today.",
	},

	SitStormsActive: {
		"Thunderstorms are overhead now and should clear by {time}. Stay in until they do.",
		"Lightning is in the area. Nothing outdoors until it clears, around {time}.",
		"Storms are moving through right now. Nothing outdoors until {time}.",
		"Thunder now, so wait it out. The line should be past by {time}.",
		"Active lightning until about {time}. No run is worth being the tallest thing out there.",
	},

	SitStormsApproaching: {
		"Thunderstorms move in around {time} and hang on until {clear}. Today is off the table.",
		"Lightning arrives near {time} and clears around {clear} — skip today, the timing is tight.",
		"Storms build in by {time} and do not clear until {clear}. Skip today.",
		"A line of storms is due around {time}, done by {clear}. Don't try to beat it; skip today.",
		"Thunder is forecast from {time} to {clear}, which takes today's window with it. Skip it.",
	},

	// The all-clear reports the line moving through. It never promises a
	// window: the ranking decides whether anything is left of the day.
	SitStormsCleared: {
		"The storms moved through at {time}, and the air behind them is cooler.",
		"Lightning cleared the area by {time}; the sky is done with it.",
		"The line went through around {time}. Wet roads, cooler air, nothing left to dodge.",
		"Storms ended near {time} — puddles and cooler air are all that is left of them.",
		"Thunder cleared out by {time}, and the air behind it feels new.",
	},

	SitHeatWarning: {
		"An {alert} holds until {alert_end}. Cut the distance and carry water.",
		"The {alert} runs until {alert_end}, so run early or not at all and drop the pace goals.",
		"An {alert} through {alert_end}. This is the heat that sends people to the hospital.",
		"The {alert} lasts to {alert_end}. Treat any hard effort as off the table until it lets go.",
		"An {alert} holds to {alert_end} — shade, water, and half the distance you planned.",
	},

	SitHeatAdvisory: {
		"A {alert} runs to {alert_end}. Drop the pace, carry water, and skip the intervals.",
		"The {alert} lasts until {alert_end}, so run by effort rather than by watch.",
		"A {alert} through {alert_end}. Nothing hard today; the heat takes back any pace you force.",
		"The {alert} holds to {alert_end}, which makes every mile a shade-to-shade run.",
		"A {alert} holds until {alert_end} — go easy out there.",
	},

	SitAirSmoke: {
		"Wildfire smoke has the AQI at {aqi}, {aqi_category}. Move the run indoors.",
		"Smoke aloft: AQI {aqi}, {aqi_category}. Running pulls it deep, so take today inside.",
		"Smoke has the air at AQI {aqi} ({aqi_category}) — an easy indoor hour beats an outdoor one.",
		"AQI {aqi} on wildfire smoke, {aqi_category}. Skip the outdoor miles today.",
		"Smoke is sitting over the city: AQI {aqi}, {aqi_category}. Breathe less of it and run inside.",
	},

	SitAirUnhealthy: {
		"AQI {aqi}, {aqi_category}. Hard breathing means more of it in your lungs, so keep it easy.",
		"The air is {aqi_category} at AQI {aqi} — short and easy today, or indoors.",
		"AQI {aqi}, {aqi_category}. This is the day to move a workout inside.",
		"At AQI {aqi} the air is {aqi_category}; save the hard session for cleaner air.",
		"Air quality is {aqi_category} (AQI {aqi}) — ease the effort or go short.",
	},

	SitAirSensitive: {
		"AQI {aqi}, {aqi_category}. If you have asthma, keep it easy or take it indoors.",
		"Air quality is {aqi_category} (AQI {aqi}) — fine for most, rough on sensitive lungs.",
		"AQI {aqi}, {aqi_category}. Healthy runners are fine; anyone with asthma should go short.",
		"The air is {aqi_category} at AQI {aqi}, so dial back the intervals if your chest tightens.",
		"AQI {aqi} — {aqi_category}. Easy miles are fine; a hard workout is not worth it today.",
	},

	SitAirModerate: {
		"AQI {aqi}, moderate air. Most runners will not notice it; asthma might.",
		"Air is moderate at AQI {aqi}, which is city haze rather than a problem.",
		"AQI {aqi} is moderate — fine for the session you planned unless your lungs are sensitive.",
		"Moderate air at AQI {aqi}: worth knowing, not worth changing the run.",
		"AQI {aqi} sits in the moderate band, which sensitive runners may feel on hard efforts.",
	},

	SitOppressiveDew: {
		"Dew point {dew}, so sweat has nowhere to go. Drink early and run by feel.",
		"At a {dew} dew point, cooling stops working and the pace feels a minute slower.",
		"The dew point is {dew} — soupy enough that effort, not pace, is the honest measure.",
		"Dew point {dew}. That is the difference between a hard run and a miserable one.",
		"Dew point {dew} — the air is a wet towel and sweat will not evaporate off you.",
	},

	// ---- Windows tier.

	SitBeforeWorkOut: {
		"Before work {day} is out.",
		"The morning {day} is a write-off.",
		"Skip the early run {day}.",
		"Before work {day} does not work.",
		"Dawn {day} is off the table.",
	},

	SitAfterWorkOut: {
		"After work {day} is out.",
		"The evening {day} is a write-off.",
		"Skip the evening run {day}.",
		"After work {day} does not work.",
		"The evening slot {day} is off the table.",
	},

	SitRunBeforeWorkBest: {
		"Run before work {day}, {range} — the pick of the next two days.",
		"Before work {day} is the window: {range}, cool and clean.",
		"Take it before work {day}, {range}. Nothing later comes close.",
		"Run before work {day}, {range}. Cool air, clean air, no reason to wait.",
		"The best of the next 48 hours is before work {day}, {range}.",
	},

	SitRunBeforeWorkGood: {
		"Before work {day}, {range}, is the window to take.",
		"Go early {day} — {range}, before work.",
		"Best of the board: before work {day}, {range}.",
		"Run before work {day}, {range}. Everything later reads worse.",
		"Run before work {day}, {range}.",
	},

	SitRunMidday: {
		"Midday {day}, {range}, is the window.",
		"Go at midday {day} — {range}.",
		"Best of the board: midday {day}, {range}.",
		"Run midday {day}, {range}. Both ends of the day read worse.",
		"Run midday {day}, {range}.",
	},

	SitRunAfterWorkBest: {
		"Run after work {day}, {range} — the best window on the board.",
		"After work {day}, {range}, is the pick: cool, clean and settled.",
		"Take the evening {day}, {range}. Clean air and no heat to fight.",
		"Run after work {day}, {range}. As good as the next two days get.",
		"The best window is after work {day}, {range}.",
	},

	SitRunAfterWorkGood: {
		"After work {day}, {range}, is the window.",
		"Go after work {day} — {range}.",
		"Best of the board: after work {day}, {range}.",
		"Run after work {day}, {range}. The day's better half.",
		"Run after work {day}, {range}.",
	},

	SitNextViableDay: {
		"Today is out. The next clear stretch is {day} {span}, {range}.",
		"Nothing today holds up. The next real window is {day} {span}, {range}.",
		"Every window today is struck out; the next one that works is {day} {span}, {range}.",
		"Today has nothing worth running. Wait for {day} {span}, {range}.",
		"Skip ahead: the first clean window is {day} {span}, {range}.",
	},

	SitNoWindow: {
		"Nothing in the next 48 hours clears the bar. This is an indoor stretch.",
		"There is no safe window in the next two days. Treadmill, or wait it out.",
		"The next 48 hours hold no window worth taking outdoors.",
		"Nothing on the board for two days — check back when the forecast turns.",
		"No clear running window in the next 48 hours.",
	},

	// ---- Texture tier.

	SitRainLikely: {
		"Rain is likely through the window at {pop}%, so pick a hat and accept wet shoes.",
		"{pop}% chance of rain in that stretch: a wet run, not a cancelled one.",
		"Expect rain, {pop}% at the best hours. Wet is fine; just skip the new shoes.",
		"Rain sits at {pop}% across the window, so dress for damp and shorten if it turns heavy.",
		"{pop}% rain during the window. The roads will be wet and the air will be cool.",
	},

	SitFrontClearing: {
		"A front sweeps the mugginess out overnight.",
		"The air dries out behind tonight's front.",
		"Overnight the front pushes through and takes the humidity with it.",
		"Tonight's front trades the soup for something you can breathe.",
		"The front clears overnight.",
	},

	SitHeatRebuilding: {
		"The heat builds back quickly once the sun is up.",
		"By late morning the heat is back.",
		"Heat climbs again through the morning.",
		"The cool does not last; heat rebuilds by midday.",
		"Heat rebuilds toward midday.",
	},

	SitBreezy: {
		"Wind is {dir} at {wind} mph, gusting {gust} — an out-and-back pays for its tailwind.",
		"{dir} wind at {wind} mph with gusts to {gust}. Head into it and let it push you home.",
		"Expect a {dir} wind, {wind} mph gusting {gust}; the exposed waterfront will feel it.",
		"Wind {dir} {wind} mph, gusts to {gust}. Effort, not pace, on the outbound half.",
		"A {dir} wind at {wind} mph, gusting {gust}, adds a step to every mile facing it.",
	},

	SitCleanMorning: {
		"Cool, dry and clean the whole way through.",
		"Nothing to work around: cool air, clean air, no heat.",
		"This is the kind of air you plan a workout around.",
		"Cool and dry enough to run whatever you feel like.",
		"Cool and dry — as good as it gets.",
	},

	// ---- Table divider rows: short, lower case, no terminal period, so
	// they read as an aside inside the ledger rather than another data row.
	// The row template supplies the surrounding em dashes — a template that
	// brings its own renders them doubled.

	BreakStormsArrive: {
		"storms arrive by {time}",
		"lightning shuts it down at {time}",
		"the line reaches the city near {time}",
		"everything stops for thunder at {time}",
		"thunder moves in around {time}",
	},

	BreakStormsPass: {
		"the line clears around {time}",
		"thunder is done by {time}",
		"safe to be outside again after {time}",
		"the sky opens back up after {time}",
		"storms pass by {time}",
	},

	BreakRainArrives: {
		"rain moves in near {time}",
		"wet roads from {time} on",
		"rain starts around {time}",
		"the rain arrives by {time}",
		"steady rain from {time}",
	},

	BreakHeatRebuilds: {
		"the cool window closes here",
		"heat climbs from here",
		"the morning edge is gone",
		"it gets hot from this row down",
		"heat rebuilds toward midday",
	},

	BreakEveningEases: {
		"the heat backs off after {time}",
		"it turns runnable again by {time}",
		"the worst of the heat is past {time}",
		"cooler from {time} on",
		"the air eases after {time}",
	},

	BreakClearing: {
		"drier air from here",
		"the front is through; humidity drops",
		"humidity breaks below this line",
		"cleaner, drier air from this row on",
		"clearing and drier after the front",
	},
}
