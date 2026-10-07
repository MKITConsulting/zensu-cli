package cmd

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/url"
	"strconv"

	"github.com/MKITConsulting/zensu-cli/internal/client"
)

const listPageSize = 100

const maxListPages = 1000

const listReadAttempts = 2

const listJSONUsage = "output all pages as one JSON envelope: data, total, page, perPage"

var errIncompleteList = errors.New("incomplete list")

type mergedList struct {
	Data    []json.RawMessage `json:"data"`
	Total   int               `json:"total"`
	Page    int               `json:"page"`
	PerPage int               `json:"perPage"`
}

func (f *Factory) listAll(ctx context.Context, path string, query url.Values, noun string) ([]byte, error) {
	c, err := f.NewClient(ctx)
	if err != nil {
		return nil, err
	}
	for attempt := 1; ; attempt++ {
		raw, err := readAllPages(ctx, c, path, query, noun)
		if attempt == listReadAttempts || !errors.Is(err, errIncompleteList) {
			return raw, err
		}
	}
}

func readAllPages(ctx context.Context, c *client.Client, path string, query url.Values, noun string) ([]byte, error) {
	items := []json.RawMessage{}
	seen := map[string]bool{}
	total := 0
	for page := 1; ; page++ {
		if page > maxListPages {
			return nil, fmt.Errorf("the %s list did not end after %d pages (%d of %d items received); refusing to print a partial list", noun, maxListPages, len(items), total)
		}
		raw, err := readResponse(c.Do(ctx, http.MethodGet, pagePath(path, query, page), nil))
		if err != nil {
			if page == 1 {
				return nil, err
			}
			return nil, fmt.Errorf("reading page %d of the %s list: %w", page, noun, err)
		}
		pageItems, pageTotal, ok := decodeListPage(raw)
		if !ok {
			if page == 1 {
				return raw, nil
			}
			return nil, fmt.Errorf("page %d of the %s list is not a list response", page, noun)
		}
		if len(pageItems) == 0 {
			break
		}
		total = max(total, pageTotal)
		added := 0
		for _, item := range pageItems {
			if key := listItemKey(item); key != "" {
				if seen[key] {
					continue
				}
				seen[key] = true
			}
			items = append(items, item)
			added++
		}
		if len(items) >= total || added == 0 {
			break
		}
	}
	if len(items) < total {
		return nil, fmt.Errorf("%w: received %d of %d %s because the server's pages overlapped or changed while they were read; run the command again, and if it fails the same way, the server's pages or total are inconsistent", errIncompleteList, len(items), total, noun)
	}
	return encodeMergedList(items)
}

func pagePath(path string, query url.Values, page int) string {
	q := url.Values{}
	for key, values := range query {
		q[key] = values
	}
	q.Set("page", strconv.Itoa(page))
	q.Set("per_page", strconv.Itoa(listPageSize))
	return path + "?" + q.Encode()
}

func decodeListPage(raw []byte) ([]json.RawMessage, int, bool) {
	var page struct {
		Data  json.RawMessage `json:"data"`
		Total int             `json:"total"`
	}
	if json.Unmarshal(raw, &page) != nil || len(page.Data) == 0 {
		return nil, 0, false
	}
	var items []json.RawMessage
	if json.Unmarshal(page.Data, &items) != nil {
		return nil, 0, false
	}
	return items, page.Total, true
}

func listItemKey(item json.RawMessage) string {
	var probe struct {
		ID json.RawMessage `json:"id"`
	}
	if json.Unmarshal(item, &probe) != nil || len(probe.ID) == 0 || string(probe.ID) == "null" {
		return ""
	}
	return string(probe.ID)
}

func encodeMergedList(items []json.RawMessage) ([]byte, error) {
	var buf bytes.Buffer
	enc := json.NewEncoder(&buf)
	enc.SetEscapeHTML(false)
	if err := enc.Encode(mergedList{Data: items, Total: len(items), Page: 1, PerPage: len(items)}); err != nil {
		return nil, err
	}
	return bytes.TrimSuffix(buf.Bytes(), []byte("\n")), nil
}
