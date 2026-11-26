package cmd

import (
	"context"
	"encoding/json"
	"errors"
	"net"
	"net/http"
	"net/url"
	"regexp"
	"slices"
	"strconv"
	"time"

	"github.com/MKITConsulting/zensu-cli/internal/client"
	"github.com/MKITConsulting/zensu-cli/internal/safefile"
)

const (
	progressStatusSent        = "sent"
	progressStatusQueued      = "queued"
	progressStatusMerged      = "merged"
	progressStatusUnsupported = "unsupported"
	progressStatusSkipped     = "skipped"

	progressReasonNetwork      = "network"
	progressReasonTimeout      = "timeout"
	progressReasonServerError  = "server_error"
	progressReasonBusy         = "busy"
	progressReasonRouteMissing = "route_missing"

	progressCodeUnitFull      = "ai_work_progress_unit_full"
	progressCodeBatchTooLarge = "progress_batch_too_large"
	progressCodeBodyTooLarge  = "request_too_large"
	aiWorkCodeUnsupported     = "unsupported"

	progressDefaultRetryAfter = time.Minute
	progressMaxRetryAfter     = time.Hour
	progressStateBudget       = 2 * time.Second
	progressSiblingLockWait   = 100 * time.Millisecond
)

var progressFieldPattern = regexp.MustCompile(`^[A-Za-z0-9_.\[\]]{1,64}$`)

var errProgressDiscarded = errors.New("the queued facts of this work were discarded by a parallel report: the server refused the work or the API host changed")

type progressReport struct {
	Status            string            `json:"status"`
	WorkID            string            `json:"workId,omitempty"`
	Recorded          int               `json:"recorded"`
	Queued            int               `json:"queued"`
	Reason            string            `json:"reason,omitempty"`
	RetryAfterSeconds int               `json:"retryAfterSeconds,omitempty"`
	Dropped           []progressDropped `json:"dropped,omitempty"`
}

type aiWorkRouteVerdict int

const (
	aiWorkRouteUndecided aiWorkRouteVerdict = iota
	aiWorkRouteMissing
	aiWorkWorkUnavailable
)

func aiWorkProbeRoute(ctx context.Context, c *client.Client, workID string, ownerOnly bool) (aiWorkRouteVerdict, error) {
	raw, err := readResponse(c.Do(ctx, http.MethodGet, aiWorkUnitPath(workID), nil))
	if err != nil {
		if e, ok := apiErrorOf(err); ok && e.status == http.StatusNotFound {
			return aiWorkWorkUnavailable, err
		}
		return aiWorkRouteUndecided, err
	}
	var detail struct {
		ID              string  `json:"id"`
		OwnerUserID     *string `json:"ownerUserId"`
		ReportingUserID string  `json:"reportingUserId"`
	}
	if json.Unmarshal(raw, &detail) != nil || detail.ID != workID {
		return aiWorkRouteUndecided, errors.New("the server's answer for this work could not be read")
	}
	if ownerOnly && (detail.OwnerUserID == nil || detail.ReportingUserID == "" || *detail.OwnerUserID != detail.ReportingUserID) {
		return aiWorkWorkUnavailable, nil
	}
	return aiWorkRouteMissing, nil
}

func progressTransportReason(ctx context.Context, err error) (string, bool) {
	if ctx.Err() != nil || errors.Is(err, context.DeadlineExceeded) {
		return progressReasonTimeout, true
	}
	var urlErr *url.Error
	var netErr net.Error
	switch {
	case errors.As(err, &urlErr):
		if urlErr.Timeout() {
			return progressReasonTimeout, true
		}
		return progressReasonNetwork, true
	case errors.As(err, &netErr):
		return progressReasonNetwork, true
	}
	return "", false
}

func progressRetryAfter(header string) time.Duration {
	seconds, err := strconv.ParseInt(header, 10, 64)
	switch {
	case errors.Is(err, strconv.ErrRange) && seconds > 0:
		return progressMaxRetryAfter
	case err != nil || seconds < 1:
		return progressDefaultRetryAfter
	case seconds > int64(progressMaxRetryAfter/time.Second):
		return progressMaxRetryAfter
	}
	return time.Duration(seconds) * time.Second
}

