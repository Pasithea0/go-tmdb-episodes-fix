# pkg/remap/tmdb2tvdb.md — TMDB → TVDB mapping

Companion to `pkg/remap/hints.md`, which covers the opposite direction. Read this
before changing `TmdbToTvdb*`, and read the *Removed* section before adding a
fallback back.

## Why the direction is hard

The tvdb2tmdb direction is asked "given these TVDB numbers, which TMDB episode is
that?" The tmdb2tvdb direction is asked the reverse, and the reverse question is
the one with a trap: **TVDB numbers only mean something inside an order**, while
the TMDB number the caller holds is order-free. For series 73871 (Futurama):

| TMDB | TMDB air date | TVDB `default` | TVDB `alternate` |
|---|---|---|---|
| S6E1 | 2010-06-24 | s6e1 id 1051911 *Rebirth* | **s7e1** id 1051911 *Rebirth* |
| S7E1 | 2012-06-20 | s7e1 id 4319164 *The Bots and the Bees* | *(nothing on that date, nothing by that name)* |

Two facts fall out of that table, and both are load-bearing:

- The **same TMDB episode** ("Rebirth") lives at `s6e1` under `default` and at
  `s7e1` under `alternate`. The numbers are not a property of the episode.
- TVDB `default` `s6e1` and TVDB `alternate` `s6e1` are **different episodes with
  the same numbers** (`Rebirth` vs `Bender's Big Score (1)`). So a coordinate
  without an order does not name an episode.

Digits alone therefore cannot be mapped. Something has to corroborate.

## Contract

```go
type EpisodeHints struct {   // shared with the tvdb2tmdb direction
    TVDBOrder     string     // which order the caller's numbers belong to
    TVDBEpisodeID int64      // TVDB episode id — unique across every order
    EpisodeName   string     // episode title, used as corroboration
}

m.TmdbToTvdbInOrder(ctx, tmdbSeriesID, season, episode, order)
m.TmdbToTvdbWithHints(ctx, tmdbSeriesID, season, episode, hints)

// Historical signature, now a thin wrapper over TmdbToTvdbInOrder with the
// mapper's configured order:
m.TmdbToTvdb(ctx, tmdbSeriesID, season, episode)
```

### Precedence — strongest evidence first

1. **`TVDBEpisodeID`** — unique across every order, so it needs no numbering at
   all. Found by scanning the *named order*, which is what supplies the
   coordinates; TVDB's `/episodes/{id}` returns `seasonNumber: null`, so the id
   alone cannot produce a coordinate. Not present in the named order ⇒
   `EpisodeHintError`.
2. **Air date** within the named order, exactly as TMDB reports it.
3. **Name** within the named order, scanned across pages.
4. **Nothing.** An error.

`EpisodeName`, when supplied, must agree with the episode evidence produced. A
contradiction is an `EpisodeHintError` — not a silent pick, and not a warning.
Two of the plan's live cases depend on this: TMDB Futurama S6E1 would otherwise be
accepted as *Rebirth* on a numbering coincidence alone.

Successful matches report `MatchedBy` as `episode_id`, `air_date`, `air_date+name`,
or `name_scan`, plus `TVDBSeriesLookup` for the provenance of the series link.

## Series resolution must be *verified*, not merely found

`resolveVerifiedTVDBSeries` refuses any link whose title does not corroborate.

This is not defensive coding; it was measured. TVDB's `/search/remoteid` indexes a
bare number **across every id namespace**, so a valid TMDB id can collide with a
different show's id:

| TMDB | TMDB `external_ids` | TVDB `/search/remoteid/{bare}` |
|---|---|---|
| 1433 American Dad! | **73141 ✅** | **84070 — "War and Remembrance" ❌** |
| 456 The Simpsons | 71663 ✅ | 71663 ✅ |
| 615 Futurama | 73871 ✅ | 73871 ✅ |
| 37854 One Piece | 81797 ✅ | 81797 ✅ |
| 30984 Bleach | 74796 ✅ | 74796 ✅ |

So the lookup order is **TMDB first**: `external_ids` was correct in 5 of 5, the
one case TVDB gets wrong included. The prefixed TVDB forms (`tmdb-615`, `tmdb:615`,
`themoviedb-615`, `themoviedb:615`) are tried second. **The bare-number form is
never used.** TMDB's field is user-contributed and can itself go stale, so its
answer is name-checked too.

Without this check, American Dad's 464 stored submissions would have been relabelled
as episodes of a 1988 WWII miniseries, silently.

