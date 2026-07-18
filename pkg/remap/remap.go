package remap

import (
	"context"
	"errors"
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"unicode"

	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/anilist"
	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tmdb"
	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tvdb"
)

type Options struct {
	TVDBAPIKey        string
	TVDBPIN           string
	TMDBBearerToken   string
	TVDBSeasonType    string
	MaxTVDBPageScan   int
	MaxTMDBSeasonScan int
	// EnableAnilist controls whether AniList resolution is active (default: true).
	// AniList does not require an API key, but you may wish to disable it for testing.
	EnableAnilist bool
}

type Mapper struct {
	tvdb           *tvdb.Client
	tmdb           *tmdb.Client
	anilist        *anilist.Client
	tvdbSeasonType string
	maxTVDBPages   int
	maxTMDBSeasons int
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

	var anilistClient *anilist.Client
	if !opts.EnableAnilist {
		// disabled via options; leave nil
	} else {
		anilistClient = anilist.NewClient()
	}

	return &Mapper{
		tvdb:           tvdb.NewClient(opts.TVDBAPIKey, opts.TVDBPIN),
		tmdb:           tmdbClient,
		anilist:        anilistClient,
		tvdbSeasonType: seasonType,
		maxTVDBPages:   maxPages,
		maxTMDBSeasons: maxSeasons,
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
	s := season
	e := episode
	eps, err := m.tvdb.GetSeriesEpisodes(ctx, tvdbSeriesID, m.tvdbSeasonType, 0, &s, &e, nil)
	if err != nil {
		return nil, err
	}
	if len(eps) == 0 {
		return nil, fmt.Errorf("tvdb episode not found: series=%d season=%d episode=%d", tvdbSeriesID, season, episode)
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

		for _, ep := range seasonEps {
			if strings.TrimSpace(tvdbEp.Aired) != "" && strings.TrimSpace(ep.AirDate) != "" && tvdbEp.Aired == ep.AirDate {
				res.TMDBSeason = seasonNum
				res.TMDBEpisode = ep.EpisodeNumber
				res.MatchedBy = "air_date_scan"
				return res, nil
			}

			if strings.TrimSpace(tvdbEp.Name) != "" && normalizeName(tvdbEp.Name) == normalizeName(ep.Name) {
				res.TMDBSeason = seasonNum
				res.TMDBEpisode = ep.EpisodeNumber
				res.MatchedBy = "name_scan"
				return res, nil
			}
		}
	}

	return nil, fmt.Errorf("unable to map tvdb %d s%de%d to tmdb", tvdbSeriesID, season, episode)
}

// ---------- Provider resolution results ----------

// ProviderIDs holds cross-referenced provider IDs for a single media item.
// Only non-nil fields were successfully resolved.
type ProviderIDs struct {
	TMDB    *int    `json:"tmdb_id,omitempty"`
	TVDB    *int    `json:"tvdb_id,omitempty"`
	IMDb    *string `json:"imdb_id,omitempty"`
	AniList *int    `json:"anilist_id,omitempty"`
	// AniListTitle is the resolved title from AniList (set even when no TMDB/TVDB found).
	AniListTitle string `json:"anilist_title,omitempty"`
	// AniListFormat is the format from AniList ("TV", "MOVIE", "OVA", etc.).
	AniListFormat string `json:"anilist_format,omitempty"`
	MatchedBy     string `json:"matched_by"` // how the TMDB ID was resolved
}

// ResolveAnilistToProviders takes an AniList ID and attempts to find
// equivalent provider IDs (TMDB, TVDB, IMDb) using AniList's external links
// and title-based fallback search.
func (m *Mapper) ResolveAnilistToProviders(ctx context.Context, anilistID int) (*ProviderIDs, error) {
	if m.anilist == nil {
		return nil, errors.New("anilist client not initialised (EnableAnilist=false)")
	}

	media, err := m.anilist.GetMedia(ctx, anilistID)
	if err != nil {
		return nil, fmt.Errorf("fetch anilist media %d: %w", anilistID, err)
	}
	if media == nil {
		return nil, fmt.Errorf("anilist media %d not found", anilistID)
	}

	result := &ProviderIDs{
		AniList:       &anilistID,
		AniListTitle:  pickTitle(media.Title),
		AniListFormat: media.Format,
	}

	// Strategy A: Parse external links for TMDB and Crunchyroll/streaming URLs.
	// AniList often includes "TVDB" and "Crunchyroll" in externalLinks but rarely
	// includes TMDB directly. TVDB is more common, so we check for both.
	var crunchyrollURL string
	for _, link := range media.ExternalLinks {
		site := strings.ToLower(strings.TrimSpace(link.Site))
		switch {
		case strings.Contains(site, "themoviedb") || strings.Contains(site, "tmdb"):
			if id := extractNumericIDFromURL(link.URL); id > 0 {
				v := id
				result.TMDB = &v
				result.MatchedBy = "anilist_external_link_tmdb"
			}
		case strings.Contains(site, "thetvdb") || strings.Contains(site, "tvdb"):
			if id := extractNumericIDFromURL(link.URL); id > 0 {
				v := id
				result.TVDB = &v
				if result.MatchedBy == "" {
					result.MatchedBy = "anilist_external_link_tvdb"
				}
			}
		case strings.Contains(site, "imdb") || strings.Contains(site, "imdb"):
			if id := extractIMDbIDFromURL(link.URL); id != "" {
				result.IMDb = &id
			}
		case strings.Contains(site, "crunchyroll"):
			crunchyrollURL = link.URL
		}
	}

	// Strategy B: If we found TMDB, also resolve TVDB using the existing mapper.
	if result.TMDB != nil && m.tvdb != nil {
		series, lookupUsed, err := m.tvdb.FindSeriesByTMDBID(ctx, *result.TMDB)
		if err == nil && series != nil {
			result.TVDB = &series.ID
			if result.MatchedBy == "anilist_external_link_tmdb" {
				result.MatchedBy = "anilist_external_link_tmdb+tvdb"
			}
			_ = lookupUsed
		}
	}

	// Strategy C: If we found TMDB but have an AniList-only ID, resolve via
	// AniList → Crunchyroll URL → TMDB search (future enhancement via external API).
	_ = crunchyrollURL // placeholder for future Crunchyroll→TMDB reverse lookup

	// Strategy D: Title-based TMDB search fallback (only use when TMDB client is available
	// and we haven't found a TMDB ID yet).
	if result.TMDB == nil && m.tmdb != nil {
		title := pickTitle(media.Title)
		if title != "" {
			tmdbID, mediaType := m.searchTMDBByTitle(ctx, title, media.Episodes)
			if tmdbID > 0 {
				result.TMDB = &tmdbID
				result.MatchedBy = "title_search_" + mediaType
			}
		}
	}

	return result, nil
}

// ResolveAnilistEpisode maps an AniList episode number to TMDB season/episode
// for the given series. Most anime use a flat episode list (season=1), so this
// is a simple passthrough unless the item has been manually remapped.
// Returns (season, episode).
func (m *Mapper) ResolveAnilistEpisode(anilistEpisode int) (season, episode int) {
	// Flat model: all episodes are season 1.
	// Override this when AniList data is linked to a TMDB item with multiple seasons.
	return 1, anilistEpisode
}

// ---------- helpers for ProviderIDs ----------

// pickTitle returns the best available title (English > Romaji > Native).
func pickTitle(t anilist.MediaTitle) string {
	if t.English != "" {
		return t.English
	}
	if t.Romaji != "" {
		return t.Romaji
	}
	return t.Native
}

var tmdbURLPattern = regexp.MustCompile(`themoviedb\.org/(?:tv|movie)/(\d+)`)

// extractNumericIDFromURL attempts to extract a numeric ID from common URL patterns.
func extractNumericIDFromURL(rawURL string) int {
	// Try TMDB URL pattern first
	if matches := tmdbURLPattern.FindStringSubmatch(rawURL); len(matches) >= 2 {
		if n, err := strconv.Atoi(matches[1]); err == nil {
			return n
		}
	}
	// Fallback: try to find any trailing number
	rawURL = strings.TrimRight(rawURL, "/")
	parts := strings.Split(rawURL, "/")
	if len(parts) > 0 {
		last := parts[len(parts)-1]
		if n, err := strconv.Atoi(last); err == nil {
			return n
		}
	}
	return 0
}

// extractIMDbIDFromURL pulls an IMDb ID (tt1234567) from a URL path.
func extractIMDbIDFromURL(rawURL string) string {
	rawURL = strings.TrimRight(rawURL, "/")
	parts := strings.Split(rawURL, "/")
	for _, part := range parts {
		part = strings.TrimSpace(part)
		if strings.HasPrefix(part, "tt") && len(part) >= 9 {
			// Verify trailing digits
			allDigits := true
			for _, r := range part[2:] {
				if r < '0' || r > '9' {
					allDigits = false
					break
				}
			}
			if allDigits {
				return part
			}
		}
	}
	return ""
}

// searchTMDBByTitle searches TMDB by title and returns the best-matching series or movie ID.
// Returns (0, "") if no match is found or the TMDB client is unavailable.
func (m *Mapper) searchTMDBByTitle(ctx context.Context, title string, expectedEpisodes *int) (int, string) {
	if m.tmdb == nil || title == "" {
		return 0, ""
	}

	// Search both TV and movie endpoints
	type searchResult struct {
		id          int
		mediaType   string
		episodeDiff int // how many episodes off from expected
	}

	var best searchResult

	// TV search
	if tvShows, err := m.tmdb.SearchTV(ctx, title, 1); err == nil {
		for _, show := range tvShows {
			if normalizeName(show.Name) == normalizeName(title) {
				diff := 0
				if expectedEpisodes != nil {
					if show.NumberOfEpisodes > 0 {
						d := *expectedEpisodes - show.NumberOfEpisodes
						if d < 0 {
							d = -d
						}
						diff = d
					}
				}
				if best.id == 0 || diff < best.episodeDiff {
					best = searchResult{id: show.ID, mediaType: "tv", episodeDiff: diff}
				}
			}
		}
	}

	// Movie search
	if movies, err := m.tmdb.SearchMovie(ctx, title, 1); err == nil {
		for _, movie := range movies {
			if normalizeName(movie.Title) == normalizeName(title) {
				if best.id == 0 {
					best = searchResult{id: movie.ID, mediaType: "movie", episodeDiff: 0}
				}
			}
		}
	}

	return best.id, best.mediaType
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
