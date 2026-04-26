// Command crashy is a minimal subprocess test fixture used only by
// internal/adapter/subprocess_test.go. It is NOT a protocol-speaking
// adapter — it does not parse stdin or write valid frames. It exits
// with code 13 immediately on startup so tests can verify that
// Subprocess.Close surfaces *SubprocessExitError when no protocol
// shutdown was acked.
package main

import "os"

func main() {
	os.Exit(13)
}
