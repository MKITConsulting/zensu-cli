package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"os"
	"strconv"
	"strings"
	"unicode"
	"unicode/utf16"
	"unicode/utf8"

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
	limit := maxResponseBytes()
	raw, readErr := io.ReadAll(io.LimitReader(resp.Body, limit+1))
	oversized := int64(len(raw)) > limit
	if resp.StatusCode < 200 || resp.StatusCode >= 300 {
		if oversized {
			raw = raw[:limit]
		}
		return nil, apiError(resp.StatusCode, raw)
	}
	if oversized {
		return nil, fmt.Errorf("response body exceeds the %d byte limit", limit)
	}
	if readErr != nil {
		return nil, fmt.Errorf("reading response body: %w", readErr)
	}
	return raw, nil
}

const defaultResponseBytes = 64 << 20

const maxResponseBytesEnv = "ZENSU_MAX_RESPONSE_BYTES"

const responseBytesCeiling = 512 << 20

func maxResponseBytes() int64 {
	if raw := os.Getenv(maxResponseBytesEnv); raw != "" {
		if n, err := strconv.ParseInt(raw, 10, 64); err == nil && n > 0 {
			if n > responseBytesCeiling {
				return responseBytesCeiling
			}
			return n
		}
	}
	return defaultResponseBytes
}

func apiError(status int, raw []byte) error {
	var e struct {
		Code    string `json:"code"`
		Message string `json:"message"`
	}
	if json.Unmarshal(raw, &e) == nil && e.Message != "" {
		return fmt.Errorf("%s (status %d)", sanitizeTerminal(excerpt(e.Message)), status)
	}
	return fmt.Errorf("request failed (status %d): %s", status, sanitizeTerminal(strings.TrimSpace(excerpt(string(raw)))))
}

const maxErrorExcerpt = 4 << 10

func excerpt(s string) string {
	if len(s) <= maxErrorExcerpt {
		return s
	}
	return s[:maxErrorExcerpt] + " ... (truncated)"
}

var invisibleFormatRunes = &unicode.RangeTable{
	R16: []unicode.Range16{
		{Lo: 0x00ad, Hi: 0x00ad, Stride: 1},
		{Lo: 0x061c, Hi: 0x061c, Stride: 1},
		{Lo: 0x200b, Hi: 0x200b, Stride: 1},
		{Lo: 0x200e, Hi: 0x200f, Stride: 1},
		{Lo: 0x202a, Hi: 0x202e, Stride: 1},
		{Lo: 0x2060, Hi: 0x206f, Stride: 1},
		{Lo: 0xfeff, Hi: 0xfeff, Stride: 1},
		{Lo: 0xfff9, Hi: 0xfffb, Stride: 1},
	},
	R32: []unicode.Range32{
		{Lo: 0xe0001, Hi: 0xe0001, Stride: 1},
		{Lo: 0xe0020, Hi: 0xe007f, Stride: 1},
	},
}

func isUnsafeControl(r rune) bool {
	return r < 0x20 || (r >= 0x7f && r <= 0x9f) || unicode.Is(invisibleFormatRunes, r)
}

func sanitizeRunes(s string, keepNewline bool) string {
	return strings.Map(func(r rune) rune {
		if r == '\t' {
			return ' '
		}
		if keepNewline && r == '\n' {
			return r
		}
		if isUnsafeControl(r) {
			return -1
		}
		return r
	}, s)
}

func sanitizeTerminal(s string) string { return sanitizeRunes(s, false) }

func sanitizeMultiline(s string) string { return sanitizeRunes(s, true) }

func sanitizeUploadFileName(s string) string {
	return strings.Map(func(r rune) rune {
		if isUnsafeControl(r) || r == '"' || r == '\\' {
			return -1
		}
		return r
	}, s)
}

func escapeUnsafeJSONRunes(s string) string {
	var b strings.Builder
	b.Grow(len(s))
	for i := 0; i < len(s); {
		r, size := utf8.DecodeRuneInString(s[i:])
		if r == utf8.RuneError && size == 1 {
			b.WriteByte(s[i])
			i++
			continue
		}
		i += size
		if r < 0x20 || !isUnsafeControl(r) {
			b.WriteRune(r)
			continue
		}
		if r > 0xffff {
			hi, lo := utf16.EncodeRune(r)
			fmt.Fprintf(&b, "\\u%04x\\u%04x", hi, lo)
			continue
		}
		fmt.Fprintf(&b, "\\u%04x", r)
	}
	return b.String()
}

func printJSON(w io.Writer, raw []byte) error {
	if json.Valid(raw) {
		var buf bytes.Buffer
		if err := json.Indent(&buf, raw, "", "  "); err == nil {
			if _, err := io.WriteString(w, escapeUnsafeJSONRunes(buf.String())); err != nil {
				return err
			}
			_, err := io.WriteString(w, "\n")
			return err
		}
	}
	if _, err := io.WriteString(w, sanitizeMultiline(string(raw))); err != nil {
		return err
	}
	_, err := io.WriteString(w, "\n")
	return err
}
