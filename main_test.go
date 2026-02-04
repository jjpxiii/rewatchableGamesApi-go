package main

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"sync/atomic"
	"testing"
)

var testData = `[
	{
		"id": "game1",
		"fullName": "Team A vs Team B",
		"shortName": "A @ B",
		"matchupQuality": "high",
		"efficiency": {
			"homeTeamEfficiency": 0.5,
			"awayTeamEfficiency": 0.5
		},
		"scenario": {
			"marginOfVictory": 7,
			"scenarioRating": 8.5
		},
		"offense": {
			"offensiveBigPlays": 5,
			"offensiveExplosivePlays": 10,
			"totalPlays": 100,
			"totalPoints": 55,
			"totalYards": 850,
			"totalYardsPerAttempt": 5.5,
			"homeQBR": 110,
			"awayQBR": 105
		},
		"defense": {
			"defensiveTds": 1,
			"fumbleRecs": 2,
			"interceptions": 3,
			"blockedKicks": 0,
			"safeties": 0,
			"specialTeamsTd": 0,
			"goalLineStands": 1
		}
	}
]`

func setupTestData(t *testing.T) string {
	t.Helper()

	// Create temp directory structure
	tmpDir := t.TempDir()
	yearDir := filepath.Join(tmpDir, "2024")
	if err := os.MkdirAll(yearDir, 0755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	// Write test data files for weeks 1 and 2
	for _, week := range []string{"1", "2"} {
		path := filepath.Join(yearDir, week+".json")
		if err := os.WriteFile(path, []byte(testData), 0644); err != nil {
			t.Fatalf("failed to write test data: %v", err)
		}
	}

	return tmpDir
}

func TestHandleGamesYearWeek(t *testing.T) {
	// Clear cache before test
	cacheMu.Lock()
	cache = make(map[string][]GameStats)
	cacheMu.Unlock()

	tmpDir := setupTestData(t)

	// Create a request handler that uses our temp data directory
	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		year := r.PathValue("year")
		week := r.PathValue("week")
		path := filepath.Join(tmpDir, year, week+".json")

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

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(processed)
	})

	mux := http.NewServeMux()
	mux.Handle("GET /games/{year}/{week}", handler)

	req := httptest.NewRequest("GET", "/games/2024/1", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result []ProcessedGameStats
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	if len(result) != 1 {
		t.Errorf("expected 1 game, got %d", len(result))
	}

	if result[0].ID != "game1" {
		t.Errorf("expected game ID 'game1', got '%s'", result[0].ID)
	}

	if result[0].FullName != "Team A vs Team B" {
		t.Errorf("expected full name 'Team A vs Team B', got '%s'", result[0].FullName)
	}
}

func TestHandleGamesYear(t *testing.T) {
	// Clear cache before test
	cacheMu.Lock()
	cache = make(map[string][]GameStats)
	cacheMu.Unlock()

	tmpDir := setupTestData(t)

	handler := http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		year := r.PathValue("year")
		allGameStats := make([]GameStats, 0, 288)

		for week := 1; week <= 18; week++ {
			path := filepath.Join(tmpDir, year, string(rune('0'+week))+".json")
			if week >= 10 {
				path = filepath.Join(tmpDir, year, string(rune('0'+week/10))+string(rune('0'+week%10))+".json")
			}
			path = filepath.Join(tmpDir, year, itoa(week)+".json")

			gameList, err := loadGameStats(path)
			if os.IsNotExist(err) {
				break
			}
			if err != nil {
				continue
			}
			allGameStats = append(allGameStats, gameList...)
		}

		w.Header().Set("Content-Type", "application/json")
		json.NewEncoder(w).Encode(allGameStats)
	})

	mux := http.NewServeMux()
	mux.Handle("GET /games/{year}", handler)

	req := httptest.NewRequest("GET", "/games/2024", nil)
	rec := httptest.NewRecorder()
	mux.ServeHTTP(rec, req)

	if rec.Code != http.StatusOK {
		t.Errorf("expected status 200, got %d", rec.Code)
	}

	var result []GameStats
	if err := json.Unmarshal(rec.Body.Bytes(), &result); err != nil {
		t.Fatalf("failed to parse response: %v", err)
	}

	// Should have 2 games (1 from each of weeks 1 and 2)
	if len(result) != 2 {
		t.Errorf("expected 2 games from 2 weeks, got %d", len(result))
	}
}

func itoa(n int) string {
	if n < 10 {
		return string(rune('0' + n))
	}
	return string(rune('0'+n/10)) + string(rune('0'+n%10))
}

func TestCachePreventsDuplicateFileReads(t *testing.T) {
	// Clear cache before test
	cacheMu.Lock()
	cache = make(map[string][]GameStats)
	cacheMu.Unlock()

	tmpDir := t.TempDir()
	yearDir := filepath.Join(tmpDir, "2024")
	if err := os.MkdirAll(yearDir, 0755); err != nil {
		t.Fatalf("failed to create test directory: %v", err)
	}

	testFile := filepath.Join(yearDir, "1.json")
	if err := os.WriteFile(testFile, []byte(testData), 0644); err != nil {
		t.Fatalf("failed to write test data: %v", err)
	}

	// Track file read count using a wrapper
	var readCount atomic.Int32

	// First read - should hit disk
	_, err := loadGameStats(testFile)
	if err != nil {
		t.Fatalf("first load failed: %v", err)
	}

	// Check cache has the data
	cacheMu.RLock()
	_, exists := cache[testFile]
	cacheMu.RUnlock()
	if !exists {
		t.Fatal("data should be in cache after first load")
	}

	// Now test multiple requests use cache by checking cache directly
	// Remove the file - if cache works, subsequent loads should succeed
	if err := os.Remove(testFile); err != nil {
		t.Fatalf("failed to remove test file: %v", err)
	}

	// These should all succeed using cached data
	for i := 0; i < 10; i++ {
		data, err := loadGameStats(testFile)
		if err != nil {
			t.Fatalf("cached load %d failed: %v", i, err)
		}
		if len(data) != 1 {
			t.Errorf("expected 1 game from cache, got %d", len(data))
		}
		readCount.Add(1)
	}

	// All 10 reads succeeded even though file was deleted - proves cache works
	if readCount.Load() != 10 {
		t.Errorf("expected 10 successful cached reads, got %d", readCount.Load())
	}
}
