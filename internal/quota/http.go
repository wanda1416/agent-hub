package quota

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"time"
)

func requestJSON(ctx context.Context, client *http.Client, method, endpoint string, headers map[string]string, output any) error {
	req, err := http.NewRequestWithContext(ctx, method, endpoint, bytes.NewReader(nil))
	if err != nil {
		return err
	}
	req.Header.Set("Accept", "application/json")
	for key, value := range headers {
		if value != "" {
			req.Header.Set(key, value)
		}
	}
	resp, err := client.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()
	body, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	if err != nil {
		return err
	}
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		message := strings.TrimSpace(string(body))
		if len(message) > 240 {
			message = message[:240]
		}
		return fmt.Errorf("%s %s: HTTP %d: %s", method, endpoint, resp.StatusCode, message)
	}
	if output == nil || len(bytes.TrimSpace(body)) == 0 {
		return nil
	}
	if err := json.Unmarshal(body, output); err != nil {
		return fmt.Errorf("decode %s: %w", endpoint, err)
	}
	return nil
}

func unixSeconds(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	t := time.Unix(value, 0).UTC()
	return &t
}

func unixMilliseconds(value int64) *time.Time {
	if value <= 0 {
		return nil
	}
	t := time.UnixMilli(value).UTC()
	return &t
}

func parseTimeValue(value string) *time.Time {
	value = strings.TrimSpace(value)
	if value == "" {
		return nil
	}
	if t, err := time.Parse(time.RFC3339Nano, value); err == nil {
		t = t.UTC()
		return &t
	}
	return nil
}

func floatPointer(value float64) *float64 {
	return &value
}

func usagePercent(used, remaining *float64) (*float64, *float64) {
	if used == nil && remaining != nil {
		value := 100 - *remaining
		used = &value
	}
	if remaining == nil && used != nil {
		value := 100 - *used
		remaining = &value
	}
	return used, remaining
}

type collection struct {
	pools   []Pool
	credits []CreditBalance
	status  string
	detail  string
	err     error
}
