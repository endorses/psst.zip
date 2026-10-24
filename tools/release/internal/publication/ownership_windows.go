package publication

import "os"

// Container publication requires a hosted Linux job; other tool subcommands
// remain available and compilable on Windows.
func owned(os.FileInfo) bool { return false }