func progressRefusalDetails(raw []byte) (int, string) {
	var body struct {
		Details struct {
			Index *int   `json:"index"`
			Field string `json:"field"`
		} `json:"details"`
	}
	err := json.Unmarshal(raw, &body)
	field := body.Details.Field
	if !progressFieldPattern.MatchString(field) {
		field = ""
	}
	if err != nil || body.Details.Index == nil {
		return -1, field
	}
	return *body.Details.Index, field
}

type progressAction int

const (
	progressActAccept progressAction = iota
	progressActDropFact
	progressActSplit
	progressActRateLimit
	progressActRetry
	progressActRefuseUnit
	progressActProbe
	progressActUnsupported
	progressActFail
)

type progressDecision struct {
	action progressAction
	code   string
	field  string
	index  int
	reason string
	err    error
}

type progressAnswer struct {
	recorded   int
	retryAfter time.Duration
	unread     bool
	err        error
}

func progressDecide(ctx context.Context, answer progressAnswer, batchLen int) progressDecision {
	if answer.err == nil {
		return progressDecision{action: progressActAccept}
	}
	api, ok := apiErrorOf(answer.err)
	if !ok {
		reason, transient := progressTransportReason(ctx, answer.err)
		switch {
		case transient:
			return progressDecision{action: progressActRetry, reason: reason}
		case answer.unread:
			return progressDecision{action: progressActRetry, reason: progressReasonNetwork}
		}
		return progressDecision{action: progressActFail, err: answer.err}
	}
	code := api.printableCode()
	index, field := progressRefusalDetails(api.raw)
	decision := progressDecision{code: code, field: field, index: index, err: answer.err}
	tooLarge := api.status == http.StatusRequestEntityTooLarge
	refusesFact := api.status == http.StatusBadRequest || api.status == http.StatusConflict || api.status == http.StatusUnprocessableEntity
	switch {
	case api.status == http.StatusUnauthorized:
		decision.action = progressActFail
	case api.status == http.StatusNotFound || api.status == http.StatusMethodNotAllowed:
		decision.action = progressActProbe
	case api.status == http.StatusTooManyRequests:
		decision.action = progressActRateLimit
	case code == "" && (!tooLarge || batchLen < 2):
		decision.action, decision.reason = progressActRetry, progressReasonServerError
	case tooLarge || code == progressCodeBodyTooLarge || code == progressCodeBatchTooLarge:
		decision.action = progressActSplit
	case code == progressCodeUnitFull:
		decision.action = progressActRefuseUnit
	case refusesFact && index >= 0 && index < batchLen:
		decision.action = progressActDropFact
	case api.status == http.StatusBadRequest || api.status == http.StatusUnprocessableEntity:
		decision.action = progressActSplit
	case api.status == http.StatusConflict || api.status == http.StatusRequestTimeout || api.status == http.StatusTooEarly || api.status >= 500:
		decision.action, decision.reason = progressActRetry, progressReasonServerError
	default:
		decision.action = progressActRefuseUnit
	}
	return decision
}

type progressRun struct {
	client         *client.Client
	queue          *progressQueue
	sessionRef     string
	report         progressReport
	slotOpen       bool
	discards       int64
	seen           bool
	foreignDiscard bool
}

func runProgressReport(ctx context.Context, c *client.Client, queue *progressQueue, sessionRef string, facts []progressFact) (progressReport, error) {
	run := &progressRun{client: c, queue: queue, sessionRef: sessionRef, report: progressReport{WorkID: queue.workID}}
	report, err := run.run(ctx, facts)
	if err == nil && (report.Status == progressStatusSent || report.Status == progressStatusMerged) {
		for _, sibling := range queue.siblings(aiWorkNow().UTC()) {
			(&progressRun{client: c, queue: sibling, report: progressReport{WorkID: sibling.workID}}).drain(ctx)
		}
	}
	return report, err
}

func progressUnitFullError() error {
	return &codedError{code: progressCodeUnitFull, message: "this piece of work takes no more progress facts; start a new work goal"}
}

