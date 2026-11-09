package cmd

import (
	"bytes"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"regexp"
	"strings"

	"github.com/spf13/cobra"
)

var captureSessionPattern = regexp.MustCompile(`^(scv1|awcv1)_[0-9a-f]{64}$`)
var captureDigestPattern = regexp.MustCompile(`^[0-9a-f]{64}$`)

const captureReceiptLimit = 98304

type captureReceiptInfo struct {
	Receipt string `json:"receipt"`
	WorkID  string `json:"workId,omitempty"`
	Reason  string `json:"reason"`
	Pending bool   `json:"pending"`
}

// Recovery is local-only. It neither initializes an API client nor changes any
// archive's contents, attribution, event IDs or human acceptance.
func newAIWorkRecoverCmd(f *Factory) *cobra.Command {
	var session, archive, restore string
	var confirm bool
	cmd := jsonByDefault(&cobra.Command{Use: "recover", Short: "Inspect, archive or restore this session's local capture receipts (offline)", Args: cobra.NoArgs,
		RunE: func(cmd *cobra.Command, _ []string) error {
			if !captureSessionPattern.MatchString(session) {
				return fmt.Errorf("--session must be the exact plugin-provided capture key")
			}
			if (archive != "" && restore != "") || ((archive != "" || restore != "") != confirm) || (cmd.Flags().Changed("archive") && !captureDigestPattern.MatchString(archive)) || (cmd.Flags().Changed("restore") && !captureDigestPattern.MatchString(restore)) {
				return fmt.Errorf("choose --archive <receipt-sha256> or --restore <receipt-sha256> with --confirm; omit all three flags to inspect")
			}
			gitDir, err := aiWorkGit(cmd.Context(), "rev-parse", "--absolute-git-dir")
			if err != nil {
				return err
			}
			dir := filepath.Join(gitDir, "zensu-ai-work-v1")
			if err = captureDirectory(dir, false); err != nil {
				return err
			}
			file := filepath.Join(dir, session+".json")
			archiveDir := filepath.Join(dir, "recovery", session)
			for _, d := range []string{filepath.Join(dir, "recovery"), archiveDir} {
				if err = captureDirectory(d, false); err != nil {
					return err
				}
			}
			status := "not_started"
			if confirm {
				if err = captureDirectory(dir, true); err != nil {
					return err
				}
				lock, err := os.OpenFile(file+".lock", os.O_WRONLY|os.O_CREATE|os.O_EXCL, 0600)
				if err != nil {
					return fmt.Errorf("capture is locked; wait for the active operation or inspect a stale lock")
				}
				defer func() { _ = lock.Close(); _ = os.Remove(file + ".lock") }()
				if archive != "" {
					raw, err := captureRead(file)
					if err != nil {
						return err
					}
					if raw == nil || captureDigest(raw) != archive {
						return fmt.Errorf("active receipt changed or is missing; inspect again")
					}
					for _, d := range []string{filepath.Join(dir, "recovery"), archiveDir} {
						if err = captureDirectory(d, true); err != nil {
							return err
						}
					}
					if err = capturePreserve(filepath.Join(archiveDir, archive+".json"), raw); err != nil {
						return err
					}
					if err = os.Remove(file); err != nil {
						return err
					}
					status = "archived"
				} else {
					if _, err = os.Lstat(file); !os.IsNotExist(err) {
						return fmt.Errorf("active slot is occupied or unreadable; archive it explicitly first")
					}
					raw, err := captureRead(filepath.Join(archiveDir, restore+".json"))
					if err != nil {
						return err
					}
					if raw == nil || captureDigest(raw) != restore {
						return fmt.Errorf("archive is missing or its checksum does not match")
					}
					// Preserve the original archive; only the restored working copy is
					// paused. Existing plugin versions already honor this flag at Stop.
					paused, err := capturePausedCopy(raw)
					if err != nil {
						return err
					}
					if err = capturePublish(file, paused); err != nil {
						return err
					}
					status = "restored"
				}
			}
			raw, err := captureRead(file)
			if err != nil {
				return err
			}
			var active *captureReceiptInfo
			if raw != nil {
				info := captureDescribe(raw)
				active = &info
				if !confirm {
					status = info.Reason
				}
			}
			archives := []captureReceiptInfo{}
			entries, err := os.ReadDir(archiveDir)
			if err != nil && !os.IsNotExist(err) {
				return err
			}
			for _, entry := range entries {
				name := entry.Name()
				digest := strings.TrimSuffix(name, ".json")
				if !strings.HasSuffix(name, ".json") || !captureDigestPattern.MatchString(digest) {
					continue
				}
				data, err := captureRead(filepath.Join(archiveDir, name))
				if err != nil {
					return err
				}
				if data == nil || captureDigest(data) != digest {
					return fmt.Errorf("archive checksum mismatch; all receipt files are retained")
				}
				archives = append(archives, captureDescribe(data))
			}
			return json.NewEncoder(f.Out).Encode(map[string]any{"status": status, "active": active, "archives": archives, "localOnly": true})
		}})
	cmd.Flags().StringVar(&session, "session", "", "exact capture key supplied by the plugin; never another conversation's key (required)")
	cmd.Flags().StringVar(&archive, "archive", "", "archive the active receipt only if its SHA-256 still matches; retains every byte")
	cmd.Flags().StringVar(&restore, "restore", "", "restore a paused working copy into an empty active slot; original archive is retained")
	cmd.Flags().BoolVar(&confirm, "confirm", false, "explicitly authorize this local change; never submits or repairs scope")
	return cmd
}

