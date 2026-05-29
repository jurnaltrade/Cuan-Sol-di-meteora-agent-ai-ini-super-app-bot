package screening

import (
	"meridian-go-rewrite/internal/config"
	"testing"
)

func TestDiscoverAndScore(t *testing.T) {
	cfg := config.DefaultConfig()
	cfg.DryRun = true
	cfg.Screening.PageSize = 5

	res, err := DiscoverAndScore(cfg)
	if err != nil {
		t.Fatalf("DiscoverAndScore failed: %v", err)
	}

	t.Logf("Successfully fetched %d, screened %d, passed %d candidates!", res.TotalFetched, res.TotalScreened, res.TotalPassed)
	if res.TotalFetched == 0 {
		t.Errorf("Fetched 0 candidates, expected > 0")
	}
}
