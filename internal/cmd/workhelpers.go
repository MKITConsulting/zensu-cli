package cmd

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
)

var (
	uuidFlagPattern   = regexp.MustCompile(`^[0-9a-fA-F]{8}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{4}-[0-9a-fA-F]{12}$`)
	featureKeyPattern = regexp.MustCompile(`^[A-Z][A-Z0-9]{1,5}$`)
)

func requireUUIDFlag(flag, value string) error {
	if !uuidFlagPattern.MatchString(value) {
		return fmt.Errorf("--%s must be a UUID, got %q", flag, sanitizeTerminal(value))
	}
	return nil
}

func canonicalFeatureKey(ref string) (string, bool) {
	key, number, ok := strings.Cut(ref, "-")
	if !ok || number == "" || number[0] < '1' || number[0] > '9' {
		return "", false
	}
	key = strings.ToUpper(key)
	if !featureKeyPattern.MatchString(key) {
		return "", false
	}
	if _, err := strconv.ParseInt(number, 10, 64); err != nil {
		return "", false
	}
	return key + "-" + number, true
}

func looksLikeFeatureKey(ref string) bool {
	_, ok := canonicalFeatureKey(ref)
	return ok
}

func tableRow(fields ...string) string {
	cells := make([]string, len(fields))
	for i, field := range fields {
		cells[i] = sanitizeTerminal(field)
	}
	return strings.Join(cells, "\t")
}
