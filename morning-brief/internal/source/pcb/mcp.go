// Package pcb is a minimal MCP client (streamable HTTP, JSON-RPC) for proton-calendar-bridge.
package pcb

import (
	"bufio"
	"bytes"
	"context"
	"crypto/tls"
	"crypto/x509"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"strings"
	"sync"
	"time"
)

const (
	protocolVersion = "2025-06-18"
	maxBody         = 8 << 20
)

// Client talks to one pcb MCP endpoint. It initialises its session on the first call.
type Client struct {
	url, token string
	hc         *http.Client

	mu      sync.Mutex
	ready   bool
	session string
	nextID  int
}

// New builds a client. caFile, when set, is the only CA trusted for the server's certificate.
// base supplies the timeout (nil = 10 s); its transport is replaced.
func New(base *http.Client, url, token, caFile string) (*Client, error) {
	tr := &http.Transport{TLSClientConfig: &tls.Config{MinVersion: tls.VersionTLS12}}
	if caFile != "" {
		pem, err := os.ReadFile(caFile)
		if err != nil {
			return nil, fmt.Errorf("pcb CA file %s: not readable", caFile)
		}
		pool := x509.NewCertPool()
		if !pool.AppendCertsFromPEM(pem) {
			return nil, fmt.Errorf("pcb CA file %s: no certificate in it", caFile)
		}
		tr.TLSClientConfig.RootCAs = pool
	}
	hc := &http.Client{Transport: tr, Timeout: 10 * time.Second}
	if base != nil && base.Timeout > 0 {
		hc.Timeout = base.Timeout
	}
	return &Client{url: url, token: token, hc: hc}, nil
}

type rpcRequest struct {
	JSONRPC string `json:"jsonrpc"`
	ID      *int   `json:"id,omitempty"`
	Method  string `json:"method"`
	Params  any    `json:"params,omitempty"`
}

type rpcResponse struct {
	ID     *int            `json:"id"`
	Result json.RawMessage `json:"result"`
	Error  *struct {
		Code    int    `json:"code"`
		Message string `json:"message"`
	} `json:"error"`
}

// Call runs one tool and returns its structured result (or its JSON text content).
func (c *Client) Call(ctx context.Context, tool string, args any) (json.RawMessage, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if !c.ready {
		if _, err := c.rpc(ctx, "initialize", map[string]any{
			"protocolVersion": protocolVersion,
			"capabilities":    map[string]any{},
			"clientInfo":      map[string]any{"name": "morning-brief", "version": "1"},
		}, true); err != nil {
			return nil, fmt.Errorf("pcb initialize: %w", err)
		}
		if _, err := c.rpc(ctx, "notifications/initialized", nil, false); err != nil {
			return nil, fmt.Errorf("pcb initialized: %w", err)
		}
		c.ready = true
	}
	if args == nil {
		args = map[string]any{}
	}
	raw, err := c.rpc(ctx, "tools/call", map[string]any{"name": tool, "arguments": args}, true)
	if err != nil {
		return nil, fmt.Errorf("pcb %s: %w", tool, err)
	}
	var res struct {
		Content []struct {
			Type string `json:"type"`
			Text string `json:"text"`
		} `json:"content"`
		Structured json.RawMessage `json:"structuredContent"`
		IsError    bool            `json:"isError"`
	}
	if err := json.Unmarshal(raw, &res); err != nil {
		return nil, fmt.Errorf("pcb %s: unreadable result", tool)
	}
	text := ""
	for _, ct := range res.Content {
		if ct.Type == "text" {
			text += ct.Text
		}
	}
	if res.IsError {
		return nil, fmt.Errorf("pcb %s refused: %s", tool, text)
	}
	if len(res.Structured) > 0 && string(res.Structured) != "null" {
		return res.Structured, nil
	}
	if json.Valid([]byte(text)) {
		return json.RawMessage(text), nil
	}
	return nil, fmt.Errorf("pcb %s: no structured result", tool)
}

