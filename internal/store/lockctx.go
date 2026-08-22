package store

import (
	"context"
	"time"
)

// lockWait bounds how long a process will wait for the write lock. Long enough
// that a slow write elsewhere is simply waited out, short enough that a stuck
// process surfaces as an error rather than a hang the user has to Ctrl-C.
const lockWait = 10 * time.Second

func lockContext() context.Context {
	ctx, cancel := context.WithTimeout(context.Background(), lockWait)
	// The context is consumed synchronously inside TryLockContext, so
	// cancelling on a timer rather than defer keeps the deadline meaningful
	// without leaking the timer goroutine past its own expiry.
	time.AfterFunc(lockWait, cancel)
	return ctx
}
