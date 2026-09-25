package remap

import (
	"context"
	"fmt"
	"strings"
	"testing"

	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tmdb"
	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/tvdb"
)

// --- fakes -----------------------------------------------------------------
//
// The mapping rules are the part that silently hands back the wrong episode, so
// they are tested directly against fixtures rather than through a live client.

type fakeTVDB struct {
	// seriesByTMDB is what FindSeriesByTMDBID returns. It deliberately models
	// TVDB's bare-number remote-id search, which returns a wrong series when a
	// number collides across id namespaces.
	seriesByTMDB map[int]*tvdb.SeriesBaseRecord
	// seriesByRemote models SearchSeriesByRemoteID, keyed by the raw search term.
	seriesByRemote map[string]*tvdb.SeriesBaseRecord
	// episodes maps series id -> order -> full episode list.
	episodes map[int]map[string][]tvdb.EpisodeBaseRecord
	// episodesByID indexes the same records by episode id.
	episodesByID map[int64]tvdb.EpisodeBaseRecord
	// seriesByID backs GetSeriesExtended, used to read a series' name when
	// corroborating a link.
	seriesByID map[int]*tvdb.SeriesBaseRecord

	lookupUsed string
}

func (f *fakeTVDB) SearchSeriesByRemoteID(_ context.Context, remoteID string) (*tvdb.SeriesBaseRecord, error) {
	if s, ok := f.seriesByRemote[remoteID]; ok {
		return s, nil
	}
	return nil, fmt.Errorf("no tvdb series for remote id %q", remoteID)
}

func (f *fakeTVDB) FindSeriesByIMDbID(_ context.Context, imdbID string) (*tvdb.SeriesBaseRecord, string, error) {
	if s, ok := f.seriesByRemote["imdb:"+imdbID]; ok {
		return s, "imdb", nil
	}
	return nil, "", fmt.Errorf("no tvdb series for imdb id %q", imdbID)
}

func (f *fakeTVDB) FindSeriesByTMDBID(_ context.Context, tmdbID int) (*tvdb.SeriesBaseRecord, string, error) {
	if s, ok := f.seriesByTMDB[tmdbID]; ok {
		return s, f.lookupUsed, nil
	}
	return nil, "", fmt.Errorf("no tvdb series for tmdb id %d", tmdbID)
}

func (f *fakeTVDB) GetSeriesExtended(_ context.Context, seriesID int) (*tvdb.SeriesExtendedRecord, error) {
	if s, ok := f.seriesByID[seriesID]; ok {
		return &tvdb.SeriesExtendedRecord{ID: s.ID, Name: s.Name}, nil
	}
	return nil, fmt.Errorf("no tvdb series %d", seriesID)
}

func (f *fakeTVDB) GetEpisodeExtended(_ context.Context, id int64) (*tvdb.EpisodeExtendedRecord, error) {
	if ep, ok := f.episodesByID[id]; ok {
		return &tvdb.EpisodeExtendedRecord{ID: ep.ID, Name: ep.Name, Aired: ep.Aired}, nil
	}
	return nil, fmt.Errorf("no tvdb episode %d", id)
}

// GetSeriesEpisodes applies the season/episode/airDate filters and paginates at
// 500, matching TVDB's real page size (measured: One Piece 500/500/242).
func (f *fakeTVDB) GetSeriesEpisodes(
	_ context.Context, seriesID int, seasonType string, page int,
	season *int, episodeNumber *int, airDate *string,
) ([]tvdb.EpisodeBaseRecord, error) {
	orders, ok := f.episodes[seriesID]
	if !ok {
		return nil, fmt.Errorf("no episodes for tvdb series %d", seriesID)
	}
	all := orders[seasonType]

	var filtered []tvdb.EpisodeBaseRecord
	for _, ep := range all {
		if season != nil && ep.SeasonNumber != *season {
			continue
		}
		if episodeNumber != nil && ep.Number != *episodeNumber {
			continue
		}
		if airDate != nil && ep.Aired != *airDate {
			continue
		}
		filtered = append(filtered, ep)
	}

	const pageSize = 500
	start := page * pageSize
	if start >= len(filtered) {
		return nil, nil
	}
	end := start + pageSize
	if end > len(filtered) {
		end = len(filtered)
	}
	return filtered[start:end], nil
}

type fakeTMDB struct {
	details map[string]*tmdb.EpisodeDetails // key "series/season/episode"
	names   map[int]string                  // series id -> series name
	// tvdbID is what GetTvdbIDFromTmdbID-style resolution yields; modelled as the
	// external_ids route.
	tvdbID map[int]int
}

