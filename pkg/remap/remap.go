package remap

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"time"
	"unicode"

	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tmdb"
	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tvdb"
)

type Options struct {
	TVDBAPIKey          string
	TVDBPIN             string
	TMDBBearerToken     string
	TVDBSeasonType      string
	FallbackSeasonTypes []string
	MaxTVDBPageScan     int
	MaxTMDBSeasonScan   int
}

var defaultFallbackSeasonTypes = []string{"official", "dvd", "alternate", "regional"}

type Mapper struct {
	tvdb                *tvdb.Client
	tmdb                *tmdb.Client
	tvdbSeasonType      string
	fallbackSeasonTypes []string
	maxTVDBPages        int
	maxTMDBSeasons      int
}

func NewMapper(opts Options) *Mapper {
	seasonType := strings.TrimSpace(opts.TVDBSeasonType)
	if seasonType == "" {
		seasonType = "default"
	}

	maxPages := opts.MaxTVDBPageScan
	if maxPages <= 0 {
		maxPages = 200
	}

	maxSeasons := opts.MaxTMDBSeasonScan
	if maxSeasons <= 0 {
		maxSeasons = 300
	}

	var tmdbClient *tmdb.Client
	if strings.TrimSpace(opts.TMDBBearerToken) != "" {
		tmdbClient = tmdb.NewClient(opts.TMDBBearerToken)
	}

	return &Mapper{
		tvdb:                tvdb.NewClient(opts.TVDBAPIKey, opts.TVDBPIN),
		tmdb:                tmdbClient,
		tvdbSeasonType:      seasonType,
		fallbackSeasonTypes: opts.FallbackSeasonTypes,
		maxTVDBPages:        maxPages,
		maxTMDBSeasons:      maxSeasons,
	}
}

type TmdbToTvdbResult struct {
	InputTMDBSeriesID int    `json:"input_tmdb_series_id"`
	InputSeason       int    `json:"input_season"`
	InputEpisode      int    `json:"input_episode"`
	TVDBSeriesID      int    `json:"tvdb_series_id"`
	TVDBEpisodeID     int64  `json:"tvdb_episode_id"`
	TVDBSeason        int    `json:"tvdb_season"`
	TVDBEpisode       int    `json:"tvdb_episode"`
	TVDBEpisodeName   string `json:"tvdb_episode_name"`
	MatchedBy         string `json:"matched_by"`
	TVDBSeriesLookup  string `json:"tvdb_series_lookup"`
}

