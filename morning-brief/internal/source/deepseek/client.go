// Package deepseek reads the account balance.
package deepseek

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
)

// Client reads the balance with one API key.
type Client struct {
	BaseURL string
	Key     string
	HTTP    *http.Client
}

// Info is one currency's balance; amounts are decimal strings.
type Info struct {
	Currency     string `json:"currency"`
	TotalBalance string `json:"total_balance"`
}

// Balance is the /user/balance answer.
type Balance struct {
	IsAvailable  bool   `json:"is_available"`
	BalanceInfos []Info `json:"balance_infos"`
}

// Balance fetches the balance.
func (c *Client) Balance(ctx context.Context) (Balance, error) {
	var b Balance
	req, err := http.NewRequestWithContext(ctx, http.MethodGet, strings.TrimRight(c.BaseURL, "/")+"/user/balance", nil)
	if err != nil {
		return b, fmt.Errorf("deepseek: bad request")
	}
	req.Header.Set("Authorization", "Bearer "+c.Key)
	req.Header.Set("Accept", "application/json")
	hc := c.HTTP
	if hc == nil {
		hc = http.DefaultClient
	}
	resp, err := hc.Do(req)
	if err != nil {
		if ue, ok := err.(*url.Error); ok {
			err = ue.Err
		}
		return b, fmt.Errorf("deepseek: request failed: %v", err)
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 1<<20))
	if err != nil {
		return b, fmt.Errorf("deepseek: reading answer: %v", err)
	}
	if resp.StatusCode != http.StatusOK {
		return b, fmt.Errorf("deepseek: HTTP %d on /user/balance", resp.StatusCode)
	}
	// is_available is required: without it, an empty object would read as an unavailable account
	var wire struct {
		IsAvailable  *bool  `json:"is_available"`
		BalanceInfos []Info `json:"balance_infos"`
	}
	if err := json.Unmarshal(body, &wire); err != nil || wire.IsAvailable == nil {
		return b, fmt.Errorf("deepseek: bad balance answer")
	}
	b.IsAvailable, b.BalanceInfos = *wire.IsAvailable, wire.BalanceInfos
	return b, nil
}
