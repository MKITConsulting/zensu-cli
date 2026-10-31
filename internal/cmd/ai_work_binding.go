package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/http"
	"net/url"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/spf13/cobra"
)

type aiWorkBinding struct {
	Schema         int    `json:"schema"`
	APIURL         string `json:"apiUrl"`
	Repository     string `json:"repositoryFingerprint"`
	ProductID      string `json:"productId"`
	ProductName    string `json:"productName"`
	UserID         string `json:"reportingUserId"`
	OrganizationID string `json:"organizationId"`
}
type aiWorkContext struct {
	APIURL         string `json:"apiUrl"`
	Ready          bool   `json:"ready"`
	WorkScope      string `json:"workScope"`
	ProductID      string `json:"productId,omitempty"`
	ProductName    string `json:"productName,omitempty"`
	UserID         string `json:"reportingUserId,omitempty"`
	OrganizationID string `json:"organizationId,omitempty"`
}

func aiWorkClient(f *Factory, cmd *cobra.Command) (*client.Client, error) {
	c, err := f.NewClient(cmd.Context())
	if err != nil {
		return nil, err
	}
	if c.AuthMode() != client.AuthModeLogin {
		return nil, fmt.Errorf("AI work capture requires a personal OAuth login")
	}
	u, err := url.Parse(c.BaseURL)
	if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.RawQuery != "" || u.Fragment != "" {
		return nil, fmt.Errorf("AI work capture requires an HTTP(S) API URL without credentials, query or fragment")
	}
	return c, nil
}
func aiWorkServerContext(cmd *cobra.Command, c *client.Client, product string) (aiWorkContext, error) {
	out := aiWorkContext{APIURL: c.BaseURL, Ready: true, WorkScope: "cross_product"}
	route := "/api/ai-work/capture/context"
	if product != "" {
		if err := aiWorkUUID(product); err != nil {
			return out, err
		}
		route += "?productId=" + product
		out.WorkScope = "product"
	}
	raw, err := readResponse(c.Do(cmd.Context(), http.MethodGet, route, nil))
	if err != nil {
		return out, err
	}
	// Decode only server identity fields; readiness and host are client-owned.
	var identity struct {
		ProductID      string `json:"productId"`
		ProductName    string `json:"productName"`
		UserID         string `json:"reportingUserId"`
		OrganizationID string `json:"organizationId"`
	}
	if json.Unmarshal(raw, &identity) != nil || identity.ProductID != product || aiWorkUUID(identity.UserID) != nil || aiWorkUUID(identity.OrganizationID) != nil {
		return out, fmt.Errorf("capture identity was not confirmed; update the server")
	}
	out.ProductID = identity.ProductID
	out.ProductName = identity.ProductName
	out.UserID = identity.UserID
	out.OrganizationID = identity.OrganizationID
	return out, nil
}
func aiWorkGit(ctx context.Context, args ...string) (string, error) {
	ctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	defer cancel()
	cmd := exec.CommandContext(ctx, "git", args...)
	for _, v := range os.Environ() {
		if !strings.HasPrefix(v, "GIT_") {
			cmd.Env = append(cmd.Env, v)
		}
	}
	raw, err := cmd.Output()
	if err != nil {
		return "", fmt.Errorf("a Git repository with an origin remote is required for a capture binding")
	}
	return strings.TrimSpace(string(raw)), nil
}
func aiWorkBindingLocation(ctx context.Context) (string, string, error) {
	dir, err := aiWorkGit(ctx, "rev-parse", "--path-format=absolute", "--git-common-dir")
	if err != nil {
		return "", "", err
	}
	origin, err := aiWorkGit(ctx, "remote", "get-url", "origin")
	if err != nil || origin == "" {
		return "", "", fmt.Errorf("a repository origin is required to confirm product scope")
	}
	// No repository URL, credentials or local path is persisted or transmitted.
	fingerprint := fmt.Sprintf("sha256:%x", sha256.Sum256([]byte(origin)))
	dir = filepath.Join(dir, "zensu-ai-work-v1")
	if st, e := os.Lstat(dir); e == nil && (!st.IsDir() || st.Mode()&os.ModeSymlink != 0) {
		return "", "", fmt.Errorf("unsafe capture binding directory")
	} else if e != nil && !os.IsNotExist(e) {
		return "", "", e
	}
	return filepath.Join(dir, "product-binding.json"), fingerprint, nil
}
func aiWorkReadBinding(file string) (*aiWorkBinding, error) {
	st, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > 16384 {
		return nil, fmt.Errorf("unsafe capture binding")
	}
	raw, err := os.ReadFile(file)
	if err != nil {
		return nil, err
	}
	var b aiWorkBinding
	if json.Unmarshal(raw, &b) != nil || b.Schema != 1 || aiWorkUUID(b.ProductID) != nil || aiWorkUUID(b.UserID) != nil || aiWorkUUID(b.OrganizationID) != nil {
		return nil, fmt.Errorf("invalid capture binding; explicitly confirm the product again")
	}
	return &b, nil
}
func aiWorkBoundContext(f *Factory, cmd *cobra.Command) (aiWorkContext, error) {
	c, err := aiWorkClient(f, cmd)
	if err != nil {
		return aiWorkContext{}, err
	}
	out := aiWorkContext{APIURL: c.BaseURL, WorkScope: "unassigned"}
	file, fp, err := aiWorkBindingLocation(cmd.Context())
	if err != nil {
		return out, err
	}
	b, err := aiWorkReadBinding(file)
	if err != nil || b == nil {
		return out, err
	}
	if b.APIURL != c.BaseURL || b.Repository != fp {
		return out, fmt.Errorf("capture host or repository changed; explicitly confirm the product again")
	}
	current, err := aiWorkServerContext(cmd, c, b.ProductID)
	if err != nil {
		return out, err
	}
	if current.UserID != b.UserID || current.OrganizationID != b.OrganizationID {
		return out, fmt.Errorf("capture account or organization changed; explicitly confirm the product again")
	}
	return current, nil
}
func newAIWorkContextCmd(f *Factory) *cobra.Command {
	var cross bool
	cmd := jsonByDefault(&cobra.Command{Use: "context", Short: "Revalidate a confirmed repository product; never creates work", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		var out aiWorkContext
		var err error
		if cross {
			var c *client.Client
			c, err = aiWorkClient(f, cmd)
			if err == nil {
				out, err = aiWorkServerContext(cmd, c, "")
			}
		} else {
			out, err = aiWorkBoundContext(f, cmd)
		}
		if err != nil {
			return err
		}
		raw, err := json.Marshal(out)
		if err != nil {
			return err
		}
		return printJSON(f.Out, raw)
	}})
	cmd.Flags().BoolVar(&cross, "cross-product", false, "read the current identity for an explicitly requested cross-product goal")
	return cmd
}
func newAIWorkBindCmd(f *Factory) *cobra.Command {
	var product string
	var confirm bool
	cmd := jsonByDefault(&cobra.Command{Use: "bind", Short: "Explicitly confirm this repository's product for automatic capture", Args: cobra.NoArgs, RunE: func(cmd *cobra.Command, _ []string) error {
		if !confirm {
			return fmt.Errorf("binding requires explicit human product confirmation via --confirm")
		}
		if err := aiWorkUUID(product); err != nil {
			return err
		}
		file, fp, err := aiWorkBindingLocation(cmd.Context())
		if err != nil {
			return err
		}
		if _, err = aiWorkReadBinding(file); err != nil {
			return err
		}
		c, err := aiWorkClient(f, cmd)
		if err != nil {
			return err
		}
		out, err := aiWorkServerContext(cmd, c, product)
		if err != nil {
			return err
		}
		b := aiWorkBinding{1, c.BaseURL, fp, product, out.ProductName, out.UserID, out.OrganizationID}
		raw, err := json.Marshal(b)
		if err != nil {
			return err
		}
		if err = os.MkdirAll(filepath.Dir(file), 0700); err != nil {
			return err
		}
		tmp, err := os.CreateTemp(filepath.Dir(file), "binding-*.tmp")
		if err != nil {
			return err
		}
		defer os.Remove(tmp.Name())
		if _, err = tmp.Write(raw); err != nil {
			tmp.Close()
			return err
		}
		if err = tmp.Close(); err != nil {
			return err
		}
		if err = os.Rename(tmp.Name(), file); err != nil {
			return err
		}
		result, _ := json.Marshal(out)
		return printJSON(f.Out, result)
	}})
	cmd.Flags().StringVar(&product, "product", "", "confirmed product UUID (required)")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "confirm the user's explicit choice of this product")
	return cmd
}
func aiWorkStartScope(f *Factory, cmd *cobra.Command, body map[string]any, product, reason, source string, legacy bool) error {
	if reason != "" {
		if product != "" || body["workOrderId"] != nil || strings.TrimSpace(reason) == "" || !utf8.ValidString(reason) || utf8.RuneCountInString(reason) > 500 || strings.IndexFunc(reason, unicode.IsControl) >= 0 {
			return fmt.Errorf("cross-product work requires one explicit reason and no product or work order")
		}
		body["crossProductReason"] = reason
		return nil
	}
	if product == "" {
		if legacy {
			return nil
		} // Server permits exact retries only; new unassigned goals fail.
		current, err := aiWorkBoundContext(f, cmd)
		if err != nil {
			return err
		}
		if !current.Ready {
			return fmt.Errorf("automatic capture skipped: explicitly confirm a product with ai-work bind, or declare this goal's --cross-product-reason")
		}
		product = current.ProductID
	}
	if err := aiWorkUUID(product); err != nil {
		return err
	}
	if source != "cli" && !legacy {
		current, err := aiWorkBoundContext(f, cmd)
		if err != nil {
			return err
		}
		if !current.Ready || current.ProductID != product {
			return fmt.Errorf("plugin product does not match a confirmed repository binding")
		}
	}
	body["productId"] = product
	return nil
}