func (m *Mapper) TmdbToTvdb(ctx context.Context, tmdbSeriesID int, season int, episode int) (*TmdbToTvdbResult, error) {
	if m.tmdb == nil {
		return nil, errors.New("tmdb bearer token is required for tmdb->tvdb mapping")
	}

	tvdbSeries, lookupUsed, err := m.tvdb.FindSeriesByTMDBID(ctx, tmdbSeriesID)
	if err != nil {
		return nil, err
	}
	if tvdbSeries == nil {
		return nil, fmt.Errorf("tvdb series not found for tmdb id %d", tmdbSeriesID)
	}

	tmdbEp, err := m.tmdb.GetEpisodeDetails(ctx, tmdbSeriesID, season, episode)
	if err != nil {
		return nil, err
	}

	if strings.TrimSpace(tmdbEp.AirDate) != "" {
		airDate := tmdbEp.AirDate
		eps, err := m.tvdb.GetSeriesEpisodes(ctx, tvdbSeries.ID, m.tvdbSeasonType, 0, nil, nil, &airDate)
		if err != nil {
			return nil, err
		}
		if len(eps) == 1 {
			ep := eps[0]
			return &TmdbToTvdbResult{
				InputTMDBSeriesID: tmdbSeriesID,
				InputSeason:       season,
				InputEpisode:      episode,
				TVDBSeriesID:      tvdbSeries.ID,
				TVDBEpisodeID:     ep.ID,
				TVDBSeason:        ep.SeasonNumber,
				TVDBEpisode:       ep.Number,
				TVDBEpisodeName:   ep.Name,
				MatchedBy:         "air_date",
				TVDBSeriesLookup:  lookupUsed,
			}, nil
		}

		if len(eps) > 1 {
			best := pickBestByName(eps, tmdbEp.Name)
			if best != nil {
				return &TmdbToTvdbResult{
					InputTMDBSeriesID: tmdbSeriesID,
					InputSeason:       season,
					InputEpisode:      episode,
					TVDBSeriesID:      tvdbSeries.ID,
					TVDBEpisodeID:     best.ID,
					TVDBSeason:        best.SeasonNumber,
					TVDBEpisode:       best.Number,
					TVDBEpisodeName:   best.Name,
					MatchedBy:         "air_date+name",
					TVDBSeriesLookup:  lookupUsed,
				}, nil
			}
		}
	}

	if strings.TrimSpace(tmdbEp.Name) != "" {
		wantName := tmdbEp.Name
		for page := 0; page < m.maxTVDBPages; page++ {
			eps, err := m.tvdb.GetSeriesEpisodes(ctx, tvdbSeries.ID, m.tvdbSeasonType, page, nil, nil, nil)
			if err != nil {
				return nil, err
			}
			if len(eps) == 0 {
				break
			}
			best := pickBestByName(eps, wantName)
			if best != nil {
				return &TmdbToTvdbResult{
					InputTMDBSeriesID: tmdbSeriesID,
					InputSeason:       season,
					InputEpisode:      episode,
					TVDBSeriesID:      tvdbSeries.ID,
					TVDBEpisodeID:     best.ID,
					TVDBSeason:        best.SeasonNumber,
					TVDBEpisode:       best.Number,
					TVDBEpisodeName:   best.Name,
					MatchedBy:         "name_scan",
					TVDBSeriesLookup:  lookupUsed,
				}, nil
			}
		}
	}

	s := season
	e := episode
	eps, err := m.tvdb.GetSeriesEpisodes(ctx, tvdbSeries.ID, m.tvdbSeasonType, 0, &s, &e, nil)
	if err != nil {
		return nil, err
	}
	if len(eps) > 0 {
		ep := eps[0]
		return &TmdbToTvdbResult{
			InputTMDBSeriesID: tmdbSeriesID,
			InputSeason:       season,
			InputEpisode:      episode,
			TVDBSeriesID:      tvdbSeries.ID,
			TVDBEpisodeID:     ep.ID,
			TVDBSeason:        ep.SeasonNumber,
			TVDBEpisode:       ep.Number,
			TVDBEpisodeName:   ep.Name,
			MatchedBy:         "season_episode_fallback",
			TVDBSeriesLookup:  lookupUsed,
		}, nil
	}

	return nil, fmt.Errorf("unable to map tmdb %d s%de%d to tvdb", tmdbSeriesID, season, episode)
}

type TvdbToTmdbResult struct {
	InputTVDBSeriesID int    `json:"input_tvdb_series_id"`
	InputSeason       int    `json:"input_season"`
	InputEpisode      int    `json:"input_episode"`
	TVDBEpisodeID     int64  `json:"tvdb_episode_id"`
	TVDBEpisodeName   string `json:"tvdb_episode_name"`
	TVDBAirDate       string `json:"tvdb_air_date"`
	TMDBSeriesID      int    `json:"tmdb_series_id"`
	TMDBEpisodeID     int    `json:"tmdb_episode_id"`
	TMDBSeason        int    `json:"tmdb_season"`
	TMDBEpisode       int    `json:"tmdb_episode"`
	MatchedBy         string `json:"matched_by"`
}

type ImdbToTmdbResult struct {
	InputIMDbID      string `json:"input_imdb_id"`
	InputSeason      int    `json:"input_season"`
	InputEpisode     int    `json:"input_episode"`
	TVDBSeriesID     int    `json:"tvdb_series_id"`
	TVDBSeriesName   string `json:"tvdb_series_name"`
	TVDBSeriesLookup string `json:"tvdb_series_lookup"`
	TVDBEpisodeID    int64  `json:"tvdb_episode_id"`
	TVDBEpisodeName  string `json:"tvdb_episode_name"`
	TVDBAirDate      string `json:"tvdb_air_date"`
	TMDBSeriesID     int    `json:"tmdb_series_id"`
	TMDBEpisodeID    int    `json:"tmdb_episode_id"`
	TMDBSeason       int    `json:"tmdb_season"`
	TMDBEpisode      int    `json:"tmdb_episode"`
	MatchedBy        string `json:"matched_by"`
}

