// Package platform isolates OS-specific file identity and open semantics
// so the rest of logi stays portable.
package platform

// FileIdentity identifies a physical file independently of its path:
// (device, inode) on Unix; (volume serial, file index) on Windows.
type FileIdentity struct {
	Dev uint64
	Ino uint64
}

// IsZero reports whether the identity is unset.
func (a FileIdentity) IsZero() bool { return a == FileIdentity{} }

// PathInfo is the result of statting a path.
type PathInfo struct {
	ID   FileIdentity
	Size int64
}
