// Run with: go run ./review/bench_state
// This uses disposable local fixtures and does not contact external services.
package main

import (
	"fmt"
	"os"
	"sort"
	"time"

	"github.com/tjohnson/maestro/internal/domain"
	"github.com/tjohnson/maestro/internal/state"
)

func main() {
	for _, count := range []int{100, 1000, 10000} {
		if err := measure(count); err != nil {
			fmt.Fprintln(os.Stderr, err)
			os.Exit(1)
		}
	}
}

func measure(count int) error {
	dir, err := os.MkdirTemp("", "maestro-state-bench-")
	if err != nil {
		return err
	}
	defer os.RemoveAll(dir)
	store := state.NewStore(dir)
	snapshot := state.Snapshot{Finished: make(map[string]state.TerminalIssue, count)}
	stamp := time.Date(2026, 10, 2, 0, 0, 0, 0, time.UTC)
	for i := 0; i < count; i++ {
		id := fmt.Sprintf("fixture-%05d", i)
		snapshot.Finished[id] = state.TerminalIssue{IssueID: id, Identifier: id, Status: domain.RunStatusDone, FinishedAt: stamp}
	}
	const iterations = 20
	times := make([]time.Duration, 0, iterations)
	for i := 0; i < iterations; i++ {
		start := time.Now()
		if err := store.Save(snapshot); err != nil {
			return err
		}
		times = append(times, time.Since(start))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	info, err := os.Stat(store.Path())
	if err != nil {
		return err
	}
	fmt.Printf("finished=%d iterations=%d state_bytes=%d p50_ms=%.3f p95_ms=%.3f max_ms=%.3f\n", count, iterations, info.Size(), float64(times[9].Microseconds())/1000, float64(times[18].Microseconds())/1000, float64(times[19].Microseconds())/1000)
	return nil
}