// ImdbToTmdb maps an IMDb-numbered (season, episode) to the equivalent TMDB
// (season, episode). The mapper resolves the TVDB series from the IMDb id and
// reuses the tvdb->tmdb flow, because TVDB and IMDb usually share the same
// season/episode numbering — so an IMDb-numbered episode's TVDB record (name +
// air date, via TVDB remote ids) is the key that finds it on TMDB.
func (m *Mapper) ImdbToTmdb(ctx context.Context, imdbID string, season int, episode int) (*ImdbToTmdbResult, error) {
	imdbID = strings.TrimSpace(imdbID)
	if imdbID == "" {
		return nil, errors.New("imdb id is required")
	}
	if m.tmdb == nil {
		return nil, errors.New("tmdb bearer token is required for imdb->tmdb mapping")
	}

	tvdbSeries, lookupUsed, err := m.tvdb.FindSeriesByIMDbID(ctx, imdbID)
	if err != nil {
		return nil, err
	}
	if tvdbSeries == nil {
		return nil, fmt.Errorf("tvdb series not found for imdb id %s", imdbID)
	}

	// Reuse TvdbToTmdb: it fetches the TVDB episode, prefers the TMDB remote id
	// on the episode, and falls back to scanning TMDB seasons by name / air date.
	t2t, err := m.TvdbToTmdb(ctx, tvdbSeries.ID, season, episode)
	if err != nil {
		return nil, fmt.Errorf("imdb %s s%de%d -> tvdb %d: %w", imdbID, season, episode, tvdbSeries.ID, err)
	}

	return &ImdbToTmdbResult{
		InputIMDbID:      imdbID,
		InputSeason:      season,
		InputEpisode:     episode,
		TVDBSeriesID:     tvdbSeries.ID,
		TVDBSeriesName:   tvdbSeries.Name,
		TVDBSeriesLookup: lookupUsed,
		TVDBEpisodeID:    t2t.TVDBEpisodeID,
		TVDBEpisodeName:  t2t.TVDBEpisodeName,
		TVDBAirDate:      t2t.TVDBAirDate,
		TMDBSeriesID:     t2t.TMDBSeriesID,
		TMDBEpisodeID:    t2t.TMDBEpisodeID,
		TMDBSeason:       t2t.TMDBSeason,
		TMDBEpisode:      t2t.TMDBEpisode,
		MatchedBy:        t2t.MatchedBy,
	}, nil
}

// ImdbToTmdbWithTMDB maps an IMDb-numbered (season, episode) to the equivalent
// TMDB (season, episode) when the caller already knows the TMDB series id (e.g.
// from a feed-sync mismatches payload). This is the right entry point when the
// TVDB series record has no TMDB remote id — the known id is used directly
// instead of resolving it from TVDB. The TVDB series is still resolved from the
// IMDb id to fetch the episode's name + air date (IMDb and TVDB usually share
// season/episode numbering), which then find the episode on TMDB.
func (m *Mapper) ImdbToTmdbWithTMDB(ctx context.Context, imdbID string, tmdbSeriesID int, season int, episode int) (*ImdbToTmdbResult, error) {
	imdbID = strings.TrimSpace(imdbID)
	if imdbID == "" {
		return nil, errors.New("imdb id is required")
	}
	if tmdbSeriesID <= 0 {
		return nil, errors.New("tmdb series id is required")
	}
	if m.tmdb == nil {
		return nil, errors.New("tmdb bearer token is required for imdb->tmdb mapping")
	}

	tvdbSeries, lookupUsed, err := m.tvdb.FindSeriesByIMDbID(ctx, imdbID)
	if err != nil {
		return nil, err
	}
	if tvdbSeries == nil {
		return nil, fmt.Errorf("tvdb series not found for imdb id %s", imdbID)
	}

	eps, err := m.fetchEpisodesBySeasonType(ctx, tvdbSeries.ID, season, episode)
	if err != nil {
		return nil, err
	}
	if len(eps) == 0 {
		return nil, fmt.Errorf("tvdb episode not found: series=%d season=%d episode=%d (tried season types: %s)",
			tvdbSeries.ID, season, episode, m.seasonTypesTried())
	}

	tvdbEp := eps[0]
	seasonNum, epNum, epID, matchedBy, err := m.scanTMDBSeasons(ctx, tmdbSeriesID, tvdbEp)
	if err != nil {
		return nil, fmt.Errorf("imdb %s s%de%d -> tvdb %d: %w", imdbID, season, episode, tvdbSeries.ID, err)
	}

	return &ImdbToTmdbResult{
		InputIMDbID:      imdbID,
		InputSeason:      season,
		InputEpisode:     episode,
		TVDBSeriesID:     tvdbSeries.ID,
		TVDBSeriesName:   tvdbSeries.Name,
		TVDBSeriesLookup: lookupUsed,
		TVDBEpisodeID:    tvdbEp.ID,
		TVDBEpisodeName:  tvdbEp.Name,
		TVDBAirDate:      tvdbEp.Aired,
		TMDBSeriesID:     tmdbSeriesID,
		TMDBEpisodeID:    epID,
		TMDBSeason:       seasonNum,
		TMDBEpisode:      epNum,
		MatchedBy:        matchedBy,
	}, nil
}