func (f *fakeTMDB) GetEpisodeDetails(_ context.Context, tvID, season, episode int) (*tmdb.EpisodeDetails, error) {
	if d, ok := f.details[fmt.Sprintf("%d/%d/%d", tvID, season, episode)]; ok {
		return d, nil
	}
	return nil, fmt.Errorf("no tmdb episode %d s%de%d", tvID, season, episode)
}

func (f *fakeTMDB) GetEpisodeByID(context.Context, int) (*tmdb.EpisodeByID, error) { return nil, nil }

func (f *fakeTMDB) GetTVDetails(_ context.Context, tvID int) (*tmdb.TVDetails, error) {
	name, ok := f.names[tvID]
	if !ok {
		return nil, fmt.Errorf("no tmdb series %d", tvID)
	}
	return &tmdb.TVDetails{Name: name, NumberOfSeasons: 1}, nil
}

func (f *fakeTMDB) GetEpisodeGroups(context.Context, int) ([]tmdb.EpisodeGroup, error) {
	return nil, nil
}
func (f *fakeTMDB) GetEpisodeGroup(context.Context, string) (*tmdb.EpisodeGroupDetail, error) {
	return nil, nil
}

// GetTvdbIDFromTmdbID models TMDB's external_ids route.
func (f *fakeTMDB) GetTvdbIDFromTmdbID(_ context.Context, tvID int) (int, error) {
	if id, ok := f.tvdbID[tvID]; ok {
		return id, nil
	}
	return 0, nil
}
func (f *fakeTMDB) GetSeasonEpisodes(context.Context, int, int) ([]tmdb.SeasonEpisode, error) {
	return nil, nil
}

// --- the Futurama fixture ---------------------------------------------------
//
// TMDB Futurama S6E1 is "Bender's Big Score (1)", aired 2008-03-23.
// TVDB default S6E1 is "Rebirth", aired 2010-06-24. The two orders disagree, so
// the same numbers name different episodes. Verified live 2026-09-25.

func futuramaMapper(t *testing.T) *Mapper {
	t.Helper()
	return &Mapper{
		tvdb: &fakeTVDB{
			seriesByTMDB: map[int]*tvdb.SeriesBaseRecord{
				615: {ID: 73871, Name: "Futurama"},
			},
			seriesByRemote: map[string]*tvdb.SeriesBaseRecord{
				"imdb:tt0149460": {ID: 73871, Name: "Futurama"},
				"tmdb-615":       {ID: 73871, Name: "Futurama"},
			},
			episodes: map[int]map[string][]tvdb.EpisodeBaseRecord{
				73871: {
					"default": {
						{ID: 1051911, Name: "Rebirth", Aired: "2010-06-24", SeriesID: 73871, SeasonNumber: 6, Number: 1},
						{ID: 1051912, Name: "In-A-Gadda-Da-Leela", Aired: "2010-07-01", SeriesID: 73871, SeasonNumber: 6, Number: 2},
						{ID: 1001, Name: "Space Pilot 3000", Aired: "1999-03-28", SeriesID: 73871, SeasonNumber: 1, Number: 1},
					},
					"alternate": {
						{ID: 8234611, Name: "Bender's Big Score (1)", Aired: "2008-03-23", SeriesID: 73871, SeasonNumber: 6, Number: 1},
					},
				},
			},
			episodesByID: map[int64]tvdb.EpisodeBaseRecord{
				1051911: {ID: 1051911, Name: "Rebirth", Aired: "2010-06-24", SeasonNumber: 6, Number: 1},
				8234611: {ID: 8234611, Name: "Bender's Big Score (1)", Aired: "2008-03-23", SeasonNumber: 6, Number: 1},
			},
			seriesByID: map[int]*tvdb.SeriesBaseRecord{
				73871: {ID: 73871, Name: "Futurama"},
			},
			lookupUsed: "tmdb",
		},
		tmdb: &fakeTMDB{
			details: map[string]*tmdb.EpisodeDetails{
				"615/6/1": {ID: 900001, Name: "Bender's Big Score (1)", AirDate: "2008-03-23", SeasonNumber: 6, EpisodeNumber: 1},
				"615/1/1": {ID: 900002, Name: "Space Pilot 3000", AirDate: "1999-03-28", SeasonNumber: 1, EpisodeNumber: 1},
			},
			names:  map[int]string{615: "Futurama"},
			tvdbID: map[int]int{615: 73871},
		},
		tvdbSeasonType:      "default",
		fallbackSeasonTypes: defaultFallbackSeasonTypes,
		maxTVDBPages:        200,
		maxTMDBSeasons:      300,
	}
}

