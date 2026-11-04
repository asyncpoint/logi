//go:build windows

package platform

import (
	"os"
	"syscall"
)

// Open opens path for reading with FILE_SHARE_DELETE. Go's os.Open omits that
// flag, which would stop other processes from renaming or deleting the log
// while logi has it open and therefore break log rotation.
func Open(path string) (*os.File, error) {
	p, err := syscall.UTF16PtrFromString(path)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	h, err := syscall.CreateFile(p, syscall.GENERIC_READ,
		syscall.FILE_SHARE_READ|syscall.FILE_SHARE_WRITE|syscall.FILE_SHARE_DELETE,
		nil, syscall.OPEN_EXISTING, syscall.FILE_ATTRIBUTE_NORMAL, 0)
	if err != nil {
		return nil, &os.PathError{Op: "open", Path: path, Err: err}
	}
	return os.NewFile(uintptr(h), path), nil
}

// IdentityOfFile returns the identity and size of an open file.
func IdentityOfFile(f *os.File) (PathInfo, error) {
	var d syscall.ByHandleFileInformation
	if err := syscall.GetFileInformationByHandle(syscall.Handle(f.Fd()), &d); err != nil {
		return PathInfo{}, err
	}
	return PathInfo{
		ID:   FileIdentity{Dev: uint64(d.VolumeSerialNumber), Ino: uint64(d.FileIndexHigh)<<32 | uint64(d.FileIndexLow)},
		Size: int64(d.FileSizeHigh)<<32 | int64(d.FileSizeLow),
	}, nil
}

// StatPath returns the identity and size of whatever file the path names.
func StatPath(path string) (PathInfo, error) {
	f, err := Open(path)
	if err != nil {
		return PathInfo{}, err
	}
	defer f.Close()
	return IdentityOfFile(f)
}
