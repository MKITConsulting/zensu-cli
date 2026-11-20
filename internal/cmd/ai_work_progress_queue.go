package cmd

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"math"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/safefile"
)

const (
	progressDirName          = "progress"
	progressStateSchema      = 1
	progressSignalWindow     = time.Minute
	progressMaxQueuedFacts   = 500
	progressMaxFactAge       = 24 * time.Hour
	progressIdleStateAge     = 30 * 24 * time.Hour
	progressUnsupportedTTL   = 10 * time.Minute
	progressStateLimit       = 8 << 20
	progressRejectedLogLimit = 20
	progressSessionRefPrefix = "zensu-ai-work-progress-session-v1\n"
	progressSessionRefLength = 24
	progressSeqEpochUnix     = 1735689600
	progressTimeLayout       = "2006-01-02T15:04:05.000000Z"

	progressWaitRateLimited = "rate_limited"
	progressWaitBackoff     = "backoff"
)

var aiWorkNow = time.Now

type progressSignalSlot struct {
	Activity string    `json:"activity,omitempty"`
	At       time.Time `json:"at"`
}

type progressSessionState struct {
	Seq      int64               `json:"seq"`
	SignalAt *time.Time          `json:"signalAt,omitempty"`
	Signal   *progressSignalSlot `json:"signal,omitempty"`
}

type progressRejection struct {
	ClientEventID string    `json:"clientEventId"`
	Kind          string    `json:"kind"`
	Code          string    `json:"code"`
	Field         string    `json:"field,omitempty"`
	At            time.Time `json:"at"`
}

type progressDropped struct {
	Kind  string `json:"kind"`
	Code  string `json:"code"`
	Field string `json:"field,omitempty"`
}

type progressState struct {
	Schema        int                              `json:"schema"`
	WorkID        string                           `json:"workId"`
	APIURL        string                           `json:"apiUrl"`
	Sessions      map[string]*progressSessionState `json:"sessions,omitempty"`
	Batch         []string                         `json:"batch,omitempty"`
	Queue         []string                         `json:"queue,omitempty"`
	NotBefore     *time.Time                       `json:"notBefore,omitempty"`
	WaitReason    string                           `json:"waitReason,omitempty"`
	Failures      int                              `json:"failures,omitempty"`
	UnsupportedAt *time.Time                       `json:"unsupportedAt,omitempty"`
	Full          bool                             `json:"full,omitempty"`
	Discards      int64                            `json:"discards,omitempty"`
	Rejected      []progressRejection              `json:"rejected,omitempty"`
}

type progressSealedFact struct {
	ClientEventID string          `json:"clientEventId"`
	Kind          string          `json:"kind"`
	OccurredAt    string          `json:"occurredAt"`
	SessionRef    string          `json:"sessionRef"`
	Seq           int64           `json:"seq"`
	Payload       json.RawMessage `json:"payload,omitempty"`
}

func progressSessionRef(captureKey string) string {
	sum := sha256.Sum256([]byte(progressSessionRefPrefix + captureKey))
	return hex.EncodeToString(sum[:])[:progressSessionRefLength]
}

func progressEventID(sessionRef, kind string, seq int64) string {
	return sessionRef + ":" + kind + ":" + strconv.FormatInt(seq, 10)
}

func (s *progressState) session(sessionRef string) *progressSessionState {
	if s.Sessions == nil {
		s.Sessions = map[string]*progressSessionState{}
	}
	session := s.Sessions[sessionRef]
	if session == nil {
		session = &progressSessionState{}
		s.Sessions[sessionRef] = session
	}
	return session
}

func (s *progressState) pending() int { return len(s.Batch) + len(s.Queue) }

