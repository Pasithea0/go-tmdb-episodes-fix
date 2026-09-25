# pkg/remap/hints.go — episode identity hints

`remap.go` holds the mapper plus `EpisodeHints` / `TvdbToTmdbWithHints` /
`ImdbToTmdbWithHints` and the identity-resolution helpers they use. This file is
its companion: what the hints mean, what was verified against live TVDB/TMDB, and
what is knowingly not solved.

## Why the hints exist

A caller's `(season, episode)` pair only means something **relative to a TVDB
numbering order**. For series 73871 (Futurama):

| request | TVDB episode | TMDB result |
|---|---|---|
| `alternate` s6e1 | id 8234611 *Bender's Big Score (1)* | — (see Findings) |
| `official` / `default` s6e1 | id 1051911 *Rebirth* | S6E1 id 35077 |
| `dvd` s6e1 | id 4105084 | its own episode |
| `alternate` s7e1 | id 1051911 *Rebirth* | **S6E1** id 35077 |
| `default` s7e1 | id 4319164 *The Bots and the Bees* | S7E1 id 35107 |

Without hints, a caller whose files follow `alternate` gets *The Bots and the
Bees*'s segments for *Rebirth* — the same file, two different answers, nothing
flagged (jellyfin-plugin#31).

## Contract

```go
type EpisodeHints struct {
    TVDBOrder     string // which order the numbers belong to
    TVDBEpisodeID int64  // TVDB episode id — unique across every order
    EpisodeName   string // the caller's episode title, for corroboration
}
```

Entry points (the historical ones are now thin wrappers with empty hints):

```go
m.TvdbToTmdbWithHints(ctx, tvdbSeriesID, season, episode, hints)
m.ImdbToTmdbWithHints(ctx, imdbID, tmdbSeriesID /* 0 = resolve from TVDB */, season, episode, hints)
```

### Precedence — most specific first

1. **`TVDBEpisodeID`** identifies the episode exactly; the order it is found
   under is then the order that defines the caller's numbers. The id is looked up
   at the caller's numbers under every order (`ordersForHints`), because the
   numbers alone cannot say which order they came from.
2. **`TVDBOrder`** is used **alone — no fallback**. The caller said what the
   numbers mean; falling back would answer a different question.
3. **The numbers alone** — primary season type, then `FallbackSeasonTypes`
   (historical behaviour, unchanged).
4. **`EpisodeName`** corroborates every step, and resolves the episode on its own
   when nothing else does and the name matches **exactly one** episode of the
   series (all orders searched, matches deduped by episode id, ambiguous → error).

### Reported provenance (additive fields on the results)

| field | meaning |
|---|---|
| `tvdb_order_used` | the order the resolved record was found under; empty when resolved from an id whose numbers exist under no order |
| `identity_source` | `season_episode_numbers` / `tvdb_order` / `tvdb_episode_id` / `episode_name` |
| `episode_name_match` | `match` / `mismatch` / `unknown` |

`episode_name_match` is a **fact, not a policy**: a missing name on either side is
`unknown`, never `mismatch`. Deciding that a mismatch means "return no data"
belongs to the caller (the API), because only the caller knows whether the name
it sent was a localized title.

### Errors the caller must handle

`*EpisodeHintError` (`Field`, `Detail`) is returned when:

- `tvdb_order` is not a known order spelling;
- `tvdb_episode_id` contradicts the numbers — the pair resolves under some order
  to a **different** episode id;
- the id belongs to a different series than the one requested.

An id that simply cannot be located is not an error: it resolves the episode by
its own record (see precedence 1).

### Accepted order spellings

`official`, `dvd`, `absolute`, `alternate`, `regional`, `default` — plus
aliases: `aired`/`aired order`/`official order` → `official`; `altdvd` →
`dvd`; `alttwo`/`streaming` → `alternate`; `production` → `regional`.
Case, spaces, dashes and underscores are ignored. `altdvd`/`alttwo` are the
Jellyfin `Series.DisplayOrder` spellings, so a plugin can forward that value
verbatim.

## Live verification (2026-09-24, series 73871 / tmdb 615)

| request | identity_source | order_used | resolved TVDB | TMDB | name_match |
|---|---|---|---|---|---|
| default s7e1, no hints | `season_episode_numbers` | `default` | *The Bots and the Bees* | S7E1 | unknown |
| s7e1 + `alternate` + name *Rebirth* | `tvdb_order` | `alternate` | *Rebirth* | **S6E1** | match |
| s7e1 + `alttwo` (alias) | `tvdb_order` | `alternate` | *Rebirth* | **S6E1** | unknown |
| s7e1 + name *Rebirth*, no order | `season_episode_numbers` | `default` | *The Bots and the Bees* | S7E1 | **mismatch** |
| s12e1 + `alternate` + name *The One Amigo* | `tvdb_order` | `alternate` | *The One Amigo* | **S9E1** | match |
| s12e1, no hints | `season_episode_numbers` | `dvd` | *Beef* | S11E1 | unknown |
| s12e1 + name *The One Amigo*, no order | `season_episode_numbers` | `dvd` | *Beef* | S11E1 | **mismatch** |
| s6e1 + `tvdb_episode_id=8234611` | `tvdb_episode_id` | `alternate` | *Bender's Big Score (1)* | **unmapped** (see Findings) |
| s99e1 + name *Rebirth* | `episode_name` | — | *Rebirth* | S6E1 | match |
| imdb tt0149460 tmdb 615 s7e1 + `alternate` | `tvdb_order` | `alternate` | *Rebirth* | S6E1 | match |
| s7e1 + id 8234611 | — | — | error: contradiction | — | — |
| s7e1 + `tvdb_order=season2` | — | — | error: unknown order | — | — |

Reproduce with:

```bash
go run ./cmd/remap -direction tvdb2tmdb -tvdb-id 73871 -season 7 -episode 1 \
  -tvdb-order alternate -episode-name Rebirth
```

(`cmd/remap` loads `./.env`; `TVDB_API_KEY` plus `TMDB_BEARER_TOKEN` are needed for
the tvdb→tmdb side. A TMDB **v4 JWT** key must go in the bearer token — passing it
as `api_key=` 401s.)

## Findings — knowingly not solved here

1. **TVDB alternate s6e1–s6e16 are the four films split into four parts each;
   TMDB keeps each film as one special** (`S0E1 Bender's Big Score`,
   `2007-11-27`). The name scan cannot bridge it — normalized
   `bendersbigscore1` ≠ `bendersbigscore`, and the air dates differ by months.
   Mapping part 1 onto the whole film would attach the film's segments to a
   43-minute partial file, so the honest outcome is **unmapped** and the caller
   should get no data rather than another episode's. Those 16 alternate episodes
   stay unmapped until TMDB and TVDB agree on the split.
2. **Season 0 (specials) is never scanned.** `scanTMDBSeasons` walks seasons
   `1..number_of_seasons`, so a TVDB special cannot map to a TMDB special, and the
   films above are unreachable even by an exact name. Adding season 0 changes what
   "the right episode" means for specials (a many-to-one split, as in finding 1)
   and needs its own decision — deliberately out of scope for the hints work.
3. **A localized episode name is reported as `mismatch`.** The mapper does not
   know the caller's locale, so the policy ("never serve another episode's
   segments") has to be applied by the caller, ideally only when it also knows
   the stored episode's name differs.

## TODO

- [ ] (Batch 1) `EpisodeHints`, precedence, provenance, `cmd/remap` flags, unit
  tests, live table above — **done**.
- [ ] Decide whether season 0 (specials) joins the scan (finding 2), and with what
  split semantics (finding 1).
- [ ] `tmdb_episode_id` has no counterpart here: TMDB resolves an episode id
  directly (`/tv/episode/{id}` → show/season/episode), so that hint belongs in the
  API, not in this mapper.