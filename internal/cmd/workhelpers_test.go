package cmd

import (
	"bytes"
	"strings"
	"testing"
	"text/tabwriter"
)

func TestRequireUUIDFlag(t *testing.T) {
	cases := map[string]string{
		"0f8fad5b-d9cb-469f-a165-70867728950e": "",
		"0F8FAD5B-D9CB-469F-A165-70867728950E": "",
		"ZEN-42":                               `--feature must be a UUID, got "ZEN-42"`,
		"":                                     `--feature must be a UUID, got ""`,
		"0f8fad5b-d9cb-469f-a165-70867728950":  `--feature must be a UUID, got "0f8fad5b-d9cb-469f-a165-70867728950"`,
		"0f8fad5b-d9cb-469f-a165-70867728950e\x1b[31m": `--feature must be a UUID, got "0f8fad5b-d9cb-469f-a165-70867728950e[31m"`,
	}
	for value, want := range cases {
		err := requireUUIDFlag("feature", value)
		if want == "" {
			if err != nil {
				t.Errorf("requireUUIDFlag(%q) = %v, want nil", value, err)
			}
			continue
		}
		if err == nil || err.Error() != want {
			t.Errorf("requireUUIDFlag(%q) = %v, want %s", value, err, want)
		}
	}
}

func TestCanonicalFeatureKey(t *testing.T) {
	cases := []struct {
		ref  string
		want string
		ok   bool
	}{
		{"ZEN-42", "ZEN-42", true},
		{"zen-42", "ZEN-42", true},
		{"Auth2-7", "AUTH2-7", true},
		{"2FA-1", "", false},
		{"ZEN-", "", false},
		{"-42", "", false},
		{"ZEN-4x", "", false},
		{"ZEN_42", "", false},
		{"ZÉN-42", "", false},
		{"ZEN-" + strings.Repeat("1", 40), "", false},
		{"ZEN-9223372036854775807", "ZEN-9223372036854775807", true},
		{"ZEN-9223372036854775808", "", false},
		{"ZEN-0", "", false},
		{"ZEN-042", "", false},
		{"Z-1", "", false},
		{"ZENSU12-1", "", false},
		{"AB1234-5", "AB1234-5", true},
		{"ZEN-4-2", "", false},
		{"0f8fad5b-d9cb-469f-a165-70867728950e", "", false},
	}
	for _, tc := range cases {
		got, ok := canonicalFeatureKey(tc.ref)
		if got != tc.want || ok != tc.ok {
			t.Errorf("canonicalFeatureKey(%q) = %q %v, want %q %v", tc.ref, got, ok, tc.want, tc.ok)
		}
		if looksLikeFeatureKey(tc.ref) != tc.ok {
			t.Errorf("looksLikeFeatureKey(%q) = %v, want %v", tc.ref, !tc.ok, tc.ok)
		}
	}
}

func TestTableRowKeepsColumnsAligned(t *testing.T) {
	var buf bytes.Buffer
	tw := tabwriter.NewWriter(&buf, 0, 4, 2, ' ', 0)
	if _, err := tw.Write([]byte("ID\tTITLE\tSTATE\n")); err != nil {
		t.Fatal(err)
	}
	if _, err := tw.Write([]byte(tableRow("f1", "Retry\twebhooks\x1b[2J", "open") + "\n")); err != nil {
		t.Fatal(err)
	}
	if err := tw.Flush(); err != nil {
		t.Fatal(err)
	}
	lines := strings.Split(strings.TrimRight(buf.String(), "\n"), "\n")
	if len(lines) != 2 {
		t.Fatalf("table = %q", buf.String())
	}
	header, row := lines[0], lines[1]
	if strings.Index(header, "STATE") != strings.Index(row, "open") || strings.Index(header, "TITLE") != strings.Index(row, "Retry") {
		t.Fatalf("columns are not aligned:\n%s\n%s", header, row)
	}
	if !strings.Contains(row, "Retry webhooks[2J") || strings.ContainsRune(row, '\x1b') {
		t.Fatalf("a field was not sanitized on its own: %q", row)
	}
}
