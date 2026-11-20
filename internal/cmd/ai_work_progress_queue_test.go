package cmd

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"runtime"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"
)

const (
	progressTestKey  = "scv1_aaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaaa"
	progressTestRef  = "86434ecd38a4ae5a96ec9f7a"
	progressTestWork = "9e733c18-b6b7-4a18-a17d-4dfd2d15f21a"
	progressTestSeq  = int64(59479200)
)

var progressTestTime = time.Date(2026, 11, 20, 10, 0, 0, 0, time.UTC)

type progressTestClock struct {
	mu  sync.Mutex
	now time.Time
}

func (c *progressTestClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.now
}

func (c *progressTestClock) Advance(d time.Duration) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.now = c.now.Add(d)
}

func useProgressClock(t *testing.T, at time.Time) *progressTestClock {
	t.Helper()
	clock := &progressTestClock{now: at}
	previous := aiWorkNow
	aiWorkNow = clock.Now
	t.Cleanup(func() { aiWorkNow = previous })
	return clock
}

func plainFacts(kinds ...string) []progressFact {
	facts := make([]progressFact, 0, len(kinds))
	for _, kind := range kinds {
		facts = append(facts, progressFact{Kind: kind})
	}
	return facts
}

func signalFact(activity string) progressFact {
	if activity == "" {
		return progressFact{Kind: progressKindSignal}
	}
	return progressFact{Kind: progressKindSignal, Payload: json.RawMessage(`{"activity":"` + activity + `"}`)}
}

func sealedFacts(t *testing.T, raws []string) []progressSealedFact {
	t.Helper()
	facts := make([]progressSealedFact, 0, len(raws))
	for _, raw := range raws {
		var fact progressSealedFact
		if err := json.Unmarshal([]byte(raw), &fact); err != nil {
			t.Fatalf("stored fact %q: %v", raw, err)
		}
		facts = append(facts, fact)
	}
	return facts
}

func TestProgressSessionRef_IsADigestThatNeverShowsTheCaptureKey(t *testing.T) {
	ref := progressSessionRef(progressTestKey)
	if ref != progressTestRef {
		t.Fatalf("sessionRef = %q, want the pinned %q", ref, progressTestRef)
	}
	if strings.Contains(progressTestKey, ref) || len(ref) != 24 || !progressTokenChars(ref, "") {
		t.Errorf("sessionRef %q must be 24 hexadecimal characters that are no part of the capture key", ref)
	}
	if other := progressSessionRef(strings.Replace(progressTestKey, "scv1_a", "scv1_b", 1)); other == ref {
		t.Error("two capture keys share a sessionRef")
	}
	if id := progressEventID(ref, "change_request_linked", 2147483647); len(id) > 64 || id != ref+":change_request_linked:2147483647" {
		t.Errorf("the longest event id %q must fit the contract's 64 characters", id)
	}
}

