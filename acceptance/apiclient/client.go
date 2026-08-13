// Package apiclient is a tiny GET+JSON helper for checks/bramblegate's
// /api/* checks. Deliberately decodes into local, minimal structs rather
// than importing plugins/mdnsbridge or plugins/clientnames — the acceptance
// module talks to BrambleGate only over the network, never via its Go
// packages (see ../README.md's "Why its own module").
package apiclient

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"strings"
	"time"
)

const DefaultTimeout = 10 * time.Second

// GetJSON issues GET baseURL+path and decodes the JSON body into out.
func GetJSON(ctx context.Context, baseURL, path string, out any) error {
	url := strings.TrimRight(baseURL, "/") + path
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, url, nil)
	if err != nil {
		return fmt.Errorf("build request for %s: %w", url, err)
	}
	client := &http.Client{Timeout: DefaultTimeout}
	resp, err := client.Do(req)
	if err != nil {
		return fmt.Errorf("GET %s: %w", url, err)
	}
	defer resp.Body.Close()
	if resp.StatusCode != http.StatusOK {
		return fmt.Errorf("GET %s: status %s", url, resp.Status)
	}
	if err := json.NewDecoder(resp.Body).Decode(out); err != nil {
		return fmt.Errorf("decode %s: %w", url, err)
	}
	return nil
}
