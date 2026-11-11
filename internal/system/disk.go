package system

import "syscall"

func freeBytes(dir string) uint64 {
	var st syscall.Statfs_t
	if syscall.Statfs(dir, &st) != nil {
		return 0
	}
	return st.Bavail * uint64(st.Bsize)
}
