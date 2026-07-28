// Package rank turns merged hourly conditions into ranked running windows.
//
// It is pure computation over []domain.Hour and []domain.Alert — no I/O.
// Three stages, per the product brief:
//
//  1. Safety vetoes (veto.go): hard exclusions, each with a NAMED reason
//     string that the UI shows verbatim. Vetoes are never score deductions.
//  2. Season branch (season.go): warm vs cold by mean daytime apparent
//     temperature, selecting which lexicographic comparator applies.
//  3. Lexicographic ranking (lex.go): coarse categorical buckets compared
//     in priority order. Metrics are NEVER averaged into a composite score;
//     the comparator also reports the first key that differed so the UI can
//     explain a choice ("chosen because heat stress is one category lower").
//
// windows.go assembles candidate morning/midday/evening windows for today
// and tomorrow in America/New_York, extends them across adjacent
// same-bucket hours, vetoes any window containing a vetoed hour, and ranks
// the rest.
package rank
