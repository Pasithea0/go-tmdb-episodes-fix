package tvdb

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"sync"
	"time"
)

const defaultBaseURL = "https://api4.thetvdb.com/v4"

type Client struct {
	baseURL string
	apiKey  string
	pin     string
	http    *http.Client

	mu    sync.Mutex
	token string
}

func NewClient(apiKey string, pin string) *Client {
	return &Client{
		baseURL: defaultBaseURL,
		apiKey:  strings.TrimSpace(apiKey),
		pin:     strings.TrimSpace(pin),
		http: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Client) ensureToken(ctx context.Context) error {
	c.mu.Lock()
	hasToken := c.token != ""
	c.mu.Unlock()

	if hasToken {
		return nil
	}

	if c.apiKey == "" {
		return errors.New("tvdb api key is required")
	}

	body := map[string]string{"apikey": c.apiKey}
	if c.pin != "" {
		body["pin"] = c.pin
	}

	var resp struct {
		Data struct {
			Token string `json:"token"`
		} `json:"data"`
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodPost, "/login", nil, body, &resp); err != nil {
		return err
	}

	if strings.TrimSpace(resp.Data.Token) == "" {
		return errors.New("tvdb login succeeded but returned empty token")
	}

	c.mu.Lock()
	c.token = resp.Data.Token
	c.mu.Unlock()
	return nil
}

func (c *Client) do(ctx context.Context, method string, path string, query url.Values, body any, out any) error {
	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}

	var bodyReader io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return err
		}
		bodyReader = bytes.NewReader(b)
	}

	req, err := http.NewRequestWithContext(ctx, method, u.String(), bodyReader)
	if err != nil {
		return err
	}

	req.Header.Set("Accept", "application/json")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}

	if path != "/login" {
		if err := c.ensureToken(ctx); err != nil {
			return err
		}
		c.mu.Lock()
		token := c.token
		c.mu.Unlock()
		req.Header.Set("Authorization", "Bearer "+token)
	}

	res, err := c.http.Do(req)
	if err != nil {
		return err
	}
	defer res.Body.Close()

	raw, err := io.ReadAll(res.Body)
	if err != nil {
		return err
	}

	if res.StatusCode < 200 || res.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 1500 {
			msg = msg[:1500]
		}
		if res.StatusCode == http.StatusNotFound {
			return &NotFoundError{Method: method, Path: path, Body: msg}
		}
		return fmt.Errorf("tvdb %s %s: status=%d body=%s", method, path, res.StatusCode, msg)
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("tvdb %s %s: decode error: %w (body=%s)", method, path, err, string(raw))
	}
	return nil
}

// NotFoundError indicates the requested TVDB resource returned HTTP 404.
// The mapper treats it as "this season type / episode isn't present under this
// ordering" so it can fall through to other season types rather than aborting,
// which is how the season-type fallback is actually exercised (TVDB 404s a
// series' episodes under "default" when the show only exposes "official"/"dvd").
type NotFoundError struct {
	Method string
	Path   string
	Body   string
}

func (e *NotFoundError) Error() string {
	body := strings.TrimSpace(e.Body)
	if len(body) > 1500 {
		body = body[:1500]
	}
	return fmt.Sprintf("tvdb %s %s: status=404 body=%s", e.Method, e.Path, body)
}

type RemoteID struct {
	ID         string `json:"id"`
	SourceName string `json:"sourceName"`
}

type SeriesBaseRecord struct {
	ID   int    `json:"id"`
	Name string `json:"name"`
}

type SeriesExtendedRecord struct {
	ID        int        `json:"id"`
	Name      string     `json:"name"`
	RemoteIDs []RemoteID `json:"remoteIds"`
}

type EpisodeBaseRecord struct {
	ID          int64  `json:"id"`
	Name        string `json:"name"`
	Aired       string `json:"aired"`
	SeasonNumber int   `json:"seasonNumber"`
	Number      int    `json:"number"`
}

type EpisodeExtendedRecord struct {
	ID           int64      `json:"id"`
	Name         string     `json:"name"`
	Aired        string     `json:"aired"`
	SeasonNumber int        `json:"seasonNumber"`
	Number       int        `json:"number"`
	RemoteIDs    []RemoteID `json:"remoteIds"`
}