func (m *Mapper) TvdbToTmdb(ctx context.Context, tvdbSeriesID int, season int, episode int) (*TvdbToTmdbResult, error) {
	eps, err := m.fetchEpisodesBySeasonType(ctx, tvdbSeriesID, season, episode)
	if err != nil {
		return nil, err
	}
	if len(eps) == 0 {
		return nil, fmt.Errorf("tvdb episode not found: series=%d season=%d episode=%d (tried season types: %s)",
			tvdbSeriesID, season, episode, m.seasonTypesTried())
	}

	tvdbEp := eps[0]
	ext, err := m.tvdb.GetEpisodeExtended(ctx, tvdbEp.ID)
	if err != nil {
		return nil, err
	}

	tmdbEpisodeID := findRemoteNumericID(ext.RemoteIDs, []string{"moviedb", "tmdb"})
	tmdbSeriesID := 0

	seriesExt, err := m.tvdb.GetSeriesExtended(ctx, tvdbSeriesID)
	if err == nil && seriesExt != nil {
		tmdbSeriesID = findRemoteNumericID(seriesExt.RemoteIDs, []string{"moviedb", "tmdb"})
	}

	res := &TvdbToTmdbResult{
		InputTVDBSeriesID: tvdbSeriesID,
		InputSeason:       season,
		InputEpisode:      episode,
		TVDBEpisodeID:     tvdbEp.ID,
		TVDBEpisodeName:   tvdbEp.Name,
		TVDBAirDate:       tvdbEp.Aired,
		TMDBSeriesID:      tmdbSeriesID,
		TMDBEpisodeID:     tmdbEpisodeID,
	}

	if m.tmdb == nil {
		if tmdbEpisodeID != 0 || tmdbSeriesID != 0 {
			res.MatchedBy = "tvdb_remote_ids"
			return res, nil
		}
		return nil, errors.New("tmdb bearer token is required for tvdb->tmdb mapping when remote ids are missing")
	}

	if tmdbEpisodeID != 0 {
		byID, err := m.tmdb.GetEpisodeByID(ctx, tmdbEpisodeID)
		if err != nil {
			return nil, err
		}
		res.TMDBSeason = byID.SeasonNumber
		res.TMDBEpisode = byID.EpisodeNumber
		if byID.ShowID != 0 {
			res.TMDBSeriesID = byID.ShowID
		}
		res.MatchedBy = "tvdb_episode_remote_id"
		return res, nil
	}

	if tmdbSeriesID == 0 {
		return nil, errors.New("tvdb series remote ids did not include a tmdb id")
	}

	if strings.TrimSpace(tvdbEp.Aired) == "" && strings.TrimSpace(tvdbEp.Name) == "" {
		return nil, errors.New("tvdb episode missing both air date and name; cannot scan tmdb seasons")
	}

	seasonNum, epNum, epID, matchedBy, err := m.scanTMDBSeasons(ctx, tmdbSeriesID, tvdbEp)
	if err != nil {
		return nil, err
	}
	res.TMDBSeason = seasonNum
	res.TMDBEpisode = epNum
	res.TMDBEpisodeID = epID
	res.MatchedBy = matchedBy
	return res, nil
}

