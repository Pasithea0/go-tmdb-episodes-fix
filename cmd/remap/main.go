package main

import (
	"bufio"
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"time"

	"github.com/Pasithea0/go-tmdb-episodes-fix/pkg/remap"
)

func main() {
	loadDotEnv(".env")

	var (
		direction  = flag.String("direction", "tmdb2tvdb", "tmdb2tvdb or tvdb2tmdb")
		tmdbID     = flag.Int("tmdb-id", 0, "TMDB series id")
		tvdbID     = flag.Int("tvdb-id", 0, "TVDB series id")
		season     = flag.Int("season", 0, "season number")
		episode    = flag.Int("episode", 0, "episode number")
		tvdbKey    = flag.String("tvdb-key", getenv("TVDB_API_KEY"), "TVDB API key (or TVDB_API_KEY env var)")
		tvdbPin    = flag.String("tvdb-pin", getenv("TVDB_PIN"), "TVDB subscriber pin (or TVDB_PIN env var)")
		tmdbToken  = flag.String("tmdb-token", getenv("TMDB_BEARER_TOKEN"), "TMDB bearer token (or TMDB_BEARER_TOKEN env var)")
		seasonType = flag.String("tvdb-season-type", "default", "TVDB season order (default, official, dvd, absolute, alternate, regional)")
	)

	flag.Parse()

	if *season <= 0 || *episode <= 0 {
		exitErr("season and episode are required and must be > 0")
	}

	mapper := remap.NewMapper(remap.Options{
		TVDBAPIKey:      *tvdbKey,
		TVDBPIN:         *tvdbPin,
		TMDBBearerToken: *tmdbToken,
		TVDBSeasonType:  *seasonType,
	})

	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()

	var out any
	switch *direction {
	case "tmdb2tvdb":
		if *tmdbID == 0 {
			exitErr("tmdb-id is required for tmdb2tvdb")
		}
		res, err := mapper.TmdbToTvdb(ctx, *tmdbID, *season, *episode)
		if err != nil {
			exitErr(err.Error())
		}
		out = res
	case "tvdb2tmdb":
		if *tvdbID == 0 {
			exitErr("tvdb-id is required for tvdb2tmdb")
		}
		res, err := mapper.TvdbToTmdb(ctx, *tvdbID, *season, *episode)
		if err != nil {
			exitErr(err.Error())
		}
		out = res
	default:
		exitErr("unknown direction: " + *direction)
	}

	b, err := json.MarshalIndent(out, "", "  ")
	if err != nil {
		exitErr(err.Error())
	}
	fmt.Println(string(b))
}

func getenv(key string) string {
	v, _ := os.LookupEnv(key)
	return v
}

func loadDotEnv(relativePath string) {
	cwd, err := os.Getwd()
	if err != nil {
		return
	}
	path := filepath.Join(cwd, relativePath)
	f, err := os.Open(path)
	if err != nil {
		return
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		key, val, ok := strings.Cut(line, "=")
		if !ok {
			continue
		}
		key = strings.TrimSpace(key)
		val = strings.TrimSpace(val)
		if key == "" {
			continue
		}

		if (strings.HasPrefix(val, "\"") && strings.HasSuffix(val, "\"")) || (strings.HasPrefix(val, "'") && strings.HasSuffix(val, "'")) {
			val = strings.Trim(val, "\"'")
		}

		if _, exists := os.LookupEnv(key); exists {
			continue
		}
		_ = os.Setenv(key, val)
	}
}

func exitErr(msg string) {
	fmt.Fprintln(os.Stderr, msg)
	os.Exit(1)
}