func (s *progressState) seal(sessionRef string, fact progressFact, at time.Time, now time.Time) error {
	session := s.session(sessionRef)
	seq := now.Unix() - progressSeqEpochUnix
	if seq <= session.Seq {
		seq = session.Seq + 1
	}
	if seq > math.MaxInt32 {
		return errors.New("the progress sequence of this session is exhausted")
	}
	session.Seq = seq
	raw, err := json.Marshal(progressSealedFact{
		ClientEventID: progressEventID(sessionRef, fact.Kind, seq),
		Kind:          fact.Kind,
		OccurredAt:    at.UTC().Format(progressTimeLayout),
		SessionRef:    sessionRef,
		Seq:           seq,
		Payload:       fact.Payload,
	})
	if err != nil {
		return fmt.Errorf("%s: fact cannot be encoded", fact.Kind)
	}
	s.Queue = append(s.Queue, string(raw))
	return nil
}

func (s *progressState) enqueue(sessionRef string, facts []progressFact, now time.Time) error {
	session := s.session(sessionRef)
	for _, fact := range facts {
		if fact.Kind == progressKindSignal {
			session.Signal = &progressSignalSlot{Activity: progressSignalActivity(fact.Payload), At: now}
			continue
		}
		session.Signal = nil
		if err := s.seal(sessionRef, fact, now, now); err != nil {
			return err
		}
	}
	s.dropExpired(now, false)
	if over := s.pending() - progressMaxQueuedFacts; over > 0 {
		s.Queue = slices.Delete(s.Queue, 0, min(over, len(s.Queue)))
	}
	return nil
}

func (s *progressState) sealDueSignals(now time.Time) error {
	refs := make([]string, 0, len(s.Sessions))
	for ref := range s.Sessions {
		refs = append(refs, ref)
	}
	slices.Sort(refs)
	for _, ref := range refs {
		session := s.Sessions[ref]
		slot := session.Signal
		if slot == nil {
			continue
		}
		if age := now.Sub(slot.At); age > progressSignalWindow {
			session.Signal = nil
			continue
		}
		if session.SignalAt != nil {
			if since := now.Sub(*session.SignalAt); since >= 0 && since < progressSignalWindow {
				continue
			}
		}
		fact := progressFact{Kind: progressKindSignal}
		if slot.Activity != "" {
			payload, err := json.Marshal(progressSignal{Activity: slot.Activity})
			if err != nil {
				return err
			}
			fact.Payload = payload
		}
		if err := s.seal(ref, fact, slot.At, now); err != nil {
			return err
		}
		sealedAt := now
		session.SignalAt = &sealedAt
		session.Signal = nil
	}
	return nil
}

func progressFactExpired(raw string, now time.Time) bool {
	var head struct {
		OccurredAt string `json:"occurredAt"`
	}
	if json.Unmarshal([]byte(raw), &head) != nil {
		return true
	}
	at, err := time.Parse(time.RFC3339Nano, head.OccurredAt)
	return err != nil || now.Sub(at) > progressMaxFactAge
}

func (s *progressState) dropExpired(now time.Time, batchToo bool) {
	expired := func(raw string) bool { return progressFactExpired(raw, now) }
	s.Queue = slices.DeleteFunc(s.Queue, expired)
	if batchToo {
		s.Batch = slices.DeleteFunc(s.Batch, expired)
	}
}

func progressBatchBody(batch []string) []byte {
	return []byte(`{"facts":[` + strings.Join(batch, ",") + `]}`)
}

func (s *progressState) formBatch(now time.Time) error {
	s.dropExpired(now, true)
	if len(s.Batch) > 0 {
		return nil
	}
	if err := s.sealDueSignals(now); err != nil {
		return err
	}
	size := len(`{"facts":[]}`)
	count := 0
	for count < len(s.Queue) && count < progressMaxBatchFacts {
		next := size + len(s.Queue[count])
		if count > 0 {
			next++
		}
		if next > progressMaxBatchBytes && count > 0 {
			break
		}
		size = next
		count++
	}
	s.Batch = slices.Clone(s.Queue[:count])
	s.Queue = slices.Delete(s.Queue, 0, count)
	return nil
}

func (s *progressState) acknowledge() {
	s.Batch = nil
	s.Failures = 0
	s.NotBefore = nil
	s.WaitReason = ""
	s.UnsupportedAt = nil
}