// scanTMDBSeasons finds the TMDB (season, episode) matching a TVDB episode by
// scanning every season of the given TMDB series with matchSeasonEpisode.
func (m *Mapper) scanTMDBSeasons(ctx context.Context, tmdbSeriesID int, tvdbEp tvdb.EpisodeBaseRecord) (seasonNum int, epNum int, epID int, matchedBy string, err error) {
	tv, err := m.tmdb.GetTVDetails(ctx, tmdbSeriesID)
	if err != nil {
		return 0, 0, 0, "", err
	}

	limit := tv.NumberOfSeasons
	if limit > m.maxTMDBSeasons {
		limit = m.maxTMDBSeasons
	}

	for seasonNum := 1; seasonNum <= limit; seasonNum++ {
		seasonEps, err := m.tmdb.GetSeasonEpisodes(ctx, tmdbSeriesID, seasonNum)
		if err != nil {
			continue
		}

		if epNum, epID, matchedBy := matchSeasonEpisode(tvdbEp, seasonEps); matchedBy != "" {
			return seasonNum, epNum, epID, matchedBy, nil
		}
	}

	// Fallback: TMDB's own "TVDB Order" episode group maps TVDB season/episode
	// positions directly to TMDB episodes. Use it when the name/air-date scan
	// fails (e.g. TVDB's air date is off by more than a day, or the names are
	// in different languages). The group's season bucket order is the TVDB
	// season number; the episode's 0-based position within the bucket is the
	// TVDB episode number - 1. The mapped TMDB season/episode numbers and id
	// come from the group entry itself.
	if m.tvdbSeasonType == "default" || m.tvdbSeasonType == "" {
		if epID, tmdbSeason, tmdbEpisode := m.mapViaTVDBOrderGroup(ctx, tmdbSeriesID, tvdbEp); epID != 0 {
			return tmdbSeason, tmdbEpisode, epID, "tvdb_order_group", nil
		}
	}

	return 0, 0, 0, "", fmt.Errorf("unable to map tvdb episode %q (aired %s) to tmdb %d", tvdbEp.Name, tvdbEp.Aired, tmdbSeriesID)
}

// mapViaTVDBOrderGroup tries TMDB's "TVDB Order" episode group for the series
// and returns the TMDB episode id/season/episode for the given TVDB episode,
// or 0 when the group doesn't exist or lacks the episode.
func (m *Mapper) mapViaTVDBOrderGroup(ctx context.Context, tmdbSeriesID int, tvdbEp tvdb.EpisodeBaseRecord) (epID int, tmdbSeason int, tmdbEpisode int) {
	groups, err := m.tmdb.GetEpisodeGroups(ctx, tmdbSeriesID)
	if err != nil {
		return 0, 0, 0
	}

	var groupID string
	for _, g := range groups {
		name := strings.ToLower(g.Name)
		if strings.Contains(name, "tvdb") {
			groupID = g.ID
			break
		}
	}
	if groupID == "" {
		return 0, 0, 0
	}

	detail, err := m.tmdb.GetEpisodeGroup(ctx, groupID)
	if err != nil {
		return 0, 0, 0
	}

	for _, bucket := range detail.Groups {
		if epID, season, episode := lookupInGroupBucket(bucket, tvdbEp); epID != 0 {
			return epID, season, episode
		}
	}
	return 0, 0, 0
}

// lookupInGroupBucket finds the TMDB episode in one group bucket that matches a
// TVDB episode. The bucket's Order is the TVDB season number (a mismatched
// bucket never matches); each entry's 0-based Order is the TVDB episode number
// - 1. Returns the mapped TMDB episode id/season/episode, or 0 when not
// present.
func lookupInGroupBucket(bucket tmdb.EpisodeGroupOrder, tvdbEp tvdb.EpisodeBaseRecord) (epID int, season int, episode int) {
	if bucket.Order != tvdbEp.SeasonNumber {
		return 0, 0, 0
	}
	for _, entry := range bucket.Episodes {
		if entry.Order == tvdbEp.Number-1 {
			return entry.ID, entry.SeasonNumber, entry.EpisodeNumber
		}
	}
	return 0, 0, 0
}

// matchSeasonEpisode picks the TMDB episode that best corresponds to a TVDB
// episode within one season's episodes, and returns its episode number, its
// TMDB episode id, and the matching method ("" when nothing matches).
// Priority, most specific first:
//
//  1. exact normalized name — the strongest signal;
//  2. air date AND episode number aligned — preferred over a bare air-date
//     match because when a whole season shares one air date (e.g. a binge-drop
//     release) a date-only scan would always return episode 1. Aligning on the
//     number too selects the correct episode when the TVDB and TMDB orderings
//     agree, which is the common case;
//  3. bare air date — fallback for orderings that genuinely differ from TMDB
//     (reordered episodes where numbers don't align).
func matchSeasonEpisode(tvdbEp tvdb.EpisodeBaseRecord, seasonEps []tmdb.SeasonEpisode) (epNum int, epID int, matchedBy string) {
	// 1) Exact name match — the strongest signal.
	for _, ep := range seasonEps {
		if strings.TrimSpace(tvdbEp.Name) != "" && normalizeName(tvdbEp.Name) == normalizeName(ep.Name) {
			return ep.EpisodeNumber, ep.ID, "name_scan"
		}
	}
	// 2) Air date AND episode-number alignment. The date comparison tolerates a
	// one-day difference: TVDB often records the Japanese broadcast date while
	// TMDB uses the local one, so the same episode can be 2023-09-28 on TVDB and
	// 2023-09-29 on TMDB.
	for _, ep := range seasonEps {
		if strings.TrimSpace(tvdbEp.Aired) != "" && strings.TrimSpace(ep.AirDate) != "" &&
			datesWithinOneDay(tvdbEp.Aired, ep.AirDate) && ep.EpisodeNumber == tvdbEp.Number {
			return ep.EpisodeNumber, ep.ID, "air_date+number_scan"
		}
	}
	// 3) Bare air-date match (same one-day tolerance).
	for _, ep := range seasonEps {
		if strings.TrimSpace(tvdbEp.Aired) != "" && strings.TrimSpace(ep.AirDate) != "" && datesWithinOneDay(tvdbEp.Aired, ep.AirDate) {
			return ep.EpisodeNumber, ep.ID, "air_date_scan"
		}
	}
	return 0, 0, ""
}

