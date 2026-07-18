package anilist

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

const (
	defaultBaseURL = "https://graphql.anilist.co"
	defaultTimeout = 15 * time.Second
)

// Client is a lightweight AniList GraphQL client.
// No API key or authentication required for read queries.
type Client struct {
	baseURL string
	http    *http.Client
}

// NewClient creates a new AniList GraphQL client.
func NewClient() *Client {
	return &Client{
		baseURL: defaultBaseURL,
		http: &http.Client{
			Timeout: defaultTimeout,
		},
	}
}

// ---------- value types ----------

// MediaTitle holds title variants returned by AniList.
type MediaTitle struct {
	Romaji  string `json:"romaji"`
	English string `json:"english"`
	Native  string `json:"native"`
}

// ExternalLink represents a link to another service (Crunchyroll, TMDB, etc.).
type ExternalLink struct {
	Site string `json:"site"` // "Crunchyroll", "Funimation", "Hulu"
	URL  string `json:"url"`  // full URL
	Type string `json:"type"` // "STREAMING" or "INFO"
}

// AiringSchedule represents a single airing event.
type AiringSchedule struct {
	ID        int    `json:"id"`
	Episode   int    `json:"episode"`
	AiringAt  int64  `json:"airingAt"`  // Unix timestamp
	AirDate   string // computed from AiringAt
}

// MediaResult is the top-level media object returned by AniList.
type MediaResult struct {
	ID                int              `json:"id"`
	Title             MediaTitle       `json:"title"`
	Description       string           `json:"description"`
	CoverImage        map[string]string `json:"coverImage"` // "large", "extraLarge" URLs
	BannerImage       *string          `json:"bannerImage"`
	Episodes          *int             `json:"episodes"`
	Duration          *int             `json:"duration"` // per-episode in minutes
	Season            *string          `json:"season"`   // WINTER, SPRING, SUMMER, FALL
	SeasonYear        *int             `json:"seasonYear"`
	Status            string           `json:"status"` // FINISHED, RELEASING, NOT_YET_RELEASED, CANCELLED, HIATUS
	Format            string           `json:"format"` // TV, MOVIE, OVA, ONA, SPECIAL, MUSIC
	Genres            []string         `json:"genres"`
	ExternalLinks     []ExternalLink   `json:"externalLinks"`
	NextAiringEpisode *struct {
		Episode  int   `json:"episode"`
		AiringAt int64 `json:"airingAt"`
	} `json:"nextAiringEpisode"`
}

// MediaAiringSchedule holds a single aired episode from AniList.
type MediaAiringSchedule struct {
	Episode  int   `json:"episode"`
	AiringAt int64 `json:"airingAt"`
}

// MediaSearchResult is a lighter result used for search.
type MediaSearchResult struct {
	ID         int              `json:"id"`
	Title      MediaTitle       `json:"title"`
	CoverImage map[string]string `json:"coverImage"`
	Format     string           `json:"format"`
	Episodes   *int             `json:"episodes"`
	Status     string           `json:"status"`
	SeasonYear *int             `json:"seasonYear"`
}

// ---------- GraphQL queries ----------

const mediaQuery = `query ($id: Int) {
	Media(id: $id, type: ANIME) {
		id
		title { romaji english native }
		description
		coverImage { large extraLarge }
		bannerImage
		episodes
		duration
		season
		seasonYear
		status
		format
		genres
		externalLinks { site url type }
		nextAiringEpisode { episode airingAt }
	}
}`

const searchQuery = `query ($search: String, $page: Int) {
	Page(page: $page, perPage: 20) {
		media(search: $search, type: ANIME) {
			id
			title { romaji english }
			coverImage { large }
			format
			episodes
			status
			seasonYear
		}
	}
}`

// ---------- request / response ----------

type graphQLRequest struct {
	Query     string `json:"query"`
	Variables map[string]interface{} `json:"variables"`
}

type graphQLResponse struct {
	Data json.RawMessage `json:"data"`
}

// ---------- public API ----------

