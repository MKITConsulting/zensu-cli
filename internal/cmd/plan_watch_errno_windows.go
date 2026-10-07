package cmd

import "syscall"

const wsaConnectionRefused = syscall.Errno(10061)

var transientErrnos = []error{syscall.ECONNREFUSED, syscall.ECONNRESET, wsaConnectionRefused, syscall.WSAECONNRESET}