type SearchByRemoteIdResult struct {
	Series *SeriesBaseRecord `json:"series"`
}

func (c *Client) SearchSeriesByRemoteID(ctx context.Context, remoteID string) (*SeriesBaseRecord, error) {
	var resp struct {
		Data   []SearchByRemoteIdResult `json:"data"`
		Status string                  `json:"status"`
	}

	if err := c.do(ctx, http.MethodGet, "/search/remoteid/"+url.PathEscape(remoteID), nil, nil, &resp); err != nil {
		return nil, err
	}

	for _, item := range resp.Data {
		if item.Series != nil && item.Series.ID != 0 {
			return item.Series, nil
		}
	}
	return nil, nil
}

// FindSeriesByIMDbID resolves a TVDB series from an IMDb id (e.g. "tt0434665").
// IMDb and TVDB usually share the same season/episode numbering, so the returned
// series id can drive the tvdb->tmdb episode mapping for feeds that number by IMDb.
func (c *Client) FindSeriesByIMDbID(ctx context.Context, imdbID string) (*SeriesBaseRecord, string, error) {
	imdbID = strings.TrimSpace(imdbID)
	if imdbID == "" {
		return nil, "", errors.New("imdb id is required")
	}

	candidates := []string{
		imdbID,
		"imdb-" + imdbID,
		"imdb:" + imdbID,
	}

	for _, candidate := range candidates {
		s, err := c.SearchSeriesByRemoteID(ctx, candidate)
		if err != nil {
			return nil, "", err
		}
		if s != nil {
			return s, candidate, nil
		}
	}
	return nil, "", nil
}

func (c *Client) FindSeriesByTMDBID(ctx context.Context, tmdbID int) (*SeriesBaseRecord, string, error) {
	candidates := []string{
		strconv.Itoa(tmdbID),
		"tmdb-" + strconv.Itoa(tmdbID),
		"tmdb:" + strconv.Itoa(tmdbID),
		"themoviedb-" + strconv.Itoa(tmdbID),
		"themoviedb:" + strconv.Itoa(tmdbID),
	}

	for _, candidate := range candidates {
		s, err := c.SearchSeriesByRemoteID(ctx, candidate)
		if err != nil {
			return nil, "", err
		}
		if s != nil {
			return s, candidate, nil
		}
	}
	return nil, "", nil
}

func (c *Client) GetSeriesExtended(ctx context.Context, seriesID int) (*SeriesExtendedRecord, error) {
	var resp struct {
		Data   SeriesExtendedRecord `json:"data"`
		Status string               `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/series/"+strconv.Itoa(seriesID)+"/extended", nil, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Data.ID == 0 {
		return nil, errors.New("tvdb series extended returned empty data")
	}
	return &resp.Data, nil
}

func (c *Client) GetSeriesEpisodes(ctx context.Context, seriesID int, seasonType string, page int, season *int, episodeNumber *int, airDate *string) ([]EpisodeBaseRecord, error) {
	query := url.Values{}
	query.Set("page", strconv.Itoa(page))
	if season != nil {
		query.Set("season", strconv.Itoa(*season))
	}
	if episodeNumber != nil {
		query.Set("episodeNumber", strconv.Itoa(*episodeNumber))
	}
	if airDate != nil && strings.TrimSpace(*airDate) != "" {
		query.Set("airDate", strings.TrimSpace(*airDate))
	}

	path := fmt.Sprintf("/series/%d/episodes/%s", seriesID, url.PathEscape(seasonType))
	var resp struct {
		Data struct {
			Episodes []EpisodeBaseRecord `json:"episodes"`
		} `json:"data"`
		Status string `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, path, query, nil, &resp); err != nil {
		return nil, err
	}
	return resp.Data.Episodes, nil
}

func (c *Client) GetEpisodeExtended(ctx context.Context, episodeID int64) (*EpisodeExtendedRecord, error) {
	var resp struct {
		Data   EpisodeExtendedRecord `json:"data"`
		Status string                `json:"status"`
	}
	if err := c.do(ctx, http.MethodGet, "/episodes/"+strconv.FormatInt(episodeID, 10)+"/extended", nil, nil, &resp); err != nil {
		return nil, err
	}
	if resp.Data.ID == 0 {
		return nil, errors.New("tvdb episode extended returned empty data")
	}
	return &resp.Data, nil
}

