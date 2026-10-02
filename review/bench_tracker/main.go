// Run with: go run ./review/bench_tracker
// Local GitLab adapter poll fixtures; no external tracker is contacted.
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"sort"
	"strconv"
	"sync"
	"sync/atomic"
	"time"

	"github.com/tjohnson/maestro/internal/config"
	"github.com/tjohnson/maestro/internal/tracker/gitlab"
)

func main() {
	measure(1, 100, 0, 20)
	measure(1, 1000, 0, 20)
	measure(10, 100, 0, 20)
	measure(10, 100, 100*time.Millisecond, 5)
}

func measure(sources, issues int, delay time.Duration, iterations int) {
	var calls atomic.Int64
	pageSize := 100
	pageBody := make([][]byte, (issues+pageSize-1)/pageSize)
	for page := range pageBody {
		items := make([]map[string]any, 0, pageSize)
		for i := page * pageSize; i < issues && i < (page+1)*pageSize; i++ {
			items = append(items, map[string]any{"id": i + 1, "iid": i + 1, "title": "Fixture", "state": "opened", "labels": []string{"agent:ready"}, "references": map[string]any{"full": fmt.Sprintf("team/project#%d", i+1)}})
		}
		pageBody[page], _ = json.Marshal(items)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		calls.Add(1)
		if delay > 0 {
			time.Sleep(delay)
		}
		switch r.URL.Path {
		case "/api/v4/projects/team/project":
			_, _ = w.Write([]byte(`{"id":1,"path_with_namespace":"team/project","http_url_to_repo":"https://example.invalid/team/project.git"}`))
		case "/api/v4/projects/team/project/issues":
			page, _ := strconv.Atoi(r.URL.Query().Get("page"))
			if page < 1 || page > len(pageBody) {
				http.Error(w, "bad page", http.StatusBadRequest)
				return
			}
			if page < len(pageBody) {
				w.Header().Set("X-Next-Page", strconv.Itoa(page+1))
			}
			_, _ = w.Write(pageBody[page-1])
		default:
			http.NotFound(w, r)
		}
	}))
	defer server.Close()
	adapters := make([]*gitlab.Adapter, sources)
	for i := range adapters {
		adapter, err := gitlab.NewAdapter(config.SourceConfig{
			Name: fmt.Sprintf("source-%d", i), Tracker: "gitlab", LabelPrefix: "agent",
			Connection: config.SourceConnection{BaseURL: server.URL, Project: "team/project", Token: "fixture"},
			Filter:     config.FilterConfig{Labels: []string{"agent:ready"}},
		})
		if err != nil {
			panic(err)
		}
		adapters[i] = adapter
	}
	times := make([]time.Duration, 0, iterations)
	for n := 0; n < iterations; n++ {
		start := time.Now()
		var wg sync.WaitGroup
		for _, adapter := range adapters {
			wg.Add(1)
			go func(a *gitlab.Adapter) {
				defer wg.Done()
				got, err := a.Poll(context.Background())
				if err != nil || len(got) != issues {
					panic(fmt.Sprintf("poll: %v count=%d", err, len(got)))
				}
			}(adapter)
		}
		wg.Wait()
		times = append(times, time.Since(start))
	}
	sort.Slice(times, func(i, j int) bool { return times[i] < times[j] })
	p50 := times[(iterations-1)/2]
	p95 := times[(95*iterations+99)/100-1]
	fmt.Printf("sources=%d issues_per_source=%d delay_ms=%d iterations=%d tracker_calls=%d calls_per_tick=%.1f p50_ms=%.2f p95_ms=%.2f\n", sources, issues, delay.Milliseconds(), iterations, calls.Load(), float64(calls.Load())/float64(iterations), float64(p50.Microseconds())/1000, float64(p95.Microseconds())/1000)
}