func TestProgressState_SealsIdsFromSessionKindAndSequence(t *testing.T) {
	if progressTestTime.Unix()-progressSeqEpochUnix != progressTestSeq {
		t.Fatalf("the sequence epoch moved: %d", progressTestTime.Unix()-progressSeqEpochUnix)
	}
	state := &progressState{}
	facts := []progressFact{{Kind: "turn_started"}, {Kind: "step_progress", Payload: json.RawMessage(`{"done":1,"total":3}`)}, {Kind: "turn_ended"}}
	if err := state.enqueue(progressTestRef, facts, progressTestTime); err != nil {
		t.Fatal(err)
	}
	want := []string{
		`{"clientEventId":"86434ecd38a4ae5a96ec9f7a:turn_started:59479200","kind":"turn_started","occurredAt":"2026-11-20T10:00:00.000000Z","sessionRef":"86434ecd38a4ae5a96ec9f7a","seq":59479200}`,
		`{"clientEventId":"86434ecd38a4ae5a96ec9f7a:step_progress:59479201","kind":"step_progress","occurredAt":"2026-11-20T10:00:00.000000Z","sessionRef":"86434ecd38a4ae5a96ec9f7a","seq":59479201,"payload":{"done":1,"total":3}}`,
		`{"clientEventId":"86434ecd38a4ae5a96ec9f7a:turn_ended:59479202","kind":"turn_ended","occurredAt":"2026-11-20T10:00:00.000000Z","sessionRef":"86434ecd38a4ae5a96ec9f7a","seq":59479202}`,
	}
	if !slices.Equal(state.Queue, want) {
		t.Fatalf("sealed facts:\n got %q\nwant %q", state.Queue, want)
	}

	later := progressTestTime.Add(10*time.Second + 123456789*time.Nanosecond)
	if err := state.enqueue(progressTestRef, plainFacts("resumed"), later); err != nil {
		t.Fatal(err)
	}
	sealed := sealedFacts(t, state.Queue)
	if got := sealed[3]; got.Seq != progressTestSeq+10 || got.OccurredAt != "2026-11-20T10:00:10.123456Z" {
		t.Errorf("a later fact takes the clock's sequence and a microsecond time, got seq %d at %s", got.Seq, got.OccurredAt)
	}

	if err := state.enqueue(progressTestRef, plainFacts("turn_started"), progressTestTime.Add(-time.Hour)); err != nil {
		t.Fatal(err)
	}
	if got := sealedFacts(t, state.Queue)[4].Seq; got != progressTestSeq+11 {
		t.Errorf("a clock that went back must not lower the sequence, got %d", got)
	}

	fresh := &progressState{}
	if err := fresh.enqueue(progressTestRef, plainFacts("turn_started"), progressTestTime.Add(time.Minute)); err != nil {
		t.Fatal(err)
	}
	if got := sealedFacts(t, fresh.Queue)[0].Seq; got <= progressTestSeq+11 {
		t.Errorf("a queue file that was lost must not hand out an old sequence again, got %d", got)
	}

	other := progressSessionRef(strings.Replace(progressTestKey, "scv1_a", "scv1_b", 1))
	if err := state.enqueue(other, plainFacts("turn_started"), progressTestTime); err != nil {
		t.Fatal(err)
	}
	if got := sealedFacts(t, state.Queue)[5]; got.SessionRef != other || got.Seq != progressTestSeq {
		t.Errorf("every session counts on its own, got %+v", got)
	}

	exhausted := &progressState{Sessions: map[string]*progressSessionState{progressTestRef: {Seq: 2147483647}}}
	if err := exhausted.enqueue(progressTestRef, plainFacts("turn_started"), progressTestTime); err == nil {
		t.Error("a sequence above the contract's bound must be refused")
	}
}

func TestProgressState_MergesSignalsIntoOnePerMinute(t *testing.T) {
	state := &progressState{}
	at := func(seconds int) time.Time { return progressTestTime.Add(time.Duration(seconds) * time.Second) }
	signal := func(seconds int, activity string) {
		t.Helper()
		if err := state.enqueue(progressTestRef, []progressFact{signalFact(activity)}, at(seconds)); err != nil {
			t.Fatal(err)
		}
	}
	form := func(seconds int) []progressSealedFact {
		t.Helper()
		if err := state.formBatch(at(seconds)); err != nil {
			t.Fatal(err)
		}
		batch := sealedFacts(t, state.Batch)
		state.acknowledge()
		return batch
	}

	signal(0, "editing")
	if len(state.Queue) != 0 || !state.openSignal(progressTestRef) {
		t.Fatalf("a signal waits in its slot and takes no id when it is queued: queue %q", state.Queue)
	}
	first := form(0)
	if len(first) != 1 || first[0].Kind != "signal" || string(first[0].Payload) != `{"activity":"editing"}` || first[0].OccurredAt != "2026-11-20T10:00:00.000000Z" || first[0].Seq != progressTestSeq {
		t.Fatalf("the first signal is sealed when it enters a batch, got %+v", first)
	}

	signal(20, "testing")
	if batch := form(20); len(batch) != 0 || !state.openSignal(progressTestRef) {
		t.Fatalf("a signal inside the minute stays in the slot, got %+v", batch)
	}
	signal(40, "reviewing")
	if batch := form(59); len(batch) != 0 {
		t.Fatalf("59 seconds after the last sealed signal nothing is sent, got %+v", batch)
	}
	signal(70, "committing")
	second := form(70)
	if len(second) != 1 || string(second[0].Payload) != `{"activity":"committing"}` || second[0].OccurredAt != "2026-11-20T10:01:10.000000Z" {
		t.Fatalf("the signal after the minute carries the latest activity and its own time, got %+v", second)
	}
	if second[0].ClientEventID == first[0].ClientEventID {
		t.Error("two sealed signals share an id")
	}

	signal(80, "testing")
	if batch := form(141); len(batch) != 0 || state.openSignal(progressTestRef) {
		t.Fatalf("a slot older than a minute is dropped and never sent, got %+v", batch)
	}

	signal(200, "")
	plain := form(200)
	if len(plain) != 1 || plain[0].Payload != nil {
		t.Fatalf("a signal without an activity carries no payload, got %+v", plain)
	}

	signal(300, "editing")
	if err := state.enqueue(progressTestRef, plainFacts("turn_ended"), at(301)); err != nil {
		t.Fatal(err)
	}
	if batch := form(301); len(batch) != 1 || batch[0].Kind != "turn_ended" || state.openSignal(progressTestRef) {
		t.Fatalf("another fact of the session discards an unsent signal, got %+v", batch)
	}

	other := progressSessionRef(strings.Replace(progressTestKey, "scv1_a", "scv1_b", 1))
	signal(400, "editing")
	if err := state.enqueue(other, []progressFact{signalFact("testing")}, at(400)); err != nil {
		t.Fatal(err)
	}
	if batch := form(400); len(batch) != 2 || batch[0].SessionRef == batch[1].SessionRef {
		t.Fatalf("two sessions each send their own signal, got %+v", batch)
	}

	backwards := &progressState{}
	later := at(500)
	backwards.session(progressTestRef).SignalAt = &later
	if err := backwards.enqueue(progressTestRef, []progressFact{signalFact("idle")}, at(450)); err != nil {
		t.Fatal(err)
	}
	if err := backwards.formBatch(at(450)); err != nil || len(backwards.Batch) != 1 {
		t.Fatalf("a clock that went back must not block signals, batch %q, %v", backwards.Batch, err)
	}
}

