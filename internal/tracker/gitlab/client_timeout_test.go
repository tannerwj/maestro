package gitlab

import (
	"context"
	"net/http"
	"net/http/httptest"
	"net/url"
	"strings"
	"testing"
	"time"
)

func TestNewClientSetsDefaultTimeout(t *testing.T) {
	client, err := NewClient("https://gitlab.example.com", "token")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	if got, want := client.httpClient.Timeout, defaultHTTPClientTimeout; got != want {
		t.Fatalf("client timeout = %s, want %s", got, want)
	}
}

func TestClientRequestTimesOut(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`[]`))
	}))
	defer server.Close()

	client, err := NewClient(server.URL, "token")
	if err != nil {
		t.Fatalf("new client: %v", err)
	}
	client.httpClient.Timeout = 50 * time.Millisecond

	var payload []map[string]any
	_, err = client.getJSON(context.Background(), "/api/v4/projects/demo/issues", url.Values{}, &payload)
	if err == nil {
		t.Fatal("expected timeout error")
	}
	if !strings.Contains(err.Error(), "Client.Timeout") {
		t.Fatalf("unexpected error: %v", err)
	}
}
