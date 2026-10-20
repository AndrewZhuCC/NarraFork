package handlers

// nopSender discards frames (upload direction receives, never sends). It is shared by
// platform-specific transfer tests, so it must not carry a build constraint itself.
type nopSender struct{}

func (nopSender) SendBinary([]byte) error { return nil }
func (nopSender) BufferedAmount() int     { return 0 }
