package limesurvey

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"time"
)

type jsonRPCRequest struct {
	JSONRPC string `json:"jsonrpc"`
	Method  string `json:"method"`
	Params  []any  `json:"params"`
	ID      int    `json:"id"`
}

type jsonRPCResponse struct {
	Result json.RawMessage `json:"result"`
	Error  *jsonRPCError   `json:"error"`
}

type jsonRPCError struct {
	Code    int    `json:"code"`
	Message string `json:"message"`
}

func (c *jsonClient) call(ctx context.Context, method string, params []any, out any) error {
	ctx, cancel := context.WithTimeout(ctx, c.httpTimeout)
	defer cancel()

	payload := jsonRPCRequest{
		JSONRPC: "2.0",
		Method:  method,
		Params:  params,
		ID:      1,
	}
	body, err := json.Marshal(payload)
	if err != nil {
		return err
	}

	req, err := http.NewRequestWithContext(ctx, http.MethodPost, c.remoteControlURL, bytes.NewReader(body))
	if err != nil {
		return err
	}
	req.Header.Set("Content-Type", "application/json")

	httpClient := &http.Client{Timeout: c.httpTimeout + 2*time.Second}
	resp, err := httpClient.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	var rpcResponse jsonRPCResponse
	if err := json.NewDecoder(resp.Body).Decode(&rpcResponse); err != nil {
		return err
	}
	if rpcResponse.Error != nil {
		return fmt.Errorf("json-rpc error %d: %s", rpcResponse.Error.Code, rpcResponse.Error.Message)
	}
	if out == nil {
		return nil
	}
	if err := json.Unmarshal(rpcResponse.Result, out); err != nil {
		var raw any
		if err2 := json.Unmarshal(rpcResponse.Result, &raw); err2 == nil {
			switch dest := out.(type) {
			case *any:
				*dest = raw
				return nil
			}
		}
		return err
	}
	return nil
}

func (c *jsonClient) getSessionKey(ctx context.Context) (string, error) {
	var response any
	if err := c.call(ctx, "get_session_key", []any{c.username, c.password}, &response); err != nil {
		return "", err
	}
	if key := parseSessionKey(response); key != "" {
		return key, nil
	}
	return "", fmt.Errorf("empty JSON-RPC session key")
}

func (c *jsonClient) releaseSessionKey(ctx context.Context, sessionKey string) {
	var ignored any
	_ = c.call(ctx, "release_session_key", []any{sessionKey}, &ignored)
}
