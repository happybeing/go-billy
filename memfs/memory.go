// Package memfs provides a billy filesystem base on memory.
package memfs // import "gopkg.in/src-d/go-billy.v2/memfs"

import (
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"time"

	"gopkg.in/src-d/go-billy.v2"
)

const separator = filepath.Separator

// Memory a very convenient filesystem based on memory files
type Memory struct {
	base string
	s    *storage

	tempCount int
}

//New returns a new Memory filesystem.
func New() *Memory {
	return &Memory{
		base: string(separator),
		s:    newStorage(),
	}
}

func (fs *Memory) Create(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDWR|os.O_CREATE|os.O_TRUNC, 0666)
}

func (fs *Memory) Open(filename string) (billy.File, error) {
	return fs.OpenFile(filename, os.O_RDONLY, 0)
}

func (fs *Memory) OpenFile(filename string, flag int, perm os.FileMode) (billy.File, error) {
	fullpath := fs.fullpath(filename)
	f, err := fs.getFromStorage(fullpath)

	switch {
	case os.IsNotExist(err):
		if !isCreate(flag) {
			return nil, os.ErrNotExist
		}

		var err error
		f, err = fs.s.New(fullpath, perm, flag)
		if err != nil {
			return nil, err
		}
	case err == nil:
		if target, isLink := fs.resolveLink(fullpath, f); isLink {
			return fs.OpenFile(target, flag, perm)
		}
	default:
		return nil, err
	}

	if f.mode.IsDir() {
		return nil, fmt.Errorf("cannot open directory: %s", filename)
	}

	filename, err = filepath.Rel(fs.base, fullpath)
	if err != nil {
		return nil, err
	}

	return f.Duplicate(filename, perm, flag), nil
}

func (fs *Memory) fullpath(path string) string {
	return clean(fs.Join(fs.base, path))
}

var errNotLink = errors.New("not a link")

func (fs *Memory) resolveLink(fullpath string, f *file) (target string, isLink bool) {
	if !isSymlink(f.mode) {
		return fullpath, false
	}

	target = string(f.content.bytes)
	if !isAbs(target) {
		target = fs.Join(filepath.Dir(fullpath), target)
	}

	rel, _ := filepath.Rel(fs.base, target)
	return rel, true
}

// On Windows OS, IsAbs validates if a path is valid based on if stars with a
// unit (eg.: `C:\`)  to assert that is absolute, but in this mem implementation
// any path starting by `separator` is also considered absolute.
func isAbs(path string) bool {
	return filepath.IsAbs(path) || strings.HasPrefix(path, string(separator))
}

func (fs *Memory) Stat(filename string) (os.FileInfo, error) {
	fullpath := fs.fullpath(filename)
	f, err := fs.getFromStorage(fullpath)
	if err != nil {
		return nil, err
	}

	fi, _ := f.Stat()

	if target, isLink := fs.resolveLink(fullpath, f); isLink {
		fi, err = fs.Stat(target)
		if err != nil {
			return nil, err
		}
	}

	// the name of the file should always the name of the stated file, so we
	// overwrite the Stat returned from the storage with it, since the
	// filename may belong to a link.
	fi.(*fileInfo).name = filepath.Base(filename)
	return fi, nil
}

func (fs *Memory) Lstat(filename string) (os.FileInfo, error) {
	fullpath := fs.fullpath(filename)
	f, err := fs.getFromStorage(fullpath)
	if err != nil {
		return nil, err
	}

	return f.Stat()
}

func (fs *Memory) ReadDir(path string) ([]os.FileInfo, error) {
	fullpath := fs.fullpath(path)
	if f, err := fs.getFromStorage(fullpath); err == nil {
		if target, isLink := fs.resolveLink(fullpath, f); isLink {
			return fs.ReadDir(target)
		}
	}

	var entries []os.FileInfo
	for _, f := range fs.s.Children(fullpath) {
		fi, _ := f.Stat()
		entries = append(entries, fi)
	}

	return entries, nil
}

func (fs *Memory) MkdirAll(path string, perm os.FileMode) error {
	fullpath := fs.Join(fs.base, path)

	_, err := fs.s.New(fullpath, perm|os.ModeDir, 0)
	return err
}

var maxTempFiles = 1024 * 4

func (fs *Memory) TempFile(dir, prefix string) (billy.File, error) {
	var fullpath string
	for {
		if fs.tempCount >= maxTempFiles {
			return nil, errors.New("max. number of tempfiles reached")
		}

		fullpath = fs.getTempFilename(dir, prefix)
		if _, ok := fs.s.files[fullpath]; !ok {
			break
		}
	}

	return fs.Create(fullpath)
}

func (fs *Memory) getTempFilename(dir, prefix string) string {
	fs.tempCount++
	filename := fmt.Sprintf("%s_%d_%d", prefix, fs.tempCount, time.Now().UnixNano())
	return fs.Join(fs.base, dir, filename)
}

func (fs *Memory) Rename(from, to string) error {
	from = fs.Join(fs.base, from)
	if err := fs.validate(from); err != nil {
		return err
	}

	to = fs.Join(fs.base, to)
	if err := fs.validate(to); err != nil {
		return err
	}

	return fs.s.Rename(from, to)
}

func (fs *Memory) Remove(filename string) error {
	fullpath := fs.Join(fs.base, filename)
	if err := fs.validate(fullpath); err != nil {
		return err
	}

	return fs.s.Remove(fullpath)
}

func (fs *Memory) Join(elem ...string) string {
	return filepath.Join(elem...)
}