func TestProgressState_CollapsesSignalsOfAnOutageIntoOne(t *testing.T) {
	state := &progressState{}
	if err := state.enqueue(progressTestRef, plainFacts("turn_started"), progressTestTime); err != nil {
		t.Fatal(err)
	}
	if err := state.formBatch(progressTestTime); err != nil {
		t.Fatal(err)
	}
	inFlight := slices.Clone(state.Batch)
	for minute := 1; minute <= 5; minute++ {
		now := progressTestTime.Add(time.Duration(minute) * time.Minute)
		if err := state.enqueue(progressTestRef, []progressFact{signalFact("testing")}, now); err != nil {
			t.Fatal(err)
		}
		if err := state.formBatch(now); err != nil {
			t.Fatal(err)
		}
		if !slices.Equal(state.Batch, inFlight) || len(state.Queue) != 0 {
			t.Fatalf("minute %d: the batch in flight stays as it was sent and no signal is sealed behind it: batch %q queue %q", minute, state.Batch, state.Queue)
		}
	}
	state.acknowledge()
	now := progressTestTime.Add(5*time.Minute + 10*time.Second)
	if err := state.formBatch(now); err != nil {
		t.Fatal(err)
	}
	batch := sealedFacts(t, state.Batch)
	if len(batch) != 1 || batch[0].Kind != "signal" || batch[0].OccurredAt != "2026-11-20T10:05:00.000000Z" {
		t.Fatalf("an outage leaves one signal with the time of the latest, got %+v", batch)
	}
}

