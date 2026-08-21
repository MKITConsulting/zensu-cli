package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"strings"
	"unicode"

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
	return readResponse(c.Do(ctx, method, path, body))
}

func (f *Factory) requestWithContentType(ctx context.Context, method, path, contentType string, body []byte) ([]byte, error) {
	c, err := f.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	return readResponse(c.DoWithContentType(ctx, method, path, contentType, body))
}

func readResponse(resp *http.Response, err error) ([]byte, error) {
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
		return fmt.Errorf("%s (status %d)", sanitizeTerminal(e.Message), status)
	}
	return fmt.Errorf("request failed (status %d): %s", status, sanitizeTerminal(strings.TrimSpace(string(raw))))
}

var bidiControls = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x061c, Hi: 0x061c, Stride: 1},
		{Lo: 0x200b, Hi: 0x200b, Stride: 1},
		{Lo: 0x200e, Hi: 0x200f, Stride: 1},
		{Lo: 0x202a, Hi: 0x202e, Stride: 1},
		{Lo: 0x2060, Hi: 0x2064, Stride: 1},
		{Lo: 0x2066, Hi: 0x2069, Stride: 1},
		{Lo: 0xfeff, Hi: 0xfeff, Stride: 1},
		{Lo: 0xfff9, Hi: 0xfffb, Stride: 1},
	},
	R32: []unicode.Range32{
		{Lo: 0xe0001, Hi: 0xe0001, Stride: 1},
		{Lo: 0xe0020, Hi: 0xe007f, Stride: 1},
	},
}

func sanitizeTerminal(s string) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if r < 0x20 || (r >= 0x7f && r <= 0x9f) || unicode.Is(bidiControls, r) {
			return -1
		}
		return r
	}, s)
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
	_, err := w.Write([]byte(sanitizeTerminal(string(raw)) + "\n"))
	return err
}
