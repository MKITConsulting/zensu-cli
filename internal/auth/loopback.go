package auth

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
)

const callbackSuccessHTML = `<!DOCTYPE html><html><head><meta charset="utf-8"><title>Zensu</title></head>
<body style="font-family:system-ui;text-align:center;padding-top:4rem">
<h1>Login successful</h1><p>You can close this window and return to the terminal.</p>
</body></html>`

type callbackResult struct {
	code string
	err  error
}

type CallbackServer struct {
	redirectURI string
	srv         *http.Server
	resultCh    chan callbackResult
	expState    string
}

func NewCallbackServer(expectedState string) (*CallbackServer, error) {
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return nil, err
	}
	port := ln.Addr().(*net.TCPAddr).Port
	cs := &CallbackServer{
		redirectURI: fmt.Sprintf("http://127.0.0.1:%d/callback", port),
		resultCh:    make(chan callbackResult, 1),
		expState:    expectedState,
	}
	mux := http.NewServeMux()
	mux.HandleFunc("/callback", cs.handle)
	cs.srv = &http.Server{Handler: mux}
	go func() { _ = cs.srv.Serve(ln) }()
	return cs, nil
}

func (cs *CallbackServer) RedirectURI() string { return cs.redirectURI }

func (cs *CallbackServer) handle(w http.ResponseWriter, r *http.Request) {
	q := r.URL.Query()
	if q.Get("state") != cs.expState {
		cs.fail(w, &callbackResult{err: errors.New("state mismatch — possible CSRF, login aborted")}, "State mismatch.")
		return
	}
	if e := q.Get("error"); e != "" {
		cs.fail(w, &callbackResult{err: fmt.Errorf("authorization error: %s", e)}, "Authorization was denied.")
		return
	}
	code := q.Get("code")
	if code == "" {
		cs.fail(w, &callbackResult{err: errors.New("no authorization code in callback")}, "Missing authorization code.")
		return
	}
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	_, _ = w.Write([]byte(callbackSuccessHTML))
	cs.send(callbackResult{code: code})
}

func (cs *CallbackServer) fail(w http.ResponseWriter, res *callbackResult, msg string) {
	w.Header().Set("Content-Type", "text/html; charset=utf-8")
	w.WriteHeader(http.StatusBadRequest)
	_, _ = fmt.Fprintf(w, "<!DOCTYPE html><html><body><h1>Login failed</h1><p>%s</p></body></html>", msg)
	cs.send(*res)
}

func (cs *CallbackServer) send(r callbackResult) {
	select {
	case cs.resultCh <- r:
	default:
	}
}

func (cs *CallbackServer) Wait(ctx context.Context) (string, error) {
	select {
	case res := <-cs.resultCh:
		return res.code, res.err
	case <-ctx.Done():
		return "", ctx.Err()
	}
}

func (cs *CallbackServer) Close() error { return cs.srv.Close() }