func (s *progressState) splitBatch() bool {
	if len(s.Batch) < 2 {
		return false
	}
	half := len(s.Batch) / 2
	s.Queue = append(slices.Clone(s.Batch[half:]), s.Queue...)
	s.Batch = slices.Clone(s.Batch[:half])
	return true
}

func (s *progressState) reject(index int, code, field string, now time.Time) (progressDropped, bool) {
	if index < 0 || index >= len(s.Batch) {
		return progressDropped{}, false
	}
	var head struct {
		ClientEventID string `json:"clientEventId"`
		Kind          string `json:"kind"`
	}
	_ = json.Unmarshal([]byte(s.Batch[index]), &head)
	s.Batch = slices.Delete(s.Batch, index, index+1)
	s.Rejected = append(s.Rejected, progressRejection{ClientEventID: head.ClientEventID, Kind: head.Kind, Code: code, Field: field, At: now})
	if over := len(s.Rejected) - progressRejectedLogLimit; over > 0 {
		s.Rejected = slices.Delete(s.Rejected, 0, over)
	}
	return progressDropped{Kind: head.Kind, Code: code, Field: field}, true
}

func (s *progressState) discard() {
	s.Discards++
	s.Batch = nil
	s.Queue = nil
	s.NotBefore = nil
	s.WaitReason = ""
	s.Failures = 0
	for _, session := range s.Sessions {
		session.Signal = nil
	}
}

func (s *progressState) wait(now time.Time, reason string, delay time.Duration) {
	until := now.Add(delay)
	s.NotBefore = &until
	s.WaitReason = reason
	s.Failures++
}

func progressBackoff(failures int) time.Duration {
	delay := 5 * time.Second
	for i := 1; i < failures && delay < time.Minute; i++ {
		delay *= 2
	}
	return min(delay, time.Minute)
}

func (s *progressState) waiting(now time.Time) (string, int, bool) {
	if s.NotBefore == nil || !now.Before(*s.NotBefore) {
		return "", 0, false
	}
	left := s.NotBefore.Sub(now)
	if left > progressMaxRetryAfter {
		s.NotBefore, s.WaitReason = nil, ""
		return "", 0, false
	}
	return s.WaitReason, max(int(math.Ceil(left.Seconds())), 1), true
}

func (s *progressState) unsupported(now time.Time) bool {
	if s.UnsupportedAt == nil {
		return false
	}
	since := now.Sub(*s.UnsupportedAt)
	return since >= 0 && since < progressUnsupportedTTL
}

func (s *progressState) openSignal(sessionRef string) bool {
	session := s.Sessions[sessionRef]
	return session != nil && session.Signal != nil
}

type progressQueue struct {
	dir    string
	workID string
	apiURL string
}

func newProgressQueue(captureDir, workID, apiURL string) *progressQueue {
	return &progressQueue{dir: filepath.Join(captureDir, progressDirName), workID: workID, apiURL: apiURL}
}

func (q *progressQueue) statePath() string { return filepath.Join(q.dir, q.workID+".json") }

func (q *progressQueue) stateLockPath() string { return filepath.Join(q.dir, q.workID+".lock") }

func (q *progressQueue) sendLockPath() string { return filepath.Join(q.dir, q.workID+".send") }

func (q *progressQueue) prepare() error {
	for _, dir := range []string{filepath.Dir(q.dir), q.dir} {
		if err := captureDirectory(dir, true); err != nil && !os.IsExist(err) {
			return err
		}
		if err := captureDirectory(dir, false); err != nil {
			return err
		}
	}
	return nil
}

