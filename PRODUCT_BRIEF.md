# NYC Running Conditions — Product Brief

The clearest hourly view of heat stress, humidity, sun, air quality and weather hazards for runners in New York City.

The differentiation is meteorological transparency, not an opaque recommendation algorithm.

## Build/stack decisions (from Kui)

- Public, unauthenticated web app.
- Use the **Merrit design system** (see Claude design assets for merrit).
- **HTMX** for interactivity; no React.
- Deploy to **Fly.io**.
- Search for good **.com domain names** related to run/weather/forecast.
- Several proposed design mockups for feedback before building.
- Test autonomously with **Playwright**; provide screenshot + video evidence.
- Review-server-over-tailnet setup exists in other projects (e.g. merrit) — reuse that pattern.
- Conditions for several popular NYC running paths.
- Geolocate the user's precise location within NYC.
- Interview Kui throughout.

## Do not invent a "Run Score"

A score such as 76/100 would imply a validated relationship among WBGT, UV, rain, AQI and wind that does not actually exist.

There is no scientifically accepted equation of the form:

    Running pleasantness = a·WBGT + b·AQI + c·UV + d·wind

The weights would be arbitrary. Worse, the quantities describe fundamentally different risks:

- **WBGT**: exertional heat stress
- **AQI**: inhaled pollution exposure
- **UV index**: radiation exposure
- **Dew point**: atmospheric moisture
- **Wind chill**: cold exposure
- **Rain and lightning**: immediate environmental conditions

Adding them together hides information. A beautiful 18°C WBGT with AQI 160 is not "moderately good." It is thermally excellent but potentially inappropriate for outdoor aerobic exercise.

## Use a metric dashboard with categorical interpretations

The homepage should show the actual metrics:

| Metric | Why show it | Interpretation source |
|---|---|---|
| WBGT | Primary warm-weather exercise stress | NWS/OSHA |
| Dew point | Intuitive humidity and sweat-evaporation context | Meteorological definition |
| UV index | Solar exposure and protection decisions | EPA |
| AQI | Pollution exposure during sustained ventilation | EPA AirNow |
| Wind chill | Exposed-skin cold stress | NWS |
| Wind and gusts | Effort, convection and route experience | Raw forecast |
| Precipitation | Comfort, visibility and surface conditions | Raw forecast |
| Alerts | Lightning, flooding, snow and severe weather | NWS |

WBGT is particularly defensible because NWS describes it as an exercise and outdoor-work heat-stress indicator incorporating temperature, humidity, wind, sun angle and cloud cover. It is used by athletic organizations and occupational-safety programs rather than being a consumer "feels like" invention.

The UV Index is already an EPA-standardized 1–11+ scale, and AQI already has official health categories from Good through Hazardous. Do not reinterpret those into proprietary scores.

## The interface can still answer "When should I run?"

You do not need a scalar to select a best window. Use a transparent ranking procedure.

### Step 1: safety exclusions

Mark an hour as unsuitable when it has:

- Active thunderstorm or severe-weather warning
- Meaningful lightning risk
- Flash-flood warning affecting likely routes
- Hazardous AQI
- Dangerous heat category
- Freezing rain or serious icing

These are vetoes, not score deductions.

### Step 2: choose the primary seasonal stress metric

For warm conditions:

1. Lowest WBGT category
2. Lowest WBGT value within that category
3. Lower precipitation probability
4. Lower AQI
5. Lower UV
6. Lower gusts

For cold conditions:

1. No icy precipitation
2. Wind chill within a useful running range
3. Lower gusts
4. No precipitation
5. Better daylight
6. Lower AQI

This is lexicographic ranking, not a weighted score. It is easy to explain:

> 7–8 AM is the best window because it has today's lowest WBGT before UV rises, with good air quality and no predicted rain.

That is much more trustworthy than:

> 7–8 AM scores 84.

## Suggested warm-weather display

An hourly row could look like:

| Time | WBGT | Dew point | UV | AQI | Rain | Interpretation |
|---|---|---|---|---|---|---|
| 6 AM | 21°C | 19°C | 0 | 42 | 10% | Good, humid |
| 9 AM | 23°C | 20°C | 3 | 46 | 10% | Moderate heat stress |
| Noon | 26°C | 21°C | 8 | 51 | 15% | Poor for hard running |
| 6 PM | 24°C | 21°C | 1 | 58 | 25% | Acceptable, muggy |

The words should be directly attributable:

- WBGT: low, moderate, high, extreme heat stress
- Dew point: dry, comfortable, humid, oppressive
- UV: low, moderate, high, very high, extreme
- AQI: Good, Moderate, Unhealthy for Sensitive Groups, etc.

You can add runner-specific prose without changing the underlying metric:

> High WBGT: reduce pace goals and consider shortening hard workouts.

That is interpretation, clearly separated from measurement.

## What the NYC-only MVP should contain

### 1. "Run now" conditions

A compact current panel:

- WBGT 23°C — Moderate heat stress
- Dew point 20°C — Humid
- UV 5 — Moderate
- AQI 41 — Good
- Wind 13 km/h, gusting 24 km/h
- No active weather alerts

### 2. Hourly 48-hour timeline

Show the next 48 hours because runners commonly decide among:

- This morning
- Lunch
- After work
- Tomorrow morning

The NWS operational WBGT forecast is hourly through approximately 36 hours, with progressively coarser intervals farther out.

### 3. Best windows

Show perhaps three factual summaries:

- Best overall: lowest safe WBGT with good AQI
- Best before work
- Best after work

No user accounts and no personalization required.

### 4. Metric explanations

Each metric should expand to explain:

- What it measures
- What it omits
- Why runners care
- Which authority defines the categories
- Whether the value is measured, forecast or calculated

This is a meaningful advantage over ordinary weather apps.

### 5. NYC route conditions

A later version could provide fixed NYC running zones:

- Central Park
- Hudson River Greenway
- Prospect Park
- East River paths
- Brooklyn waterfront
- Van Cortlandt Park

The value is not radically different air temperature across Manhattan. It is route context:

- Exposed waterfront wind
- Shade versus direct sun
- Bridge exposure
- Park shade
- Flood-prone or icy surfaces

For MVP, I would still begin with one NYC forecast location, probably Central Park, rather than pretending you have hyperlocal precision that the source models cannot support.

## Clothing should be deliberately modest

Because Dress My Run exists, clothing could be a small static layer:

> Typical outfit at 4°C with moderate wind: tights, long-sleeve technical top and light gloves.

But do not market the site around it. The site's differentiating claim should be:

> Know how the weather will affect your run—not merely what the weather is.

Dress My Run answers *what should I wear?*

This site should answer:

- How stressful will this run be?
- Why will it feel that way?
- When are conditions best?
- Is the limiting factor heat, humidity, UV, pollution, wind or precipitation?
- How much confidence should I place in the forecast?

## A restrained MVP

Build only these five elements initially:

1. Current WBGT, dew point, UV, AQI and wind
2. A 48-hour hourly table
3. Official categorical interpretations
4. Best morning, midday and evening windows
5. Weather and air-quality safety flags

No accounts, maps, ML, clothing inventory, personalized scores or arbitrary 0–100 index.

That product is both more scientifically defensible and more distinct from Dress My Run.