func captureDigest(raw []byte) string { return fmt.Sprintf("%x", sha256.Sum256(raw)) }
func captureDescribe(raw []byte) captureReceiptInfo {
	info := captureReceiptInfo{Receipt: captureDigest(raw), Reason: "invalid_receipt"}
	var s struct {
		Schema  int             `json:"schema"`
		ID      string          `json:"id"`
		Scope   map[string]any  `json:"scope"`
		Pending json.RawMessage `json:"pending"`
	}
	if json.Unmarshal(raw, &s) != nil || s.Schema != 1 || aiWorkUUID(s.ID) != nil {
		return info
	}
	info.WorkID = s.ID
	info.Pending = len(s.Pending) > 0 && string(s.Pending) != "null"
	switch {
	case s.Scope == nil:
		info.Reason = "legacy_product_scope"
	case s.Scope["taskScope"] == nil || s.Scope["taskScope"] == "":
		info.Reason = "legacy_feature_scope"
	case info.Pending:
		info.Reason = "pending"
	default:
		info.Reason = "ready"
	}
	return info
}
func captureDirectory(dir string, create bool) error {
	st, err := os.Lstat(dir)
	if os.IsNotExist(err) {
		if !create {
			return nil
		}
		return os.Mkdir(dir, 0700)
	}
	if err != nil {
		return err
	}
	if !st.IsDir() || st.Mode()&os.ModeSymlink != 0 {
		return fmt.Errorf("unsafe capture directory")
	}
	return nil
}
func captureRead(file string) ([]byte, error) {
	st, err := os.Lstat(file)
	if os.IsNotExist(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !st.Mode().IsRegular() || st.Size() > captureReceiptLimit {
		return nil, fmt.Errorf("unsafe capture receipt")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return nil, fmt.Errorf("capture receipt changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, captureReceiptLimit+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > captureReceiptLimit {
		return nil, fmt.Errorf("unsafe capture receipt")
	}
	return raw, nil
}
func capturePreserve(file string, raw []byte) error {
	existing, err := captureRead(file)
	if err != nil {
		return err
	}
	if existing != nil {
		if !bytes.Equal(existing, raw) {
			return fmt.Errorf("archive collision; active receipt retained")
		}
		return nil
	}
	return capturePublish(file, raw)
}

// Publish complete, synced bytes without replacing any existing file. A crash
// before the active slot is released leaves either the old receipt or both copies.
func capturePublish(file string, raw []byte) error {
	f, err := os.CreateTemp(filepath.Dir(file), ".receipt-*.tmp")
	if err != nil {
		return err
	}
	defer os.Remove(f.Name())
	defer f.Close()
	if _, err = f.Write(raw); err != nil {
		return err
	}
	if err = f.Sync(); err != nil {
		return err
	}
	if err = f.Close(); err != nil {
		return err
	}
	return os.Link(f.Name(), file)
}

// Keep unknown values as raw JSON to avoid rounding IDs or future numeric fields.
// Malformed/non-object receipts already fail closed in every capture helper.
func capturePausedCopy(raw []byte) ([]byte, error) {
	var receipt map[string]json.RawMessage
	if json.Unmarshal(raw, &receipt) != nil || receipt == nil {
		return raw, nil
	}
	receipt["capturePaused"] = json.RawMessage("true")
	paused, err := json.Marshal(receipt)
	if err != nil {
		return nil, err
	}
	paused = append(paused, '\n')
	if len(paused) > captureReceiptLimit {
		return nil, fmt.Errorf("paused working copy exceeds receipt limit; original archive retained")
	}
	return paused, nil
}