func progressReadFile(path string) ([]byte, bool, error) {
	st, err := os.Lstat(path)
	if os.IsNotExist(err) {
		return nil, false, nil
	}
	if err != nil {
		return nil, false, err
	}
	if !st.Mode().IsRegular() {
		return nil, false, errors.New("unsafe progress queue file")
	}
	if st.Size() > progressStateLimit {
		return nil, true, nil
	}
	f, err := os.Open(path)
	if err != nil {
		return nil, false, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(st, opened) {
		return nil, false, errors.New("progress queue file changed while opening")
	}
	raw, err := io.ReadAll(io.LimitReader(f, progressStateLimit+1))
	if err != nil {
		return nil, false, err
	}
	if len(raw) > progressStateLimit {
		return nil, true, nil
	}
	return raw, true, nil
}

func (q *progressQueue) load(now time.Time) (*progressState, error) {
	raw, exists, err := progressReadFile(q.statePath())
	if err != nil {
		return nil, err
	}
	state := &progressState{}
	if raw == nil || json.Unmarshal(raw, state) != nil || state.Schema != progressStateSchema || state.WorkID != q.workID {
		state = &progressState{Schema: progressStateSchema, WorkID: q.workID, APIURL: q.apiURL}
	}
	for ref, session := range state.Sessions {
		if session == nil {
			delete(state.Sessions, ref)
		}
	}
	if state.APIURL != q.apiURL {
		state.discard()
		state.UnsupportedAt = nil
		state.Full = false
		state.APIURL = q.apiURL
	}
	if !exists {
		q.collectIdle(now)
	}
	return state, nil
}

func (q *progressQueue) save(state *progressState) error {
	raw, err := json.Marshal(state)
	if err != nil {
		return err
	}
	tmp, err := os.CreateTemp(q.dir, "."+q.workID+".*.tmp")
	if err != nil {
		return err
	}
	name := tmp.Name()
	defer os.Remove(name)
	if _, err := tmp.Write(append(raw, '\n')); err != nil {
		tmp.Close()
		return err
	}
	if err := tmp.Close(); err != nil {
		return err
	}
	return safefile.Replace(name, q.statePath())
}

func progressLock(ctx context.Context, path string) (func(), error) {
	st, err := os.Lstat(path)
	switch {
	case err == nil && !st.Mode().IsRegular():
		return nil, errors.New("unsafe progress queue file")
	case err != nil && !os.IsNotExist(err):
		return nil, err
	}
	return safefile.Lock(ctx, path)
}

func (q *progressQueue) update(ctx context.Context, change func(*progressState, time.Time) error) error {
	unlock, err := progressLock(ctx, q.stateLockPath())
	if err != nil {
		return err
	}
	defer unlock()
	now := aiWorkNow().UTC()
	state, err := q.load(now)
	if err != nil {
		return err
	}
	if err := change(state, now); err != nil {
		return err
	}
	return q.save(state)
}

func (q *progressQueue) collectIdle(now time.Time) {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return
	}
	for _, entry := range entries {
		name := entry.Name()
		id, isState := strings.CutSuffix(name, ".json")
		if !isState || entry.IsDir() || id == q.workID || aiWorkUUID(id) != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) < progressIdleStateAge {
			continue
		}
		for _, suffix := range []string{".json", ".lock", ".send"} {
			_ = os.Remove(filepath.Join(q.dir, id+suffix))
		}
	}
}

func (q *progressQueue) siblings(now time.Time) []*progressQueue {
	entries, err := os.ReadDir(q.dir)
	if err != nil {
		return nil
	}
	var siblings []*progressQueue
	for _, entry := range entries {
		id, isState := strings.CutSuffix(entry.Name(), ".json")
		if !isState || entry.IsDir() || id == q.workID || aiWorkUUID(id) != nil {
			continue
		}
		info, err := entry.Info()
		if err != nil || !info.Mode().IsRegular() || now.Sub(info.ModTime()) > progressMaxFactAge {
			continue
		}
		raw, _, err := progressReadFile(filepath.Join(q.dir, entry.Name()))
		if err != nil || raw == nil {
			continue
		}
		state := &progressState{}
		if json.Unmarshal(raw, state) != nil || state.Schema != progressStateSchema || state.WorkID != id || state.APIURL != q.apiURL || state.Full || state.pending() == 0 {
			continue
		}
		if _, _, waiting := state.waiting(now); waiting {
			continue
		}
		siblings = append(siblings, &progressQueue{dir: q.dir, workID: id, apiURL: q.apiURL})
	}
	return siblings
}
