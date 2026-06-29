package auth

import (
	"encoding/base64"
	"encoding/json"
	"strings"
	"unicode"
)

func IdentityFromToken(token string) (email, org string) {
	parts := strings.Split(token, ".")
	if len(parts) != 3 {
		return "", ""
	}
	payload, err := base64.RawURLEncoding.DecodeString(parts[1])
	if err != nil {
		return "", ""
	}
	var claims struct {
		Email   string `json:"email"`
		OrgName string `json:"orgName"`
	}
	if err := json.Unmarshal(payload, &claims); err != nil {
		return "", ""
	}
	return stripControl(claims.Email), stripControl(claims.OrgName)
}

func stripControl(s string) string {
	return strings.Map(func(r rune) rune {
		if unicode.IsControl(r) {
			return -1
		}
		return r
	}, s)
}
