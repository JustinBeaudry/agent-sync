//go:build unix

package fsroot

import "syscall"

// oNoFollow is [syscall.O_NOFOLLOW] on Unix; 0 elsewhere. It is passed
// to [os.Root.OpenFile] in [Root.StagedWrite] as defense-in-depth. Since
// StagedWrite always creates a unique temp file with O_CREATE|O_EXCL,
// there is no existing inode to follow; this flag is belt-and-braces
// for exotic filesystems where O_CREATE|O_EXCL races are detectable.
const oNoFollow = syscall.O_NOFOLLOW