func TestProgressState_HoldsItsBounds(t *testing.T) {
	state := &progressState{}
	for chunk := 0; chunk < 6; chunk++ {
		if err := state.enqueue(progressTestRef, plainFacts(slices.Repeat([]string{"turn_started"}, 100)...), progressTestTime); err != nil {
			t.Fatal(err)
		}
	}
	queued := sealedFacts(t, state.Queue)
	if len(queued) != 500 || queued[0].Seq != progressTestSeq+100 || queued[499].Seq != progressTestSeq+599 {
		t.Fatalf("the queue keeps the newest 500 facts, got %d from %d to %d", len(queued), queued[0].Seq, queued[len(queued)-1].Seq)
	}

	if err := state.formBatch(progressTestTime); err != nil {
		t.Fatal(err)
	}
	if len(state.Batch) != 100 || len(state.Queue) != 400 {
		t.Fatalf("a batch takes 100 facts, got %d and %d left", len(state.Batch), len(state.Queue))
	}
	inFlight := slices.Clone(state.Batch)
	if err := state.enqueue(progressTestRef, plainFacts(slices.Repeat([]string{"turn_ended"}, 50)...), progressTestTime); err != nil {
		t.Fatal(err)
	}
	if !slices.Equal(state.Batch, inFlight) || state.pending() != 500 || sealedFacts(t, state.Queue)[0].Seq != progressTestSeq+250 {
		t.Fatalf("the cap never touches the batch in flight: batch %d, pending %d", len(state.Batch), state.pending())
	}

	aged := &progressState{}
	if err := aged.enqueue(progressTestRef, plainFacts("turn_started", "turn_ended", "resumed"), progressTestTime); err != nil {
		t.Fatal(err)
	}
	if err := aged.formBatch(progressTestTime); err != nil {
		t.Fatal(err)
	}
	if err := aged.enqueue(progressTestRef, plainFacts("turn_started"), progressTestTime.Add(time.Hour)); err != nil {
		t.Fatal(err)
	}
	dayLater := progressTestTime.Add(24*time.Hour + time.Minute)
	if err := aged.enqueue(progressTestRef, plainFacts("turn_ended"), dayLater); err != nil {
		t.Fatal(err)
	}
	if len(aged.Batch) != 3 || len(aged.Queue) != 2 {
		t.Fatalf("queueing leaves the batch in flight alone: batch %d queue %d", len(aged.Batch), len(aged.Queue))
	}
	if err := aged.formBatch(dayLater); err != nil {
		t.Fatal(err)
	}
	if batch := sealedFacts(t, aged.Batch); len(batch) != 2 || batch[0].Seq != progressTestSeq+3600 {
		t.Fatalf("no fact older than 24 hours is sent, got %+v", batch)
	}
	if !progressFactExpired("not json", dayLater) || !progressFactExpired(`{"occurredAt":"yesterday"}`, dayLater) {
		t.Error("a stored fact that cannot be read counts as expired")
	}
}

func TestProgressState_FormsBatchesInsideTheContractLimits(t *testing.T) {
	state := &progressState{}
	if err := state.enqueue(progressTestRef, plainFacts(slices.Repeat([]string{"turn_started"}, 250)...), progressTestTime); err != nil {
		t.Fatal(err)
	}
	var sizes []int
	var seqs []int64
	for state.pending() > 0 {
		if err := state.formBatch(progressTestTime); err != nil {
			t.Fatal(err)
		}
		sizes = append(sizes, len(state.Batch))
		for _, fact := range sealedFacts(t, state.Batch) {
			seqs = append(seqs, fact.Seq)
		}
		body := progressBatchBody(state.Batch)
		var decoded struct {
			Facts []json.RawMessage `json:"facts"`
		}
		if err := json.Unmarshal(body, &decoded); err != nil || len(decoded.Facts) != len(state.Batch) {
			t.Fatalf("the body is not the batch: %v", err)
		}
		state.acknowledge()
	}
	if !slices.Equal(sizes, []int{100, 100, 50}) || !slices.IsSorted(seqs) || len(seqs) != 250 {
		t.Errorf("batches = %v with %d facts in order %v", sizes, len(seqs), slices.IsSorted(seqs))
	}

	title := strings.Repeat("ä", 120)
	var items []string
	for i := 0; i < 20; i++ {
		items = append(items, `{"severity":"low","state":"open","title":"`+title+`"}`)
	}
	large := progressFact{Kind: "review_round_ended", Payload: json.RawMessage(`{"round":1,"findings":{"open":20,"fixed":0,"blocking":0},"items":[` + strings.Join(items, ",") + `]}`)}
	heavy := &progressState{}
	if err := heavy.enqueue(progressTestRef, slices.Repeat([]progressFact{large}, 80), progressTestTime); err != nil {
		t.Fatal(err)
	}
	total := 0
	for heavy.pending() > 0 {
		if err := heavy.formBatch(progressTestTime); err != nil {
			t.Fatal(err)
		}
		if size := len(progressBatchBody(heavy.Batch)); size > progressMaxBatchBytes || len(heavy.Batch) == 0 {
			t.Fatalf("a batch of %d facts has %d bytes, above the contract's 256 KiB", len(heavy.Batch), size)
		}
		total += len(heavy.Batch)
		heavy.acknowledge()
	}
	if total != 80 {
		t.Errorf("large facts arrive in several batches without a loss, got %d of 80", total)
	}

	oversized := &progressState{Queue: []string{`{"occurredAt":"2026-11-20T10:00:00.000000Z","padding":"` + strings.Repeat("x", progressMaxBatchBytes) + `"}`, `{"occurredAt":"2026-11-20T10:00:00.000000Z"}`}}
	if err := oversized.formBatch(progressTestTime); err != nil || len(oversized.Batch) != 1 || len(oversized.Queue) != 1 {
		t.Errorf("a fact above the limit travels alone so the server can refuse it: batch %d, %v", len(oversized.Batch), err)
	}
}

