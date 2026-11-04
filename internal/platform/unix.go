//go:build !windows

package platform

import (
	"os"
	"syscall"
)

func identityOf(fi os.FileInfo) FileIdentity {
	if st, ok := fi.Sys().(*syscall.Stat_t); ok {
		return FileIdentity{Dev: uint64(st.Dev), Ino: uint64(st.Ino)}
	}
	return FileIdentity{}
}

// Open opens path for reading.
func Open(path string) (*os.File, error) { return os.Open(path) }

// IdentityOfFile returns the identity and size of an open file.
func IdentityOfFile(f *os.File) (PathInfo, error) {
	fi, err := f.Stat()
	if err != nil {
		return PathInfo{}, err
	}
	return PathInfo{ID: identityOf(fi), Size: fi.Size()}, nil
}

// StatPath returns the identity and size of whatever file the path currently
// names.
func StatPath(path string) (PathInfo, error) {
	fi, err := os.Stat(path)
	if err != nil {
		return PathInfo{}, err
	}
	return PathInfo{ID: identityOf(fi), Size: fi.Size()}, nil
}