func (r *progressRun) update(change func(*progressState, time.Time) error) error {
	ctx, cancel := context.WithTimeout(context.Background(), progressStateBudget)
	defer cancel()
	return r.queue.update(ctx, func(state *progressState, now time.Time) error {
		if r.seen && state.Discards != r.discards {
			r.foreignDiscard = true
		}
		if err := change(state, now); err != nil {
			return err
		}
		r.discards, r.seen = state.Discards, true
		r.report.Queued = state.pending()
		r.slotOpen = state.openSignal(r.sessionRef)
		return nil
	})
}

func (r *progressRun) unsupported() progressReport {
	r.report.Status, r.report.Reason, r.report.Queued, r.report.RetryAfterSeconds = progressStatusUnsupported, progressReasonRouteMissing, 0, 0
	return r.report
}

func (r *progressRun) closed(state *progressState, now time.Time) (string, error) {
	switch {
	case state.Full:
		return "", progressUnitFullError()
	case state.unsupported(now):
		state.discard()
		return progressStatusUnsupported, nil
	case r.foreignDiscard:
		return "", errProgressDiscarded
	}
	return "", nil
}

func (r *progressRun) settle(reason string) (progressReport, error) {
	stop := reason
	if err := r.update(func(state *progressState, now time.Time) error {
		verdict, err := r.closed(state, now)
		if verdict != "" {
			stop = verdict
		}
		return err
	}); err != nil {
		return r.report, err
	}
	return r.finish(stop), nil
}

func (r *progressRun) held(state *progressState, now time.Time) string {
	reason, seconds, waiting := state.waiting(now)
	if !waiting {
		return ""
	}
	if reason == progressWaitRateLimited {
		r.report.RetryAfterSeconds = seconds
	}
	return reason
}

func (r *progressRun) run(ctx context.Context, facts []progressFact) (progressReport, error) {
	if err := r.queue.prepare(); err != nil {
		return r.report, err
	}
	stop := ""
	if err := r.update(func(state *progressState, now time.Time) error {
		var err error
		if stop, err = r.closed(state, now); err != nil || stop != "" {
			return err
		}
		state.UnsupportedAt = nil
		if err := state.enqueue(r.sessionRef, facts, now); err != nil {
			return err
		}
		stop = r.held(state, now)
		return nil
	}); err != nil {
		return r.report, err
	}
	switch stop {
	case "":
	case progressStatusUnsupported:
		return r.unsupported(), nil
	default:
		r.report.Status, r.report.Reason = progressStatusQueued, stop
		return r.report, nil
	}
	unlock, err := progressLock(ctx, r.queue.sendLockPath())
	if err != nil {
		if errors.Is(err, safefile.ErrLockBusy) {
			return r.settle(progressReasonBusy)
		}
		return r.report, err
	}
	defer unlock()
	return r.flush(ctx)
}

func (r *progressRun) drain(ctx context.Context) {
	if ctx.Err() != nil {
		return
	}
	lockCtx, cancel := context.WithTimeout(ctx, progressSiblingLockWait)
	unlock, err := progressLock(lockCtx, r.queue.sendLockPath())
	cancel()
	if err != nil {
		return
	}
	defer unlock()
	_, _ = r.flush(ctx)
}

func (r *progressRun) post(ctx context.Context, batch []string) progressAnswer {
	resp, err := r.client.Do(ctx, http.MethodPost, aiWorkUnitPath(r.queue.workID)+"/progress", progressBatchBody(batch))
	if err != nil {
		return progressAnswer{err: err}
	}
	retryAfter := progressRetryAfter(resp.Header.Get("Retry-After"))
	raw, err := readResponse(resp, nil)
	if err != nil {
		_, refused := apiErrorOf(err)
		return progressAnswer{err: err, retryAfter: retryAfter, unread: !refused}
	}
	var receipt struct {
		Recorded *int `json:"recorded"`
	}
	if json.Unmarshal(raw, &receipt) != nil || receipt.Recorded == nil || *receipt.Recorded < 0 {
		return progressAnswer{err: apiError(http.StatusBadGateway, []byte("the progress route answered without a receipt"))}
	}
	return progressAnswer{recorded: *receipt.Recorded}
}

