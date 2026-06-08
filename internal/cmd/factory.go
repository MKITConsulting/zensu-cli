package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"strings"

	"github.com/MKITConsulting/zensu-cli/internal/client"
)

type Factory struct {
	Out       io.Writer
	NewClient func(ctx context.Context) (*client.Client, error)
}

func (f *Factory) request(ctx context.Context, method, path string, body []byte) ([]byte, error) {
	c, err := f.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	resp, err := c.Do(ctx, method, path, body)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()
	raw, _ := io.ReadAll(resp.Body)
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		return nil, apiError(resp.StatusCode, raw)
	}
	return raw, nil
}

func apiError(status int, raw []byte) error {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return fmt.Errorf("%s (status %d)", e.Message, status)
	}
	return fmt.Errorf("request failed (status %d): %s", status, strings.TrimSpace(string(raw)))
}

func printJSON(w io.Writer, raw []byte) error {
	if json.Valid(raw) {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err == nil {
			buf.WriteByte('\n')
			_, err := w.Write(buf.Bytes())
			return err
		}
	}
	_, err := w.Write(append(raw, '\n'))
	return err
}
