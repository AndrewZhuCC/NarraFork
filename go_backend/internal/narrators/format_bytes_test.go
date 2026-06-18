package narrators

import "testing"

// TestFormatBytes covers the human-readable byte formatter used in the file
// modification listing: sub-1KB values render as "N B", larger values scale
// through KB/MB/GB with one decimal place, and GB is the terminal unit.
func TestFormatBytes(t *testing.T) {
	cases := []struct {
		size int64
		want string
	}{
		{0, "0 B"},
		{512, "512 B"},
		{1023, "1023 B"},
		{1024, "1.0 KB"},
		{1536, "1.5 KB"},
		{1024 * 1024, "1.0 MB"},
		{int64(1.5 * 1024 * 1024), "1.5 MB"},
		{1024 * 1024 * 1024, "1.0 GB"},
		{int64(2.5 * 1024 * 1024 * 1024), "2.5 GB"},
		{1024 * 1024 * 1024 * 1024, "1024.0 GB"}, // GB is terminal; TB rolls into GB
	}
	for _, tc := range cases {
		if got := formatBytes(tc.size); got != tc.want {
			t.Fatalf("formatBytes(%d) = %q, want %q", tc.size, got, tc.want)
		}
	}
}