// --- Task 0.0 ---------------------------------------------------------------

// TestTmdbToTvdbRefusesSameCoordinateFallback pins that an unmatchable episode
// returns an error instead of a same-numbered episode from the wrong order.
// TMDB S6E1 is "Bender's Big Score (1)"; returning TVDB default S6E1 "Rebirth"
// is a wrong answer, not a best-effort match.
func TestTmdbToTvdbRefusesSameCoordinateFallback(t *testing.T) {
	m := futuramaMapper(t)

	got, err := m.TmdbToTvdb(context.Background(), 615, 6, 1)
	if err == nil {
		if got.TVDBEpisodeName == "Rebirth" {
			t.Fatalf("returned TVDB default S6E1 %q for a request about %q — "+
				"that is the same-coordinate guess, not a mapping",
				got.TVDBEpisodeName, "Bender's Big Score (1)")
		}
		t.Fatalf("expected an error; got episode %q via matched_by=%q",
			got.TVDBEpisodeName, got.MatchedBy)
	}
	if !strings.Contains(err.Error(), "unable to map") {
		t.Fatalf("want an 'unable to map' error, got %v", err)
	}
}

// TestTmdbToTvdbMapsWhenEvidenceAgrees is the positive control: when the air date
// does identify the episode, the mapping still happens.
func TestTmdbToTvdbMapsWhenEvidenceAgrees(t *testing.T) {
	m := futuramaMapper(t)

	got, err := m.TmdbToTvdb(context.Background(), 615, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TVDBEpisodeName != "Space Pilot 3000" {
		t.Fatalf("got %q, want %q", got.TVDBEpisodeName, "Space Pilot 3000")
	}
	if got.MatchedBy != "air_date" {
		t.Fatalf("got matched_by %q, want %q", got.MatchedBy, "air_date")
	}
}

// TestTmdbToTvdbCoordinateFallbackIsOptIn pins that the removed behaviour, if
// re-enabled, announces itself rather than masquerading as a match.
func TestTmdbToTvdbCoordinateFallbackIsOptIn(t *testing.T) {
	m := futuramaMapper(t)
	m.allowCoordinateIdentityFallback = true

	got, err := m.TmdbToTvdb(context.Background(), 615, 6, 1)
	if err != nil {
		t.Fatalf("fallback was opted in, expected a result: %v", err)
	}
	if got.MatchedBy != "assumed_same_coordinates" {
		t.Fatalf("got matched_by %q; an assumption must never be reported as a match",
			got.MatchedBy)
	}
}

// --- Task 0.0b --------------------------------------------------------------
//
// TMDB 1433 is "American Dad!". TVDB's bare-number remote-id search answers with
// series 84070 "War and Remembrance", a 1988 WWII miniseries, because the search
// indexes a bare number across every id namespace. Measured live 2026-09-25.
// TMDB's own external_ids correctly gives 73141.

func americanDadMapper(t *testing.T, withExternalIDs bool) *Mapper {
	t.Helper()
	external := map[int]int{}
	if withExternalIDs {
		external[1433] = 73141
	}
	return &Mapper{
		tvdb: &fakeTVDB{
			seriesByTMDB: map[int]*tvdb.SeriesBaseRecord{
				1433: {ID: 84070, Name: "War and Remembrance"},
			},
			// The prefixed forms returned nothing when measured; only the bare
			// number resolves, and it resolves wrong.
			seriesByRemote: map[string]*tvdb.SeriesBaseRecord{},
			seriesByID: map[int]*tvdb.SeriesBaseRecord{
				73141: {ID: 73141, Name: "American Dad!"},
				84070: {ID: 84070, Name: "War and Remembrance"},
			},
			episodes: map[int]map[string][]tvdb.EpisodeBaseRecord{
				73141: {"default": {
					{ID: 5001, Name: "Pilot", Aired: "2005-02-06", SeriesID: 73141, SeasonNumber: 1, Number: 1},
				}},
				84070: {"default": {
					{ID: 5002, Name: "Part I: December 15-27, 1941", Aired: "1988-11-13", SeriesID: 84070, SeasonNumber: 1, Number: 1},
				}},
			},
			lookupUsed: "tvdb",
		},
		tmdb: &fakeTMDB{
			details: map[string]*tmdb.EpisodeDetails{
				"1433/1/1": {ID: 700001, Name: "Pilot", AirDate: "2005-02-06", SeasonNumber: 1, EpisodeNumber: 1},
			},
			names:  map[int]string{1433: "American Dad!"},
			tvdbID: external,
		},
		tvdbSeasonType:      "default",
		fallbackSeasonTypes: defaultFallbackSeasonTypes,
		maxTVDBPages:        200,
		maxTMDBSeasons:      300,
	}
}

// TestTmdbToTvdbPrefersVerifiedExternalIDs pins that TMDB's external_ids wins and
// is accepted only because its name corroborates.
func TestTmdbToTvdbPrefersVerifiedExternalIDs(t *testing.T) {
	m := americanDadMapper(t, true)

	got, err := m.TmdbToTvdb(context.Background(), 1433, 1, 1)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TVDBSeriesID != 73141 {
		t.Fatalf("resolved tvdb series %d, want 73141 (American Dad!); "+
			"84070 is War and Remembrance and must never be chosen", got.TVDBSeriesID)
	}
	if got.TVDBEpisodeName != "Pilot" {
		t.Fatalf("got episode %q, want %q", got.TVDBEpisodeName, "Pilot")
	}
}

// TestTmdbToTvdbRejectsUnverifiedSeries pins that an id match alone is not a
// mapping: with no external_ids available the search returns a series with a
// different name, and the mapper must refuse rather than relabel American Dad
// submissions as War and Remembrance.
func TestTmdbToTvdbRejectsUnverifiedSeries(t *testing.T) {
	m := americanDadMapper(t, false)

	got, err := m.TmdbToTvdb(context.Background(), 1433, 1, 1)
	if err == nil {
		t.Fatalf("accepted an unverified series link: tvdb %d %q",
			got.TVDBSeriesID, got.TVDBEpisodeName)
	}
	if !strings.Contains(err.Error(), "verified") {
		t.Fatalf("want a verification failure, got %v", err)
	}
}

func TestNamesCorroborate(t *testing.T) {
	cases := []struct {
		a, b string
		want bool
	}{
		{"American Dad!", "American Dad!", true},
		{"american dad", "American Dad!", true},
		{"Futurama", "Futurama", true},
		{"American Dad!", "War and Remembrance", false},
		{"Futurama", "Futurama (1999)", false}, // year suffix is not stripped
		{"", "Futurama", false},
		{"Futurama", "", false},
	}
	for _, c := range cases {
		if got := namesCorroborate(c.a, c.b); got != c.want {
			t.Fatalf("namesCorroborate(%q, %q) = %v, want %v", c.a, c.b, got, c.want)
		}
	}
}

// --- Task 0.0c --------------------------------------------------------------

// TestTmdbToTvdbInOrderResolvesAlternate pins the whole point of naming an order:
// the same TMDB coordinate resolves to different TVDB episodes depending on which
// order is consulted. TMDB S6E1 aired 2008-03-23; under "alternate" that places it
// at TVDB S6E1 "Bender's Big Score (1)", while "default" has no episode on that
// date at all.
func TestTmdbToTvdbInOrderResolvesAlternate(t *testing.T) {
	m := futuramaMapper(t)

	got, err := m.TmdbToTvdbInOrder(context.Background(), 615, 6, 1, "alternate")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TVDBEpisodeName != "Bender's Big Score (1)" {
		t.Fatalf("got %q, want %q", got.TVDBEpisodeName, "Bender's Big Score (1)")
	}
	if got.TVDBEpisodeID != 8234611 {
		t.Fatalf("got episode id %d, want 8234611", got.TVDBEpisodeID)
	}
}

// TestTmdbToTvdbInOrderRejectsUnknownOrder pins that a typo'd order is an error
// rather than a silent fallback to default -- silently answering in the wrong
// order is the failure mode this whole change exists to remove.
func TestTmdbToTvdbInOrderRejectsUnknownOrder(t *testing.T) {
	m := futuramaMapper(t)

	if _, err := m.TmdbToTvdbInOrder(context.Background(), 615, 1, 1, "bogus"); err == nil {
		t.Fatal("expected an error for an unknown order")
	}
}

// TestTmdbToTvdbInOrderEmptyUsesConfiguredOrder pins backward compatibility: an
// empty order means "the mapper's configured order", not "no order".
func TestTmdbToTvdbInOrderEmptyUsesConfiguredOrder(t *testing.T) {
	m := futuramaMapper(t)

	got, err := m.TmdbToTvdbInOrder(context.Background(), 615, 1, 1, "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if got.TVDBEpisodeName != "Space Pilot 3000" {
		t.Fatalf("got %q, want %q", got.TVDBEpisodeName, "Space Pilot 3000")
	}
}
