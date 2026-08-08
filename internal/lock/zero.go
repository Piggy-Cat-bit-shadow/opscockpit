package lock

import "syscall"

// signalZero is signal 0 — a liveness probe that never delivers a signal.
var signalZero = syscall.Signal(0)