// rpc sends one message. A request (wantReply) returns its result; a notification expects an empty 2xx.
func (c *Client) rpc(ctx context.Context, method string, params any, wantReply bool) (json.RawMessage, error) {
	msg := rpcRequest{JSONRPC: "2.0", Method: method, Params: params}
	if wantReply {
		c.nextID++
		id := c.nextID
		msg.ID = &id
	}
	body, err := json.Marshal(msg)
	if err != nil {
		return nil, err
	}
	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.url, bytes.NewReader(body))
	if err != nil {
		return nil, errors.New("bad pcb URL")
	}
	req.Header.Set("Content-Type", "application/json")
	req.Header.Set("Accept", "application/json, text/event-stream")
	req.Header.Set("Authorization", "Bearer "+c.token)
	if c.session != "" {
		req.Header.Set("Mcp-Session-Id", c.session)
		req.Header.Set("MCP-Protocol-Version", protocolVersion)
	}
	resp, err := c.hc.Do(req)
	if err != nil {
		return nil, fmt.Errorf("request failed: %w", unwrapURL(err))
	}
	defer resp.Body.Close()
	if resp.StatusCode/100 != 2 {
		b, _ := io.ReadAll(io.LimitReader(resp.Body, 512))
		return nil, fmt.Errorf("HTTP %d: %s", resp.StatusCode, strings.TrimSpace(string(b)))
	}
	if s := resp.Header.Get("Mcp-Session-Id"); s != "" && method == "initialize" {
		c.session = s
	}
	if !wantReply {
		_, _ = io.Copy(io.Discard, io.LimitReader(resp.Body, maxBody))
		return nil, nil
	}
	var out rpcResponse
	var found bool
	ct := resp.Header.Get("Content-Type")
	if strings.HasPrefix(ct, "text/event-stream") {
		out, found, err = readSSE(resp.Body, *msg.ID)
	} else {
		b, rerr := io.ReadAll(io.LimitReader(resp.Body, maxBody))
		if rerr != nil {
			return nil, rerr
		}
		if err = json.Unmarshal(b, &out); err == nil {
			found = out.ID != nil && *out.ID == *msg.ID
		}
	}
	if err != nil {
		return nil, fmt.Errorf("unreadable answer: %w", err)
	}
	if !found {
		return nil, errors.New("no answer to the request")
	}
	if out.Error != nil {
		return nil, fmt.Errorf("error %d: %s", out.Error.Code, out.Error.Message)
	}
	return out.Result, nil
}

func readSSE(r io.Reader, id int) (rpcResponse, bool, error) {
	sc := bufio.NewScanner(io.LimitReader(r, maxBody))
	sc.Buffer(make([]byte, 64<<10), maxBody)
	var data []string
	flush := func() (rpcResponse, bool) {
		defer func() { data = nil }()
		if len(data) == 0 {
			return rpcResponse{}, false
		}
		var m rpcResponse
		if json.Unmarshal([]byte(strings.Join(data, "\n")), &m) != nil {
			return rpcResponse{}, false
		}
		return m, m.ID != nil && *m.ID == id
	}
	for sc.Scan() {
		line := sc.Text()
		switch {
		case line == "":
			if m, ok := flush(); ok {
				return m, true, nil
			}
		case strings.HasPrefix(line, "data:"):
			data = append(data, strings.TrimPrefix(strings.TrimPrefix(line, "data:"), " "))
		}
	}
	if m, ok := flush(); ok {
		return m, true, nil
	}
	return rpcResponse{}, false, sc.Err()
}

// unwrapURL drops the URL from a net/http error (it is not a secret, but is noise).
func unwrapURL(err error) error {
	var ue interface{ Unwrap() error }
	if errors.As(err, &ue) && ue.Unwrap() != nil {
		return ue.Unwrap()
	}
	return err
}