func TestProgressState_SplitsRejectsAndWaits(t *testing.T) {
	state := &progressState{}
	if err := state.enqueue(progressTestRef, plainFacts("turn_started", "segment_started", "segment_ended", "turn_ended", "resumed"), progressTestTime); err != nil {
		t.Fatal(err)
	}
	if err := state.formBatch(progressTestTime); err != nil {
		t.Fatal(err)
	}
	if err := state.enqueue(progressTestRef, plainFacts("session_ended"), progressTestTime); err != nil {
		t.Fatal(err)
	}
	kinds := func(raws []string) []string {
		var out []string
		for _, fact := range sealedFacts(t, raws) {
			out = append(out, fact.Kind)
		}
		return out
	}
	if !state.splitBatch() || !slices.Equal(kinds(state.Batch), []string{"turn_started", "segment_started"}) || !slices.Equal(kinds(state.Queue), []string{"segment_ended", "turn_ended", "resumed", "session_ended"}) {
		t.Fatalf("a split keeps the order: batch %v queue %v", kinds(state.Batch), kinds(state.Queue))
	}
	dropped, ok := state.reject(1, "unknown_progress_fact_kind", "kind", progressTestTime)
	if !ok || dropped != (progressDropped{Kind: "segment_started", Code: "unknown_progress_fact_kind", Field: "kind"}) || !slices.Equal(kinds(state.Batch), []string{"turn_started"}) {
		t.Fatalf("reject removes exactly the named fact, got %+v and %v", dropped, kinds(state.Batch))
	}
	if state.Rejected[0].ClientEventID != progressEventID(progressTestRef, "segment_started", progressTestSeq+1) {
		t.Errorf("the log names the refused fact, got %+v", state.Rejected[0])
	}
	if _, ok := state.reject(5, "x", "", progressTestTime); ok {
		t.Error("an index outside the batch removes nothing")
	}
	if _, ok := state.reject(-1, "x", "", progressTestTime); ok {
		t.Error("a negative index removes nothing")
	}
	if state.splitBatch() {
		t.Error("a batch of one fact cannot be split")
	}
	for i := 0; i < 30; i++ {
		state.Batch = []string{`{"clientEventId":"id-` + strings.Repeat("x", 8) + `","kind":"signal"}`}
		state.reject(0, "invalid_progress_fact", "payload", progressTestTime)
	}
	if len(state.Rejected) != progressRejectedLogLimit {
		t.Errorf("the log of refused facts keeps %d entries, want %d", len(state.Rejected), progressRejectedLogLimit)
	}

	for failures, want := range map[int]time.Duration{1: 5 * time.Second, 2: 10 * time.Second, 3: 20 * time.Second, 4: 40 * time.Second, 5: time.Minute, 50: time.Minute} {
		if got := progressBackoff(failures); got != want {
			t.Errorf("back-off after %d failures = %v, want %v", failures, got, want)
		}
	}
	state.wait(progressTestTime, progressWaitRateLimited, 90*time.Second)
	if reason, seconds, waiting := state.waiting(progressTestTime.Add(30500 * time.Millisecond)); !waiting || reason != progressWaitRateLimited || seconds != 60 {
		t.Errorf("waiting = %q %d %v", reason, seconds, waiting)
	}
	if _, _, waiting := state.waiting(progressTestTime.Add(90 * time.Second)); waiting {
		t.Error("the wait ends at its time")
	}
	state.acknowledge()
	if state.Failures != 0 || state.NotBefore != nil || state.WaitReason != "" {
		t.Error("an accepted batch clears the wait")
	}
	state.wait(progressTestTime, progressWaitBackoff, 30*time.Second)
	if _, _, waiting := state.waiting(progressTestTime.Add(-59 * time.Minute)); !waiting {
		t.Error("a wait no longer than the longest one the queue stores holds")
	}
	if _, _, waiting := state.waiting(progressTestTime.Add(-time.Hour)); waiting || state.NotBefore != nil || state.WaitReason != "" {
		t.Error("a wait longer than an hour, as after a clock that was set back, is dropped instead of obeyed")
	}

	at := progressTestTime
	state.UnsupportedAt = &at
	if !state.unsupported(progressTestTime.Add(9*time.Minute+59*time.Second)) || state.unsupported(progressTestTime.Add(10*time.Minute)) || state.unsupported(progressTestTime.Add(-time.Second)) {
		t.Error("the unsupported verdict holds for ten minutes and not before its own time")
	}
	state.session(progressTestRef).Signal = &progressSignalSlot{At: progressTestTime}
	state.discard()
	if state.pending() != 0 || state.openSignal(progressTestRef) || state.Sessions[progressTestRef].Seq == 0 || state.UnsupportedAt == nil {
		t.Error("discard drops facts and slots and keeps the counters and the verdict")
	}
}

