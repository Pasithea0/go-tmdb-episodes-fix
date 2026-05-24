package tmdb

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"
)

const defaultBaseURL = "https://api.themoviedb.org/3"

type Client struct {
	baseURL string
	token   string
	http    *http.Client
}

func NewClient(bearerToken string) *Client {
	return &Client{
		baseURL: defaultBaseURL,
		token:   strings.TrimSpace(bearerToken),
		http: &http.Client{
			Timeout: 20 * time.Second,
		},
	}
}

func (c *Client) doGet(ctx context.Context, path string, query url.Values, out any) error {
	if c.token == "" {
		return errors.New("tmdb bearer token is required")
	}

	u, err := url.Parse(c.baseURL)
	if err != nil {
		return err
	}
	u.Path = strings.TrimSuffix(u.Path, "/") + path
	if query != nil {
		u.RawQuery = query.Encode()
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, u.String(), nil)
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	req.Header.Set("Authorization", "Bearer "+c.token)

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
		return fmt.Errorf("tmdb GET %s: status=%d body=%s", path, res.StatusCode, msg)
	}

	if out == nil {
		return nil
	}

	if err := json.Unmarshal(raw, out); err != nil {
		return fmt.Errorf("tmdb GET %s: decode error: %w (body=%s)", path, err, string(raw))
	}
	return nil
}

type EpisodeDetails struct {
	ID            int
	Name          string
	AirDate       string
	SeasonNumber  int
	EpisodeNumber int
}

func (c *Client) GetEpisodeDetails(ctx context.Context, tvID int, season int, episode int) (*EpisodeDetails, error) {
	path := fmt.Sprintf("/tv/%d/season/%d/episode/%d", tvID, season, episode)
	var resp struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		AirDate       string `json:"air_date"`
		SeasonNumber  int    `json:"season_number"`
		EpisodeNumber int    `json:"episode_number"`
	}
	if err := c.doGet(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.ID == 0 {
		return nil, errors.New("tmdb episode returned empty id")
	}
	return &EpisodeDetails{
		ID:            resp.ID,
		Name:          resp.Name,
		AirDate:       resp.AirDate,
		SeasonNumber:  resp.SeasonNumber,
		EpisodeNumber: resp.EpisodeNumber,
	}, nil
}

type EpisodeByID struct {
	ID            int
	Name          string
	AirDate       string
	SeasonNumber  int
	EpisodeNumber int
	ShowID        int
}

func (c *Client) GetEpisodeByID(ctx context.Context, episodeID int) (*EpisodeByID, error) {
	path := "/tv/episode/" + strconv.Itoa(episodeID)
	var resp struct {
		ID            int    `json:"id"`
		Name          string `json:"name"`
		AirDate       string `json:"air_date"`
		SeasonNumber  int    `json:"season_number"`
		EpisodeNumber int    `json:"episode_number"`
		ShowID        int    `json:"show_id"`
	}
	if err := c.doGet(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.ID == 0 {
		return nil, errors.New("tmdb episode-by-id returned empty id")
	}
	return &EpisodeByID{
		ID:            resp.ID,
		Name:          resp.Name,
		AirDate:       resp.AirDate,
		SeasonNumber:  resp.SeasonNumber,
		EpisodeNumber: resp.EpisodeNumber,
		ShowID:        resp.ShowID,
	}, nil
}

type TVDetails struct {
	NumberOfSeasons int
}

func (c *Client) GetTVDetails(ctx context.Context, tvID int) (*TVDetails, error) {
	path := "/tv/" + strconv.Itoa(tvID)
	var resp struct {
		NumberOfSeasons int `json:"number_of_seasons"`
	}
	if err := c.doGet(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	if resp.NumberOfSeasons == 0 {
		return nil, errors.New("tmdb tv details returned empty number_of_seasons")
	}
	return &TVDetails{NumberOfSeasons: resp.NumberOfSeasons}, nil
}

type SeasonEpisode struct {
	EpisodeNumber int
	Name          string
	AirDate       string
}

func (c *Client) GetSeasonEpisodes(ctx context.Context, tvID int, season int) ([]SeasonEpisode, error) {
	path := fmt.Sprintf("/tv/%d/season/%d", tvID, season)
	var resp struct {
		Episodes []struct {
			EpisodeNumber int    `json:"episode_number"`
			Name          string `json:"name"`
			AirDate       string `json:"air_date"`
		} `json:"episodes"`
	}
	if err := c.doGet(ctx, path, nil, &resp); err != nil {
		return nil, err
	}
	out := make([]SeasonEpisode, 0, len(resp.Episodes))
	for _, ep := range resp.Episodes {
		out = append(out, SeasonEpisode{
			EpisodeNumber: ep.EpisodeNumber,
			Name:          ep.Name,
			AirDate:       ep.AirDate,
		})
	}
	return out, nil
}