## Removed: the same-coordinate fallback

`TmdbToTvdb` used to end by returning whatever TVDB episode sat at the caller's
numbers, flagged `MatchedBy: "season_episode_fallback"`. It is gone. It was not a
mapping — it asserted that TMDB and TVDB number episodes identically, which is
false exactly where the mapper is needed: TMDB Futurama S7E1 is *The Bots and the
Bees* (2012-06-20), while TVDB `alternate` S7E1 is *Rebirth* (2010-06-24). The
fallback returned *Rebirth*, with no error.

The worst measured instance is Bleach. TMDB 30984 S2E1 is *The Blood Warfare*
(2022-10-11); TVDB 74796 `default` s2e1 is `突入！死神の世界` (2005-03-01). The
fallback would have answered a question about a 2022 episode with a 2005 one,
seventeen seasons out of place, and nothing downstream could have told.

If a caller has independently established that an order agrees, the assumption is
available as **opt-in** `Options.AllowCoordinateIdentityFallback`, and results then
carry `MatchedBy: "assumed_same_coordinates"` so it can never be read as evidence.
It is off by default, and a test pins that.

**Do not re-add it as a default.** An honest error is recoverable; a confident
wrong episode is not, because nothing downstream can tell it apart from a right one.

## Pagination

TVDB returns **at most 500 episodes per page** (measured: One Piece 500/500/242).
`fetchOrderEpisodes` pages to exhaustion and is used wherever a full list is needed.
Reading page 0 alone silently truncates every long-running series, which turns
episodes that do exist into "does not exist" rejections.

The one place a single page is read is the air-date lookup, and that is safe
because **TVDB applies the `airDate` filter server-side**. Verified 2026-09-25:
One Piece returns exactly one episode for `airDate=2024-01-07`, and that episode
(S22E4) sits well beyond page 0. If that ever became client-side, the
`len(eps) == 1` check would stop matching for long series without erroring.

## Live cases worth keeping as fixtures

| Case | What it pins |
|---|---|
| TMDB 615 S1E1 | Positive control: air date resolves to *Space Pilot 3000* |
| TMDB 615 S6E1, `default` | `Rebirth` at **s6e1** |
| TMDB 615 S6E1, `alternate` | The *same* episode at **s7e1** — order changes the coordinate |
| TMDB 615 S7E1, `alternate` | No coordinate-identity fallback: error, not *Rebirth* |
| TMDB 615 S6E1, `alternate` + title *Rebirth* | Order parameter and title corroboration both work |
| TMDB 615 S6E1, `alternate` + title *Bender's Big Score (1)* | Contradiction ⇒ `EpisodeHintError` |
| TMDB 615 S6E1, `alternate` + episode id 8234611 | An id needs no numbering; resolved via `episode_id` |
| TMDB 1433, external_ids present | Verified link 73141 wins over the wrong 84070 |
| TMDB 1433, external_ids absent | Unverified link refused, not used |
| TMDB 30984 Bleach S2E1 | Air date places the 2022 episode at TVDB **s17e1** |
| TMDB 30984 Bleach S2E9 | The 2005 episode is *not* leaked onto 2022 coordinates |
| One Piece 1242 episodes | Pages beyond the first are read |

## Known limits

- **Localized titles.** TVDB names anime in Japanese (One Piece S1E1 is
  `俺はルフィ！海賊王になる男だ！`), so a caller supplying an English title gets
  `NameMatchMismatch` and an `EpisodeHintError`. That is the correct behaviour —
  the mapper will not pretend to recognise a title it cannot — but it means
  `EpisodeName` should be omitted for localized series rather than treated as
  authoritative. Name *overlap* measured: One Piece 1/1220, Kamen Rider 6/1785,
  Bleach 49/420. Air date is the reliable signal there (One Piece 1162/1194).
- **Shared air dates.** A binge drop puts several episodes on one date; the
  air-date path then resolves by name, and if the name is unavailable it fails
  rather than picking one. See the `TvdbToTmdb` air-date scan fix for the sibling
  case.
- **Remakes and same-titled series.** `namesCorroborate` compares normalized
  titles exactly, so two distinct shows with identical names corroborate. It is a
  wrong-link check, not a disambiguator.
- **Season 0.** TVDB `default` keeps the four Futurama films as single specials
  (S0E2/3/5/6, ids 342888/359477/395236/427447). The mapper's TMDB season scan
  historically started at season 1; see the season-0 task in the plan.