package plugin

import (
	"fmt"
	"os"
	"runtime/debug"
)

// panicLog receives recovered background panics; tests replace it.
var panicLog = func(msg string) { fmt.Fprint(os.Stderr, msg) }

func logPanic(name string, v any) {
	panicLog(fmt.Sprintf("plugin: %s panic: %v\n%s", name, v, debug.Stack()))
}

// recoverLog must be deferred directly. The C ABI boundary in main.go only
// recovers panics of synchronous plugin calls; a panic in a background goroutine
// would otherwise take down the whole CLIProxyAPI host process. It logs the value
// and stack and never re-panics.
func recoverLog(name string) {
	if v := recover(); v != nil {
		logPanic(name, v)
	}
}

// safeGo runs fn on a new goroutine guarded by recoverLog.
func safeGo(name string, fn func()) {
	go func() {
		defer recoverLog(name)
		fn()
	}()
}
