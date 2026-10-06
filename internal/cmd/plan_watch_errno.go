//go:build !windows

package cmd

import "syscall"

var transientErrnos = []error{syscall.ECONNREFUSED, syscall.ECONNRESET}
