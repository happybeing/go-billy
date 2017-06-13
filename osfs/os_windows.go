// +build windows

package osfs

import (
	"os"
	"path/filepath"
	"strings"
)

// Stat returns the FileInfo structure describing file.
func (fs *OS) Stat(filename string) (os.FileInfo, error) {
	// TODO: remove this in Go 1.9

	fullpath, err := fs.absolutize(filename)
	if err != nil {
		return nil, err
	}

	target, err := fs.Readlink(filename)
	if err != nil {
		return os.Stat(fullpath)
	}

	if !filepath.IsAbs(target) && !strings.HasPrefix(target, string(filepath.Separator)) {
		target, _ = filepath.Rel(fs.base, fs.Join(filepath.Dir(fullpath), target))
	}

	fi, err := fs.Stat(target)
	if err != nil {
		return nil, err
	}

	return &fileInfo{
		FileInfo: fi,
		name:     filepath.Base(fullpath),
	}, nil
}

type fileInfo struct {
	os.FileInfo
	name string
}

func (fi *fileInfo) Name() string {
	return fi.name
}
