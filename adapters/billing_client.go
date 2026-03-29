package adapters

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"

	"github.com/giorgim/senior-task-2/contracts"
)

var _ contracts.BillingClient = (*HTTPBillingClient)(nil)

type HTTPBillingClient struct {
	client  *http.Client
	baseURL string
}

func NewHTTPBillingClient(client *http.Client, baseURL string) *HTTPBillingClient {
	return &HTTPBillingClient{client: client, baseURL: baseURL}
}

func (c *HTTPBillingClient) ValidateCustomer(ctx context.Context, customerID string) (bool, error) {
	endpoint := fmt.Sprintf("%s/validate/%s", c.baseURL, url.PathEscape(customerID))

	req, err := http.NewRequestWithContext(ctx, http.MethodGet, endpoint, nil)
	if err != nil {
		return false, fmt.Errorf("building validate request: %w", err)
	}

	resp, err := c.client.Do(req)
	if err != nil {
		return false, fmt.Errorf("calling validate API: %w", err)
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return false, fmt.Errorf("validate API returned status %d", resp.StatusCode)
	}

	var result struct {
		Valid bool `json:"valid"`
	}
	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return false, fmt.Errorf("decoding validate response: %w", err)
	}

	return result.Valid, nil
}