func (r *progressRun) probe(ctx context.Context, decision progressDecision) progressDecision {
	verdict, err := aiWorkProbeRoute(ctx, r.client, r.queue.workID, false)
	switch verdict {
	case aiWorkRouteMissing:
		return progressDecision{action: progressActUnsupported}
	case aiWorkWorkUnavailable:
		if api, ok := apiErrorOf(err); ok && api.printableCode() != "" {
			decision.action, decision.err = progressActRefuseUnit, err
			return decision
		}
		return progressDecision{action: progressActRetry, reason: progressReasonServerError}
	}
	if api, ok := apiErrorOf(err); ok {
		if api.status == http.StatusUnauthorized {
			return progressDecision{action: progressActFail, err: err}
		}
		return progressDecision{action: progressActRetry, reason: progressReasonServerError}
	}
	if reason, transient := progressTransportReason(ctx, err); transient {
		return progressDecision{action: progressActRetry, reason: reason}
	}
	return progressDecision{action: progressActRetry, reason: progressReasonServerError}
}

func (r *progressRun) flush(ctx context.Context) (progressReport, error) {
	stop := ""
	for stop == "" {
		var batch []string
		if err := r.update(func(state *progressState, now time.Time) error {
			var err error
			if stop, err = r.closed(state, now); err != nil || stop != "" {
				return err
			}
			if ctx.Err() != nil {
				stop = progressReasonTimeout
				return nil
			}
			if stop = r.held(state, now); stop != "" {
				return nil
			}
			if err := state.formBatch(now); err != nil {
				return err
			}
			batch = slices.Clone(state.Batch)
			return nil
		}); err != nil {
			return r.report, err
		}
		if stop != "" || len(batch) == 0 {
			break
		}
		answer := r.post(ctx, batch)
		decision := progressDecide(ctx, answer, len(batch))
		if decision.action == progressActProbe {
			decision = r.probe(ctx, decision)
		}
		var failure error
		if err := r.update(func(state *progressState, now time.Time) error {
			if !slices.Equal(state.Batch, batch) {
				r.foreignDiscard = true
				return nil
			}
			switch decision.action {
			case progressActAccept:
				state.acknowledge()
				r.report.Recorded += answer.recorded
			case progressActDropFact:
				if dropped, ok := state.reject(decision.index, decision.code, decision.field, now); ok {
					r.report.Dropped = append(r.report.Dropped, dropped)
				}
			case progressActSplit:
				if !state.splitBatch() {
					if dropped, ok := state.reject(0, decision.code, decision.field, now); ok {
						r.report.Dropped = append(r.report.Dropped, dropped)
					}
				}
			case progressActRateLimit:
				state.wait(now, progressWaitRateLimited, answer.retryAfter)
				r.report.RetryAfterSeconds = int(answer.retryAfter / time.Second)
				stop = progressWaitRateLimited
			case progressActRetry:
				state.wait(now, progressWaitBackoff, progressBackoff(state.Failures+1))
				stop = decision.reason
			case progressActRefuseUnit:
				state.discard()
				state.Full = decision.code == progressCodeUnitFull
				failure = decision.err
			case progressActUnsupported:
				state.discard()
				at := now
				state.UnsupportedAt = &at
				stop = progressStatusUnsupported
			default:
				failure = decision.err
			}
			return nil
		}); err != nil {
			return r.report, err
		}
		if failure != nil {
			return r.report, failure
		}
	}
	return r.finish(stop), nil
}

func (r *progressRun) finish(stop string) progressReport {
	switch {
	case stop == progressStatusUnsupported:
		return r.unsupported()
	case r.report.Queued > 0:
		r.report.Status = progressStatusQueued
		r.report.Reason = stop
		if stop == "" {
			r.report.Reason = progressReasonTimeout
		}
	case r.report.Recorded == 0 && len(r.report.Dropped) == 0 && r.slotOpen:
		r.report.Status = progressStatusMerged
	default:
		r.report.Status = progressStatusSent
	}
	return r.report
}
