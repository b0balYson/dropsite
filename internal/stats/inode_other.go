//go:build !unix

package stats

import "io/fs"

func inodeOf(fs.FileInfo) (uint64, bool) { return 0, false }