// datesWithinOneDay reports whether two YYYY-MM-DD dates are the same day or
// exactly one day apart (TVDB vs TMDB air-date skew).
func datesWithinOneDay(a, b string) bool {
	ta, errA := time.Parse("2006-01-02", strings.TrimSpace(a))
	tb, errB := time.Parse("2006-01-02", strings.TrimSpace(b))
	if errA != nil || errB != nil {
		return false
	}
	d := ta.Sub(tb)
	return d >= -24*time.Hour && d <= 24*time.Hour
}

func pickBestByName(eps []tvdb.EpisodeBaseRecord, wantName string) *tvdb.EpisodeBaseRecord {
	if len(eps) == 0 {
		return nil
	}

	if strings.TrimSpace(wantName) == "" {
		return &eps[0]
	}

	want := normalizeName(wantName)
	for i := range eps {
		if want != "" && normalizeName(eps[i].Name) == want {
			return &eps[i]
		}
	}

	for i := range eps {
		if strings.EqualFold(strings.TrimSpace(eps[i].Name), strings.TrimSpace(wantName)) {
			return &eps[i]
		}
	}

	return nil
}

func normalizeName(s string) string {
	s = strings.ToLower(strings.TrimSpace(s))
	if s == "" {
		return ""
	}
	var b strings.Builder
	b.Grow(len(s))
	for _, r := range s {
		if unicode.IsLetter(r) || unicode.IsDigit(r) {
			b.WriteRune(r)
		}
	}
	return b.String()
}

func (m *Mapper) seasonTypesTried() string {
	types := []string{m.tvdbSeasonType}
	types = append(types, m.fallbackSeasonTypes...)
	return strings.Join(types, ", ")
}

// fetchEpisodesBySeasonType returns the episodes for a series/season/episode
// under the first season type (primary, then fallbacks) that yields any. TVDB
// 404s an ordering when the show's episodes don't exist under it (e.g. only
// "official"/"dvd", not "default"), so a NotFoundError - or an empty result -
// means "not under this ordering" and falls through to the next season type.
// Non-404 errors are remembered but do not stop the scan (a transient error on
// one ordering shouldn't hide a hit on another); if no ordering matches, the
// first non-404 error is returned so real failures aren't masked as "not found".
func (m *Mapper) fetchEpisodesBySeasonType(ctx context.Context, tvdbSeriesID, season, episode int) ([]tvdb.EpisodeBaseRecord, error) {
	types := append([]string{m.tvdbSeasonType}, m.fallbackSeasonTypes...)
	var firstErr error
	for _, seasonType := range types {
		if ctx.Err() != nil {
			return nil, ctx.Err()
		}
		s, e := season, episode
		eps, err := m.tvdb.GetSeriesEpisodes(ctx, tvdbSeriesID, seasonType, 0, &s, &e, nil)
		if err != nil {
			var nf *tvdb.NotFoundError
			if errors.As(err, &nf) {
				// Not present under this ordering - try the next season type.
				continue
			}
			if firstErr == nil {
				firstErr = err
			}
			continue
		}
		if len(eps) > 0 {
			return eps, nil
		}
	}
	if firstErr != nil {
		return nil, firstErr
	}
	return nil, nil
}

func findRemoteNumericID(ids []tvdb.RemoteID, hints []string) int {
	for _, id := range ids {
		name := strings.ToLower(strings.TrimSpace(id.SourceName))
		for _, hint := range hints {
			if strings.Contains(name, hint) {
				if n, err := strconv.Atoi(strings.TrimSpace(id.ID)); err == nil && n != 0 {
					return n
				}
			}
		}
	}
	return 0
}