func (fs *Memory) Symlink(target, link string) error {
	_, err := fs.Stat(link)
	if err == nil {
		return os.ErrExist
	}

	if !os.IsNotExist(err) {
		return err
	}

	if fs.isTargetOutBounders(link, target) {
		return billy.ErrCrossedBoundary
	}

	return billy.WriteFile(fs, clean(link), []byte(clean(target)), 0777|os.ModeSymlink)
}

func (fs *Memory) isTargetOutBounders(link, target string) bool {
	fulllink := fs.Join(fs.base, link)
	fullpath := fs.Join(filepath.Dir(fulllink), target)
	target, err := filepath.Rel(fs.base, fullpath)
	if err != nil {
		return true
	}

	return isCrossBoundaries(target)
}

func isCrossBoundaries(path string) bool {
	path = filepath.ToSlash(path)
	path = filepath.Clean(path)

	return strings.HasPrefix(path, "..")
}

func (fs *Memory) Readlink(link string) (string, error) {
	fullpath := fs.fullpath(link)
	f, err := fs.getFromStorage(fullpath)
	if err != nil {
		return "", err
	}

	if !isSymlink(f.mode) {
		return "", &os.PathError{
			Op:   "readlink",
			Path: fullpath,
			Err:  fmt.Errorf("not a symlink"),
		}
	}

	return string(f.content.bytes), nil
}

func (fs *Memory) Chroot(path string) (billy.Basic, error) {
	fullpath := fs.Join(fs.base, path)
	if err := fs.validate(fullpath); err != nil {
		return nil, err
	}

	return &Memory{
		base: fullpath,
		s:    fs.s,
	}, nil
}

func (fs *Memory) Root() string {
	return fs.base
}

func (fs *Memory) getFromStorage(fullpath string) (*file, error) {
	if err := fs.validate(fullpath); err != nil {
		return nil, err
	}

	f, has := fs.s.Get(fullpath)
	if !has {
		return nil, os.ErrNotExist
	}

	return f, nil
}

func (fs *Memory) validate(fullpath string) error {
	relpath, _ := filepath.Rel(fs.base, fullpath)
	if strings.HasPrefix(relpath, "..") {
		return billy.ErrCrossedBoundary
	}

	return nil
}

type file struct {
	name     string
	content  *content
	position int64
	flag     int
	mode     os.FileMode

	isClosed bool
}

func (f *file) Name() string {
	return f.name
}

func (f *file) Read(b []byte) (int, error) {
	n, err := f.ReadAt(b, f.position)
	f.position += int64(n)

	if err == io.EOF && n != 0 {
		err = nil
	}

	return n, err
}

func (f *file) ReadAt(b []byte, off int64) (int, error) {
	if f.isClosed {
		return 0, os.ErrClosed
	}

	if !isReadAndWrite(f.flag) && !isReadOnly(f.flag) {
		return 0, errors.New("read not supported")
	}

	n, err := f.content.ReadAt(b, off)

	return n, err
}

func (f *file) Seek(offset int64, whence int) (int64, error) {
	if f.isClosed {
		return 0, os.ErrClosed
	}

	switch whence {
	case io.SeekCurrent:
		f.position += offset
	case io.SeekStart:
		f.position = offset
	case io.SeekEnd:
		f.position = int64(f.content.Len()) + offset
	}

	return f.position, nil
}

func (f *file) Write(p []byte) (int, error) {
	if f.isClosed {
		return 0, os.ErrClosed
	}

	if !isReadAndWrite(f.flag) && !isWriteOnly(f.flag) {
		return 0, errors.New("write not supported")
	}

	n, err := f.content.WriteAt(p, f.position)
	f.position += int64(n)

	return n, err
}

func (f *file) Close() error {
	if f.isClosed {
		return os.ErrClosed
	}

	f.isClosed = true
	return nil
}

func (f *file) Duplicate(filename string, mode os.FileMode, flag int) billy.File {
	new := &file{
		name:    filename,
		content: f.content,
		mode:    mode,
		flag:    flag,
	}

	if isAppend(flag) {
		new.position = int64(new.content.Len())
	}

	if isTruncate(flag) {
		new.content.Truncate()
	}

	return new
}

func (f *file) Stat() (os.FileInfo, error) {
	return &fileInfo{
		name: f.Name(),
		mode: f.mode,
		size: f.content.Len(),
	}, nil
}

type fileInfo struct {
	name string
	size int
	mode os.FileMode
}

func (fi *fileInfo) Name() string {
	return fi.name
}

func (fi *fileInfo) Size() int64 {
	return int64(fi.size)
}

func (fi *fileInfo) Mode() os.FileMode {
	return fi.mode
}

func (*fileInfo) ModTime() time.Time {
	return time.Now()
}

func (fi *fileInfo) IsDir() bool {
	return fi.mode.IsDir()
}

func (*fileInfo) Sys() interface{} {
	return nil
}

func (c *content) Truncate() {
	c.bytes = make([]byte, 0)
}

func (c *content) Len() int {
	return len(c.bytes)
}

func isCreate(flag int) bool {
	return flag&os.O_CREATE != 0
}

func isAppend(flag int) bool {
	return flag&os.O_APPEND != 0
}

func isTruncate(flag int) bool {
	return flag&os.O_TRUNC != 0
}

func isReadAndWrite(flag int) bool {
	return flag&os.O_RDWR != 0
}

func isReadOnly(flag int) bool {
	return flag == os.O_RDONLY
}

func isWriteOnly(flag int) bool {
	return flag&os.O_WRONLY != 0
}

func isSymlink(m os.FileMode) bool {
	return m&os.ModeSymlink != 0
}