// GetMedia fetches a single anime by AniList ID.
// Returns nil, nil when the media does not exist.
func (c *Client) GetMedia(ctx context.Context, id int) (*MediaResult, error) {
	query := `
	query ($id: Int) {
		Media(id: $id, type: ANIME) {
			id
			title { romaji english native }
			description
			coverImage { large extraLarge }
			bannerImage
			episodes
			duration
			season
			seasonYear
			status
			format
			genres
			externalLinks { site url type }
			nextAiringEpisode { episode airingAt }
		}
	}`

	raw, err := c.doGraphQL(ctx, query, map[string]interface{}{"id": id})
	if err != nil {
		return nil, err
	}

	var wrapper struct {
		Media *MediaResult `json:"Media"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("anilist: decode Media: %w", err)
	}

	return wrapper.Media, nil
}

// SearchByTitle searches anime by title on AniList.
// Returns an empty slice on no results (not an error).
func (c *Client) SearchByTitle(ctx context.Context, title string) ([]MediaSearchResult, error) {
	raw, err := c.doGraphQL(ctx, searchQuery, map[string]interface{}{
		"search": title,
		"page":   1,
	})
	if err != nil {
		return nil, err
	}

	var wrapper struct {
		Page struct {
			Media []MediaSearchResult `json:"media"`
		} `json:"Page"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("anilist: decode Page: %w", err)
	}

	return wrapper.Page.Media, nil
}

// GetMediaByExternalURL searches AniList by a streaming/external URL
// (e.g. a Crunchyroll or Netflix link found in another provider's metadata).
// This is useful for reverse-linking when we have a TMDB item with a known
// streaming URL and want to find the AniList equivalent.
func (c *Client) GetMediaByExternalURL(ctx context.Context, url string) (*MediaResult, error) {
	query := `
	query ($url: String) {
		Media(externalSource: EXTERNAL, externalSiteUrl: $url) {
			id
			title { romaji english native }
			episodes
			format
			status
			externalLinks { site url type }
		}
	}`

	raw, err := c.doGraphQL(ctx, query, map[string]interface{}{"url": url})
	if err != nil {
		return nil, err
	}

	var wrapper struct {
		Media *MediaResult `json:"Media"`
	}
	if err := json.Unmarshal(raw, &wrapper); err != nil {
		return nil, fmt.Errorf("anilist: decode Media by URL: %w", err)
	}

	return wrapper.Media, nil
}

// GetAiringSchedule fetches the airing schedule (air dates) for an anime.
func (c *Client) GetAiringSchedule(ctx context.Context, anilistID int) ([]MediaAiringSchedule, error) {
	const scheduleQuery = `
	query ($id: Int, $page: Int) {
		Page(page: $page, perPage: 50) {
			airingSchedules(mediaId: $id, notYetAired: false, sort: EPISODE) {
				episode
				airingAt
			}
		}
	}`
	var allSched []MediaAiringSchedule
	for page := 1; page <= 3; page++ {
		raw, err := c.doGraphQL(ctx, scheduleQuery, map[string]interface{}{"id": anilistID, "page": page})
		if err != nil {
			return allSched, nil // return what we have
		}
		var wrapper struct {
			Page struct {
				Schedules []MediaAiringSchedule `json:"airingSchedules"`
			} `json:"Page"`
		}
		if err := json.Unmarshal(raw, &wrapper); err != nil {
			return allSched, nil
		}
		if len(wrapper.Page.Schedules) == 0 {
			break
		}
		allSched = append(allSched, wrapper.Page.Schedules...)
	}
	return allSched, nil
}

// ---------- internals ----------

func (c *Client) doGraphQL(ctx context.Context, query string, vars map[string]interface{}) (json.RawMessage, error) {
	body := graphQLRequest{Query: query, Variables: vars}
	encoded, err := json.Marshal(body)
	if err != nil {
		return nil, fmt.Errorf("anilist: marshal request: %w", err)
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.baseURL, bytes.NewReader(encoded))
	if err != nil {
		return nil, fmt.Errorf("anilist: create request: %w", err)
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json")

	resp, err := c.http.Do(req)
	if err != nil {
		return nil, fmt.Errorf("anilist: http do: %w", err)
	}
	defer resp.Body.Close()

	raw, err := io.ReadAll(resp.Body)
	if err != nil {
		return nil, fmt.Errorf("anilist: read body: %w", err)
	}

	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		msg := strings.TrimSpace(string(raw))
		if len(msg) > 500 {
			msg = msg[:500]
		}
		return nil, fmt.Errorf("anilist: POST %s: status=%d body=%s", c.baseURL, resp.StatusCode, msg)
	}

	var gqlResp graphQLResponse
	if err := json.Unmarshal(raw, &gqlResp); err != nil {
		return nil, fmt.Errorf("anilist: decode graphql envelope: %w", err)
	}

	if gqlResp.Data == nil || string(gqlResp.Data) == "null" {
		return nil, errors.New("anilist: response data is null (media not found)")
	}

	return gqlResp.Data, nil
}
