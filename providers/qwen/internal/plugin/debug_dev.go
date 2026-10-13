//go:build debug

package plugin

import (
	"encoding/json"
	"fmt"
	"os"
	"path/filepath"
	"sync"
	"time"
)

var debugMu sync.Mutex

// Match OpenCode's debug-only file sink. Callers must supply only non-secret
// metadata; credential conflicts contain boolean flags, never keys or URLs.
func debugTrace(format string, args ...any) {
	debugMu.Lock()
	defer debugMu.Unlock()

	path := filepath.Join(filepath.Dir(os.Args[0]), "qwen-debug.log")
	f, err := os.OpenFile(path, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0600)
	if err != nil {
		return
	}
	defer f.Close()
	values := append([]any{time.Now().Format(time.RFC3339Nano)}, args...)
	_, _ = fmt.Fprintf(f, "%s "+format+"\n", values...)
}

func debugBodyMeta(body []byte) string {
	return fmt.Sprintf("len=%d json=%t", len(body), json.Valid(body))
}