func newTestQueue(t *testing.T) *progressQueue {
	t.Helper()
	queue := newProgressQueue(filepath.Join(t.TempDir(), "zensu-ai-work-v1"), progressTestWork, "https://zensu.test")
	if err := queue.prepare(); err != nil {
		t.Fatalf("prepare: %v", err)
	}
	return queue
}

func TestProgressQueue_StoresFactsByteForByte(t *testing.T) {
	useProgressClock(t, progressTestTime)
	queue := newTestQueue(t)
	label := `Compare <a> & "b" ` + string(rune(0x1f600)) + ` \ done`
	fact, err := progressFactFromFlags("step_progress", &progressFlagValues{done: 1, total: 2, label: label}, flagsFor(map[string]bool{"done": true, "total": true, "label": true}))
	if err != nil {
		t.Fatal(err)
	}
	var stored []string
	if err := queue.update(context.Background(), func(state *progressState, now time.Time) error {
		if !now.Equal(progressTestTime) || state.WorkID != progressTestWork || state.APIURL != "https://zensu.test" || state.Schema != 1 {
			t.Errorf("a new state names its unit and host at the clock's time: %+v at %v", state, now)
		}
		if err := state.enqueue(progressTestRef, []progressFact{fact, {Kind: "turn_ended"}}, now); err != nil {
			return err
		}
		if err := state.formBatch(now); err != nil {
			return err
		}
		stored = slices.Clone(state.Batch)
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	for round := 0; round < 3; round++ {
		if err := queue.update(context.Background(), func(state *progressState, _ time.Time) error {
			if !slices.Equal(state.Batch, stored) {
				t.Errorf("round %d: the stored batch changed:\n got %q\nwant %q", round, state.Batch, stored)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	}
	var sealed progressSealedFact
	if err := json.Unmarshal([]byte(stored[0]), &sealed); err != nil {
		t.Fatal(err)
	}
	var payload progressStepProgress
	if err := json.Unmarshal(sealed.Payload, &payload); err != nil || payload.Label != label {
		t.Errorf("the label survives sealing and storing: %q, %v", payload.Label, err)
	}

	if runtime.GOOS != "windows" {
		for path, want := range map[string]os.FileMode{queue.statePath(): 0o600, queue.dir: 0o700, filepath.Dir(queue.dir): 0o700} {
			info, err := os.Stat(path)
			if err != nil || info.Mode().Perm() != want {
				t.Errorf("%s: mode %v, want %v (%v)", filepath.Base(path), info.Mode().Perm(), want, err)
			}
		}
	}
	leftovers, _ := filepath.Glob(filepath.Join(queue.dir, ".*.tmp"))
	if len(leftovers) != 0 {
		t.Errorf("temporary files stay behind: %v", leftovers)
	}
	if queue.statePath() != filepath.Join(queue.dir, progressTestWork+".json") || queue.stateLockPath() == queue.sendLockPath() {
		t.Error("the state file is named after the unit and the two locks are separate files")
	}
}

func TestProgressQueue_StartsAfreshWhenTheStateCannotBeUsed(t *testing.T) {
	useProgressClock(t, progressTestTime)
	for name, content := range map[string]string{
		"not JSON":         "{",
		"another schema":   `{"schema":2,"workId":"` + progressTestWork + `","apiUrl":"https://zensu.test","queue":["{}"]}`,
		"another unit":     `{"schema":1,"workId":"891419fa-f7e4-4a52-8123-2bef791876dd","apiUrl":"https://zensu.test","queue":["{}"]}`,
		"an oversize file": strings.Repeat(" ", progressStateLimit+1),
	} {
		queue := newTestQueue(t)
		if err := os.WriteFile(queue.statePath(), []byte(content), 0o600); err != nil {
			t.Fatal(err)
		}
		if err := queue.update(context.Background(), func(state *progressState, now time.Time) error {
			if state.pending() != 0 || state.WorkID != progressTestWork || state.Schema != 1 {
				t.Errorf("%s: the state must start afresh, got %+v", name, state)
			}
			return state.enqueue(progressTestRef, plainFacts("turn_started"), now)
		}); err != nil {
			t.Fatalf("%s: %v", name, err)
		}
		raw, err := os.ReadFile(queue.statePath())
		if err != nil || !strings.Contains(string(raw), `"seq":59479200`) {
			t.Errorf("%s: the fresh state was not stored: %v", name, err)
		}
	}

	switched := newTestQueue(t)
	if err := switched.update(context.Background(), func(state *progressState, now time.Time) error {
		state.Full = true
		state.UnsupportedAt = &now
		state.session(progressTestRef).Signal = &progressSignalSlot{At: now}
		return state.enqueue(progressTestRef, plainFacts("turn_started"), now)
	}); err != nil {
		t.Fatal(err)
	}
	moved := &progressQueue{dir: switched.dir, workID: switched.workID, apiURL: "https://other.test"}
	if err := moved.update(context.Background(), func(state *progressState, _ time.Time) error {
		if state.pending() != 0 || state.Full || state.UnsupportedAt != nil || state.openSignal(progressTestRef) || state.APIURL != "https://other.test" || state.Sessions[progressTestRef].Seq != progressTestSeq {
			t.Errorf("facts queued for one host never reach another, and the counters stay: %+v", state)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	const known = "0123456789abcdef01234567"
	holes := newTestQueue(t)
	if err := os.WriteFile(holes.statePath(), []byte(`{"schema":1,"workId":"`+progressTestWork+`","apiUrl":"https://zensu.test","sessions":{"`+progressTestRef+`":null,"`+known+`":{"seq":7}}}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := holes.update(context.Background(), func(state *progressState, now time.Time) error {
		if len(state.Sessions) != 1 || state.Sessions[known] == nil || state.Sessions[known].Seq != 7 {
			t.Errorf("a session without a record is dropped and the others stay: %+v", state.Sessions)
		}
		state.discard()
		if err := state.formBatch(now); err != nil {
			return err
		}
		return state.enqueue(progressTestRef, plainFacts("turn_started"), now)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestProgressQueue_RefusesUnsafeFilesAndDirectories(t *testing.T) {
	if runtime.GOOS == "windows" {
		t.Skip("symbolic links need a privilege on Windows")
	}
	useProgressClock(t, progressTestTime)
	root := t.TempDir()
	elsewhere := t.TempDir()
	if err := os.Symlink(elsewhere, filepath.Join(root, "zensu-ai-work-v1")); err != nil {
		t.Fatal(err)
	}
	if err := newProgressQueue(filepath.Join(root, "zensu-ai-work-v1"), progressTestWork, "https://zensu.test").prepare(); err == nil {
		t.Error("a capture directory that is a symbolic link must be refused")
	}
	entries, _ := os.ReadDir(elsewhere)
	if len(entries) != 0 {
		t.Errorf("nothing may be created behind the link, found %d entries", len(entries))
	}

	linked := newTestQueue(t)
	target := filepath.Join(t.TempDir(), "target.json")
	if err := os.WriteFile(target, []byte(`{"schema":1}`), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.Symlink(target, linked.statePath()); err != nil {
		t.Fatal(err)
	}
	if err := linked.update(context.Background(), func(*progressState, time.Time) error { return nil }); err == nil || !strings.Contains(err.Error(), "unsafe progress queue file") {
		t.Errorf("a state file that is a symbolic link must be refused, got %v", err)
	}
	if raw, _ := os.ReadFile(target); string(raw) != `{"schema":1}` {
		t.Errorf("the link's target was changed: %q", raw)
	}

	for name, lockPath := range map[string]func(*progressQueue) string{"state lock": (*progressQueue).stateLockPath, "send lock": (*progressQueue).sendLockPath} {
		f := newSendFixture(t, map[string]func(recordedRequest) fakeReply{})
		if err := f.queue.prepare(); err != nil {
			t.Fatal(err)
		}
		outside := filepath.Join(t.TempDir(), "outside")
		if err := os.Symlink(outside, lockPath(f.queue)); err != nil {
			t.Fatal(err)
		}
		if _, err := f.report(progressFact{Kind: "turn_started"}); err == nil || !strings.Contains(err.Error(), "unsafe progress queue file") {
			t.Errorf("a %s that is a symbolic link must be refused, got %v", name, err)
		}
		if _, err := os.Lstat(outside); !os.IsNotExist(err) {
			t.Errorf("a %s that is a symbolic link created a file behind the link: %v", name, err)
		}
		if calls := f.api.summary(); len(calls) != 0 {
			t.Errorf("a %s that is a symbolic link still let a request out: %v", name, calls)
		}
	}

	asDirectory := newTestQueue(t)
	if err := os.Mkdir(asDirectory.stateLockPath(), 0o700); err != nil {
		t.Fatal(err)
	}
	if err := asDirectory.update(context.Background(), func(*progressState, time.Time) error { return nil }); err == nil || !strings.Contains(err.Error(), "unsafe progress queue file") {
		t.Errorf("a lock path that is a directory must be refused, got %v", err)
	}

	progressAsFile := filepath.Join(t.TempDir(), "zensu-ai-work-v1")
	if err := os.MkdirAll(progressAsFile, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(progressAsFile, "progress"), nil, 0o600); err != nil {
		t.Fatal(err)
	}
	if err := newProgressQueue(progressAsFile, progressTestWork, "https://zensu.test").prepare(); err == nil {
		t.Error("a progress path that is a file must be refused")
	}
}

func TestProgressQueue_RemovesTheStateOfIdleUnits(t *testing.T) {
	useProgressClock(t, progressTestTime)
	queue := newTestQueue(t)
	const idle, recent = "11111111-1111-4111-8111-111111111111", "22222222-2222-4222-8222-222222222222"
	write := func(name string, age time.Duration) {
		t.Helper()
		path := filepath.Join(queue.dir, name)
		if err := os.WriteFile(path, []byte("{}"), 0o600); err != nil {
			t.Fatal(err)
		}
		when := progressTestTime.Add(-age)
		if err := os.Chtimes(path, when, when); err != nil {
			t.Fatal(err)
		}
	}
	for _, suffix := range []string{".json", ".lock", ".send"} {
		write(idle+suffix, 31*24*time.Hour)
		write(recent+suffix, 29*24*time.Hour)
	}
	write("notes.json", 400*24*time.Hour)
	if err := queue.update(context.Background(), func(*progressState, time.Time) error { return nil }); err != nil {
		t.Fatal(err)
	}
	var names []string
	entries, err := os.ReadDir(queue.dir)
	if err != nil {
		t.Fatal(err)
	}
	for _, entry := range entries {
		names = append(names, entry.Name())
	}
	want := []string{recent + ".json", recent + ".lock", recent + ".send", progressTestWork + ".json", progressTestWork + ".lock", "notes.json"}
	slices.Sort(want)
	if !slices.Equal(names, want) {
		t.Errorf("after the first load of a new unit the directory holds %v, want %v", names, want)
	}
}

func TestProgressQueue_SerializesConcurrentUpdates(t *testing.T) {
	useProgressClock(t, progressTestTime)
	queue := newTestQueue(t)
	const writers, each = 12, 5
	var group sync.WaitGroup
	for writer := 0; writer < writers; writer++ {
		group.Add(1)
		go func() {
			defer group.Done()
			for i := 0; i < each; i++ {
				if err := queue.update(context.Background(), func(state *progressState, now time.Time) error {
					return state.enqueue(progressTestRef, plainFacts("turn_started"), now)
				}); err != nil {
					t.Errorf("update: %v", err)
				}
			}
		}()
	}
	group.Wait()
	if err := queue.update(context.Background(), func(state *progressState, _ time.Time) error {
		seen := map[int64]bool{}
		for _, fact := range sealedFacts(t, state.Queue) {
			seen[fact.Seq] = true
		}
		if len(state.Queue) != writers*each || len(seen) != writers*each {
			t.Errorf("%d writers stored %d facts with %d distinct sequences, want %d", writers, len(state.Queue), len(seen), writers*each)
		}
		return nil
	}); err != nil {
		t.Fatal(err)
	}

	ctx, cancel := context.WithTimeout(context.Background(), 100*time.Millisecond)
	defer cancel()
	blocked := make(chan error, 1)
	if err := queue.update(context.Background(), func(*progressState, time.Time) error {
		blocked <- queue.update(ctx, func(*progressState, time.Time) error { return nil })
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	if err := <-blocked; err == nil {
		t.Error("an update that cannot get the state lock in time must fail instead of running unlocked")
	}
}
