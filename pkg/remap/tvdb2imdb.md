# TVDB → IMDb episode numbering

`TvdbToImdb` / `TvdbToImdbWithHints` answer one question: **which coordinate does
IMDb use for this TVDB episode?**

Be clear about what this is. IMDb publishes no episode-numbering API, and nothing
in this module's dependency chain exposes IMDb episode numbers — `FindSeriesByIMDbID`
returns a *series*, not episode numbers. So the answer is **derived from TVDB**,
never read from IMDb. Every result therefore carries the order it came from
(`IMDbOrder`) and the evidence that matched (`MatchedBy`), and callers should treat
it as evidence with provenance rather than an authoritative lookup.

## Why an order has to be chosen

A season/episode pair only means something relative to a TVDB ordering. IMDb
episodes *usually* follow TVDB's `alternate` order — but "usually" is the whole
problem:

- a series may have **no alternate order at all** (TVDB 404s an order the series
  has none of), in which case `default` is what IMDb lines up with;
- an alternate order that exists may **not list the episode**;
- and either order may place the episode at completely different coordinates than
  the caller's numbers.

Assuming any single order is how the wrong episode reaches a library, so the order
is decided by evidence rather than assumed.

## The rule

1. **Identity first.** The episode is resolved from the caller's numbers and hints
   exactly as the other directions do (episode id > order > numbers > name), giving
   one TVDB episode record.
2. **Locate that episode in every plausible order**, strongest evidence first:
   | Evidence | `MatchedBy` | Score |
   | --- | --- | --- |
   | TVDB episode id (unique across orders) | `tvdb_episode_id` | 6 |
   | Normalised name equal | `name` | 5 |
   | Prefix-tolerant name match | `name_loose` | 4 |
   | Air date + name within one date | `air_date+name` | 3 |
   | Air date + episode number (binge drop) | `air_date+number` | 2 |
   | Air date alone | `air_date` | 1 |
3. **Best evidence wins; `IMDbOrderPreference` breaks ties.** The preference list is
   `alternate, default, absolute, official, dvd, regional`.

Ties are the *common* case, not the exception: an episode id appears in every order
that lists the episode, so `alternate` and `default` both score 6 and preference is
what selects `alternate`. That is the "IMDb usually follows alternate" rule,
enforced where it is safe. Preference never overrides evidence, because the
question being asked is which order actually **contains** the episode.

Refusals are deliberate. Two episodes sharing a title, or a shared air date that no
episode number or name can break, produce *no* match rather than a first hit — and
if no order contains the episode, coordinate identity is not assumed:
`AllowCoordinateIdentityFallback` opts in, and the result is labelled
`assumed_same_coordinates`.

## Live verification

Read-only runs against live TVDB/TMDB with `go run ./cmd/remap -direction tvdb2imdb`
(the `tvdb2tmdb` column is the cross-check: TMDB's numbering is the closest
available proxy for IMDb's):

| Series | Input | `IMDbOrder` | Result | `MatchedBy` | `tvdb2tmdb` agrees? |
| --- | --- | --- | --- | --- | --- |
| Futurama (73871) | default s0e2 `Bender's Big Score` | `default` | s0e2 | `tvdb_episode_id` | yes — TMDB also keeps it in the specials (s0e1) |
| Futurama (73871) | default s0e1 `Everybody Loves Hypnotoad` | `alternate` | s0e1 | `tvdb_episode_id` | yes |
| Futurama (73871) | default s1e1 `Space Pilot 3000` | `alternate` | s1e1 | `tvdb_episode_id` | yes |
| Breaking Bad (81189) | default s1e1 `Pilot` | `default` | s1e1 | `tvdb_episode_id` | yes — series has no alternate order |

The first row is the interesting one, and it is why preference must not override
evidence: preferring `alternate` blindly would answer **s6e1**, while TMDB's own
mapping keeps that episode in the specials (TMDB s0e1 ↔ TVDB default s0e2). The
`alternate` order carries a *separate* TVDB record for it, so the id match lands in
`default`. Rows 2–4 show preference doing its job on ordinary episodes, and row 4
shows the "alternate isn't an option" fallback.

## Known limit

Where TVDB splits one real-world episode into **separate records per order** (some
specials and movies), and the orders disagree, no evidence in the module can
settle which numbering IMDb follows. The result reports the order and evidence it
used so a caller can decide — do not read a single coordinate as certain in that
case.

## CLI

```bash
go run ./cmd/remap -direction tvdb2imdb -tvdb-id 73871 -season 0 -episode 2
go run ./cmd/remap -direction tvdb2imdb -tvdb-id 73871 -season 6 -episode 1 -tvdb-episode-id 8234611
```
