package systemroutes

import (
	"bytes"
	"context"
	"strings"
	"testing"
)

func TestRuntimeLimitedWriterTruncatesPodmanScanOutput(t *testing.T) {
	writer := &runtimeLimitedWriter{limit: 8}
	if _, err := writer.Write(bytes.Repeat([]byte("x"), 32)); err != nil {
		t.Fatalf("write: %v", err)
	}
	out := writer.String()
	if !strings.Contains(out, "[truncated]") || len(out) > 64 {
		t.Fatalf("limited output = %q, want truncation marker", out)
	}
}

func TestPodmanCountsReportsUnavailableWhenPodmanMissing(t *testing.T) {
	t.Setenv("PATH", t.TempDir())
	running, stopped, available := podmanCounts(context.Background())
	if available || running != 0 || stopped != 0 {
		t.Fatalf("podmanCounts = running:%d stopped:%d available:%v, want unavailable zero counts", running, stopped, available)
	}
}
