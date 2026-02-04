package main

import (
	"compress/gzip"
	"fmt"
	"io"
	"log"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"strings"
	"sync"

	jsoniter "github.com/json-iterator/go"
)

var json = jsoniter.ConfigCompatibleWithStandardLibrary

// GameStats mirrors the structure in types.ts and the JSON data
type GameStats struct {
	ID             string `json:"id"`
	Week           int    `json:"week,omitempty"`
	FullName       string `json:"fullName"`
	ShortName      string `json:"shortName"`
	MatchupQuality string `json:"matchupQuality"`
	Efficiency     struct {
		HomeTeamEfficiency          float64 `json:"homeTeamEfficiency"`
		AwayTeamEfficiency          float64 `json:"awayTeamEfficiency"`
		HomeTeamOffensiveEfficiency float64 `json:"homeTeamOffensiveEfficiency"`
		HomeTeamDefensiveEfficiency float64 `json:"homeTeamDefensiveEfficiency"`
		AwayTeamOffensiveEfficiency float64 `json:"awayTeamOffensiveEfficiency"`
		AwayTeamDefensiveEfficiency float64 `json:"awayTeamDefensiveEfficiency"`
		HomeTeamPerformance         float64 `json:"homeTeamPerformance"`
		AwayTeamPerformance         float64 `json:"awayTeamPerformance"`
	} `json:"efficiency"`
	Scenario struct {
		MarginOfVictory             float64 `json:"marginOfVictory"`
		FourthQuarterLeadershipChange float64 `json:"fourthQuarterLeadershipChange"`
		LeadershipChange            float64 `json:"leadershipChange"`
		ScenarioRating              float64 `json:"scenarioRating"`
		ScenarioData                struct {
			MaxWinProbability float64 `json:"maxWinProbability"`
			MinWinProbability float64 `json:"minWinProbability"`
			InversionOfLead   float64 `json:"inversionOfLead"`
			ShareOfLead       float64 `json:"shareOfLead"`
			Max4th            float64 `json:"max_4th"`
			Min4th            float64 `json:"min_4th"`
			Inv4th            float64 `json:"inv_4th"`
			Share4th          float64 `json:"share_4th"`
		} `json:"scenarioData"`
	} `json:"scenario"`
	Offense struct {
		OffensiveBigPlays         float64 `json:"offensiveBigPlays"`
		OffensiveExplosivePlays   float64 `json:"offensiveExplosivePlays"`
		ExplosiveRate             float64 `json:"explosiveRate"`
		TotalPlays                float64 `json:"totalPlays"`
		TotalPoints               float64 `json:"totalPoints"`
		TotalYards                float64 `json:"totalYards"`
		TotalYardsPerAttempt      float64 `json:"totalYardsPerAttempt"`
		TotalPassYards            float64 `json:"totalPassYards"`
		TotalPassYardsPerAttempt  float64 `json:"totalPassYardsPerAttempt"`
		TotalRushYards            float64 `json:"totalRushYards"`
		TotalRushYardsPerAttempt  float64 `json:"totalRushYardsPerAttempt"`
		HomeQBR                   float64 `json:"homeQBR"`
		AwayQBR                   float64 `json:"awayQBR"`
	} `json:"offense"`
	Defense struct {
		Punts            float64 `json:"punts"`
		Sacks            float64 `json:"sacks"`
		Interceptions    float64 `json:"interceptions"`
		DefensiveTds     float64 `json:"defensiveTds"`
		FumbleRecs       float64 `json:"fumbleRecs"`
		BlockedKicks     float64 `json:"blockedKicks"`
		Safeties         float64 `json:"safeties"`
		SpecialTeamsTd   float64 `json:"specialTeamsTd"`
		GoalLineStands   float64 `json:"goalLineStands"`
	} `json:"defense"`
}

// In-memory cache for game stats
var (
	cache   = make(map[string][]GameStats)
	cacheMu sync.RWMutex
)

