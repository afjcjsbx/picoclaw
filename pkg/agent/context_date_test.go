package agent

import (
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/sipeed/picoclaw/pkg/logger"
)

func TestCurrentDateLine(t *testing.T) {
	rome, err := time.LoadLocation("Europe/Rome")
	if err != nil {
		t.Skipf("tzdata unavailable: %v", err)
	}
	t.Setenv("TZ", "Europe/Rome")

	morning := time.Date(2026, 10, 20, 0, 1, 0, 0, rome)
	evening := time.Date(2026, 10, 20, 23, 59, 0, 0, rome)
	want := "Tuesday, October 20, 2026 (Europe/Rome, CEST, UTC+02:00)"

	if got := currentDateLine(morning); got != want {
		t.Errorf("currentDateLine() = %q, want %q", got, want)
	}
	// Same text all day: this is what keeps the prompt cache valid.
	if got := currentDateLine(evening); got != want {
		t.Errorf("currentDateLine(evening) = %q, want %q", got, want)
	}

	winter := time.Date(2026, 1, 5, 12, 0, 0, 0, rome)
	if got, want := currentDateLine(winter), "Monday, January 5, 2026 (Europe/Rome, CET, UTC+01:00)"; got != want {
		t.Errorf("currentDateLine(winter) = %q, want %q", got, want)
	}
}

func TestCurrentDateLine_TZUnsetWarnsOnce(t *testing.T) {
	t.Setenv("TZ", "")
	warnTZUnsetOnce = sync.Once{}

	logFile := t.TempDir() + "/tz-warning.log"
	prevLevel := logger.GetLevel()
	logger.SetLevel(logger.WARN)
	if err := logger.EnableFileLogging(logFile); err != nil {
		t.Fatalf("EnableFileLogging() error = %v", err)
	}
	defer func() {
		logger.DisableFileLogging()
		logger.SetLevel(prevLevel)
	}()

	now := time.Date(2026, 10, 20, 12, 0, 0, 0, time.UTC)
	want := "Tuesday, October 20, 2026 (UTC, UTC+00:00)"
	for i := 0; i < 3; i++ {
		if got := currentDateLine(now); got != want {
			t.Fatalf("currentDateLine() = %q, want %q (no zone name without TZ)", got, want)
		}
	}

	data, err := os.ReadFile(logFile)
	if err != nil {
		t.Fatalf("ReadFile(%q) error = %v", logFile, err)
	}
	if got := strings.Count(string(data), "TZ environment variable is not set"); got != 1 {
		t.Fatalf("TZ warning logged %d times, want 1; log = %q", got, data)
	}
}
