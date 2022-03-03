package lv

import "path/filepath"

type FileSystemInterface interface {
	evalSymlink(path string) (string, error)
}

type NixFileSystemInterface struct {
}

func (n NixFileSystemInterface) evalSymlink(path string) (string, error) {
	return filepath.EvalSymlinks(path)
}

// for testing
type FakeFileSystemInterface struct {
}

func (f FakeFileSystemInterface) evalSymlink(path string) (string, error) {
	return path, nil
}
