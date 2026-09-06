package remap

import (
	"context"
	"errors"
	"fmt"
	"strconv"
	"strings"
	"unicode"

	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tmdb"
	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tvdb"
)

type Options struct {
	TVDBAPIKey        string
	TVDBPIN           string
	TMDBBearerToken   string
	TVDBSeasonType    string
	FallbackSeasonTypes []string
	MaxTVDBPageScan   int
	MaxTMDBSeasonScan int
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

	tv, err := m.tmdb.GetTVDetails(ctx, tmdbSeriesID)
	if err != nil {
		return nil, err
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

		if epNum, matchedBy := matchSeasonEpisode(tvdbEp, seasonEps); matchedBy != "" {
			res.TMDBSeason = seasonNum
			res.TMDBEpisode = epNum
			res.MatchedBy = matchedBy
			return res, nil
		}
	}

	return nil, fmt.Errorf("unable to map tvdb %d s%de%d to tmdb", tvdbSeriesID, season, episode)
}

// matchSeasonEpisode picks the TMDB episode that best corresponds to a TVDB
// episode within one season's episodes, and returns its episode number plus the
// matching method ("" when nothing matches). Priority, most specific first:
//
//  1. exact normalized name — the strongest signal;
//  2. air date AND episode number aligned — preferred over a bare air-date
//     match because when a whole season shares one air date (e.g. a binge-drop
//     release) a date-only scan would always return episode 1. Aligning on the
//     number too selects the correct episode when the TVDB and TMDB orderings
//     agree, which is the common case;
//  3. bare air date — fallback for orderings that genuinely differ from TMDB
//     (reordered episodes where numbers don't align).
func matchSeasonEpisode(tvdbEp tvdb.EpisodeBaseRecord, seasonEps []tmdb.SeasonEpisode) (epNum int, matchedBy string) {
	// 1) Exact name match — the strongest signal.
	for _, ep := range seasonEps {
		if strings.TrimSpace(tvdbEp.Name) != "" && normalizeName(tvdbEp.Name) == normalizeName(ep.Name) {
			return ep.EpisodeNumber, "name_scan"
		}
	}
	// 2) Air date AND episode-number alignment.
	for _, ep := range seasonEps {
		if strings.TrimSpace(tvdbEp.Aired) != "" && strings.TrimSpace(ep.AirDate) != "" &&
			tvdbEp.Aired == ep.AirDate && ep.EpisodeNumber == tvdbEp.Number {
			return ep.EpisodeNumber, "air_date+number_scan"
		}
	}
	// 3) Bare air-date match.
	for _, ep := range seasonEps {
		if strings.TrimSpace(tvdbEp.Aired) != "" && strings.TrimSpace(ep.AirDate) != "" && tvdbEp.Aired == ep.AirDate {
			return ep.EpisodeNumber, "air_date_scan"
		}
	}
	return 0, ""
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
