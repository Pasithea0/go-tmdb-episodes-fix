# go-tmdb-episodes-fix
A go module to help remap TMDB to TVDB/IMDb episode order

## CLI
- Automatically loads `./.env` if present (without overriding already-exported env vars).
- Env vars:
  - TVDB_API_KEY
  - TVDB_PIN (optional)
  - TMDB_BEARER_TOKEN (required for tmdb2tvdb and for tvdb2tmdb when TVDB remote ids are missing)

TMDB → TVDB:

```bash
go run ./cmd/remap -direction tmdb2tvdb -tmdb-id 123 -season 1 -episode 18
```

TVDB → TMDB:

```bash
go run ./cmd/remap -direction tvdb2tmdb -tvdb-id 456 -season 1 -episode 18
```

## Library
- Entry point: pkg/remap
  - NewMapper(remap.Options)
  - (*Mapper).TmdbToTvdb(ctx, tmdbSeriesID, season, episode)
  - (*Mapper).TvdbToTmdb(ctx, tvdbSeriesID, season, episode)
