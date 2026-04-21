//go:build !unix

package fsroot

// oNoFollow is zero on non-Unix platforms (notably Windows). Windows
// does not expose an O_NOFOLLOW equivalent at the CRT level; containment
// is provided by [os.Root] plus [Root.DetectReparsePoint].
const oNoFollow = 0
