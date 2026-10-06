package cmd

import (
	"net"
	"net/url"
	"os"
	"syscall"
	"testing"
)

func TestTransientPollError_WindowsSocketErrors(t *testing.T) {
	for _, errno := range []syscall.Errno{wsaConnectionRefused, syscall.WSAECONNRESET} {
		err := &url.Error{Op: "Get", URL: "http://zensu.test", Err: &net.OpError{Op: "dial", Net: "tcp", Err: os.NewSyscallError("connectex", errno)}}
		if !transientPollError(err) {
			t.Errorf("transientPollError(%v) = false, want true", err)
		}
	}
}
