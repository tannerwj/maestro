package linear

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"strings"
	"time"
)

const defaultGraphQLEndpoint = "https://api.linear.app/graphql"
const defaultHTTPClientTimeout = 30 * time.Second

type Client struct {
	endpoint   string
	token      string
	httpClient *http.Client
}

func NewClient(baseURL string, token string) (*Client, error) {
	endpoint, err := normalizeEndpoint(baseURL)
	if err != nil {
		return nil, err
	}

	return &Client{
		endpoint:   endpoint,
		token:      token,
		httpClient: &http.Client{Timeout: defaultHTTPClientTimeout},
	}, nil
}

func (c *Client) query(ctx context.Context, query string, variables map[string]any, dst any) error {
	body, err := json.Marshal(map[string]any{
		"query":     query,
		"variables": variables,
	})
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.endpoint, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Authorization", c.token)

	resp, err := c.httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var envelope struct {
		Data   json.RawMessage `json:"data"`
		Errors []struct {
			Message string `json:"message"`
		} `json:"errors"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&envelope); err != nil {
		return err
	}
	if len(envelope.Errors) > 0 {
		return fmt.Errorf("linear graphql: %s", envelope.Errors[0].Message)
	}
	if dst != nil {
		if err := json.Unmarshal(envelope.Data, dst); err != nil {
			return err
		}
	}
	return nil
}

func normalizeEndpoint(raw string) (string, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return defaultGraphQLEndpoint, nil
	}
	parsed, err := url.Parse(raw)
	if err != nil {
		return "", err
	}
	if parsed.Path == "" || parsed.Path == "/" {
		parsed.Path = "/graphql"
	}
	return parsed.String(), nil
}
