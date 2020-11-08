// +build wasm

package osfs

import (
	"os"
)

func (f *file) Lock() error {
	return nil
}

func (f *file) Unlock() error {
	return nil
}

func rename(from, to string) error {
	return os.Rename(from, to)
}