// loadGameStats loads game stats from cache or disk
func loadGameStats(path string) ([]GameStats, error) {
	cacheMu.RLock()
	if data, ok := cache[path]; ok {
		cacheMu.RUnlock()
		return data, nil
	}
	cacheMu.RUnlock()

	// Check file exists
	if _, err := os.Stat(path); os.IsNotExist(err) {
		return nil, err
	}

	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var gameList []GameStats
	if err := json.Unmarshal(data, &gameList); err != nil {
		return nil, err
	}

	// Store in cache
	cacheMu.Lock()
	cache[path] = gameList
	cacheMu.Unlock()

	return gameList, nil
}

// preloadCache loads all available data files at startup
func preloadCache(dataDir string) {
	years, err := os.ReadDir(dataDir)
	if err != nil {
		log.Printf("Warning: could not read data directory %s: %v", dataDir, err)
		return
	}

	count := 0
	for _, year := range years {
		if !year.IsDir() {
			continue
		}
		yearPath := filepath.Join(dataDir, year.Name())
		weeks, err := os.ReadDir(yearPath)
		if err != nil {
			continue
		}
		for _, week := range weeks {
			if week.IsDir() || !strings.HasSuffix(week.Name(), ".json") {
				continue
			}
			path := filepath.Join(yearPath, week.Name())
			if _, err := loadGameStats(path); err == nil {
				count++
			}
		}
	}
	log.Printf("Preloaded %d data files into cache", count)
}

// ProcessedGameStats is the response structure for /games/:year/:week
type ProcessedGameStats struct {
	ID                string  `json:"id"`
	FullName          string  `json:"fullName"`
	ShortName         string  `json:"shortName"`
	MatchupQuality    string  `json:"matchupQuality"`
	OffensiveRating   float64 `json:"offensiveRating"`
	DefensiveBigPlays float64 `json:"defensiveBigPlays"`
	ScenarioRating    float64 `json:"scenarioRating"`
	TotalRating       float64 `json:"totalRating"`
}

func computeOffensiveRating(gameStats GameStats) float64 {
	// If TotalPlays is 0, we can't calculate rates and likely there's no meaningful stats
	if gameStats.Offense.TotalPlays == 0 {
		return 0
	}

	var offensiveRating float64
	explosiveRate := gameStats.Offense.OffensiveExplosivePlays / gameStats.Offense.TotalPlays
	bigPlayRate := gameStats.Offense.OffensiveBigPlays / gameStats.Offense.TotalPlays

	if explosiveRate > 3 {
		offensiveRating += 1
	}
	if bigPlayRate > 10 {
		offensiveRating += 1
	}

	if gameStats.Offense.TotalPoints > 75 {
		offensiveRating += 3
	} else if gameStats.Offense.TotalPoints > 60 {
		offensiveRating += 2
	} else if gameStats.Offense.TotalPoints > 50 {
		offensiveRating += 1
	}

	if gameStats.Offense.TotalYards > 1000 {
		offensiveRating += 2
	} else if gameStats.Offense.TotalYards > 800 {
		offensiveRating += 1
	}

	if gameStats.Offense.TotalYardsPerAttempt >= 6 {
		offensiveRating += 3
	} else if gameStats.Offense.TotalYardsPerAttempt >= 5 {
		offensiveRating += 1
	}

	if gameStats.Offense.HomeQBR > 120 {
		offensiveRating += 1
	} else if gameStats.Offense.HomeQBR > 100 {
		offensiveRating += 0.5
	}

	if gameStats.Offense.AwayQBR > 120 {
		offensiveRating += 1
	} else if gameStats.Offense.AwayQBR > 100 {
		offensiveRating += 0.5
	}

	return offensiveRating
}

func computeDefensiveBigPlays(gameStats GameStats) float64 {
	return gameStats.Defense.DefensiveTds*3 +
		gameStats.Defense.FumbleRecs +
		gameStats.Defense.SpecialTeamsTd*3 +
		gameStats.Defense.Interceptions +
		gameStats.Defense.BlockedKicks +
		gameStats.Defense.Safeties +
		gameStats.Defense.GoalLineStands
}

func corsMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Origin", "*")
		w.Header().Set("Access-Control-Allow-Methods", "GET, OPTIONS")
		w.Header().Set("Access-Control-Allow-Headers", "Content-Type")

		if r.Method == http.MethodOptions {
			w.WriteHeader(http.StatusNoContent)
			return
		}

		next.ServeHTTP(w, r)
	})
}

// gzipResponseWriter wraps http.ResponseWriter with gzip compression
type gzipResponseWriter struct {
	io.Writer
	http.ResponseWriter
}

func (w gzipResponseWriter) Write(b []byte) (int, error) {
	return w.Writer.Write(b)
}

func gzipMiddleware(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if !strings.Contains(r.Header.Get("Accept-Encoding"), "gzip") {
			next.ServeHTTP(w, r)
			return
		}

		w.Header().Set("Content-Encoding", "gzip")
		gz := gzip.NewWriter(w)
		defer gz.Close()

		next.ServeHTTP(gzipResponseWriter{Writer: gz, ResponseWriter: w}, r)
	})
}

func handleGamesYearWeek(w http.ResponseWriter, r *http.Request) {
	year := r.PathValue("year")
	week := r.PathValue("week")

	path := filepath.Join("data", year, week+".json")

	gameList, err := loadGameStats(path)
	if os.IsNotExist(err) {
		w.WriteHeader(http.StatusNotFound)
		w.Write([]byte("No data"))
		return
	}
	if err != nil {
		http.Error(w, "Error reading data", http.StatusInternalServerError)
		return
	}

	// Pre-allocate slice with exact capacity needed
	processed := make([]ProcessedGameStats, 0, len(gameList))
	for _, g := range gameList {
		offRating := computeOffensiveRating(g)
		defPlays := computeDefensiveBigPlays(g)
		scenRating := g.Scenario.ScenarioRating

		processed = append(processed, ProcessedGameStats{
			ID:                g.ID,
			FullName:          g.FullName,
			ShortName:         g.ShortName,
			MatchupQuality:    g.MatchupQuality,
			OffensiveRating:   offRating,
			DefensiveBigPlays: defPlays,
			ScenarioRating:    scenRating,
			TotalRating:       offRating + defPlays + scenRating,
		})
	}

	// Sort by OffensiveRating descending
	sort.Slice(processed, func(i, j int) bool {
		return processed[i].OffensiveRating > processed[j].OffensiveRating
	})

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(processed); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
	}
}

func handleGamesYear(w http.ResponseWriter, r *http.Request) {
	year := r.PathValue("year")

	// Pre-allocate with estimated capacity (18 weeks * ~16 games)
	allGameStats := make([]GameStats, 0, 288)

	// Iterate from week 1 to 18
	for week := 1; week <= 18; week++ {
		weekStr := strconv.Itoa(week)
		// Fixed: use "data" not "../data"
		path := filepath.Join("data", year, weekStr+".json")

		gameList, err := loadGameStats(path)
		if os.IsNotExist(err) {
			// Stop if a week is missing
			break
		}
		if err != nil {
			continue
		}

		allGameStats = append(allGameStats, gameList...)
	}

	w.Header().Set("Content-Type", "application/json")
	w.Header().Set("Cache-Control", "public, max-age=3600")
	if err := json.NewEncoder(w).Encode(allGameStats); err != nil {
		http.Error(w, "Error encoding response", http.StatusInternalServerError)
	}
}

func main() {
	// Preload all data files into cache at startup
	preloadCache("data")

	mux := http.NewServeMux()
	mux.HandleFunc("GET /games/{year}/{week}", handleGamesYearWeek)
	mux.HandleFunc("GET /games/{year}", handleGamesYear)

	port := "8000"
	if p := os.Getenv("PORT"); p != "" {
		port = p
	}

	// Chain middlewares: CORS -> Gzip -> Handler
	handler := corsMiddleware(gzipMiddleware(mux))

	fmt.Printf("Server listening on :%s\n", port)
	err := http.ListenAndServe(":"+port, handler)
	if err != nil {
		log.Fatal(err)
	}
}
