package main

import (
	"bytes"
	"strings"
	"testing"

	"github.com/rs/zerolog"
)

// TestAsynqZerologAdapter covers Debug/Info/Warn/Error — each just routes
// through zerolog at the matching level. Fatal is intentionally not
// exercised here: zerolog's default Fatal-level Msg() calls os.Exit(1),
// which would kill the test binary rather than fail the test.
func TestAsynqZerologAdapter(t *testing.T) {
	var buf bytes.Buffer
	a := asynqZerologAdapter{logger: zerolog.New(&buf).Level(zerolog.DebugLevel)}

	a.Debug("debug", "msg")
	a.Info("info", "msg")
	a.Warn("warn", "msg")
	a.Error("error", "msg")

	out := buf.String()
	for _, want := range []string{`"debugmsg"`, `"infomsg"`, `"warnmsg"`, `"errormsg"`} {
		if !strings.Contains(out, want) {
			t.Errorf("output %q missing %q", out, want)
		}
	}
}
