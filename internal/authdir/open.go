package authdir

import (
	"errors"
	"os"
)

// The auth directory is trusted configuration but its contents are not: a file may be
// replaced by a symlink or special file at any time. These helpers open entries
// without following symlinks, portably (Linux, macOS, Windows), using only the
// standard library:
//
//   - os.Root confines every access to the opened directory, so a ".." component or a
//     symlink pointing outside it can never escape.
//   - Lstat before and SameFile after the open reject a final-component symlink and any
//     swap that happens between the two calls, mirroring what O_NOFOLLOW provided.
//   - Only regular files are opened; the extra open flag (O_NONBLOCK on Unix) keeps a
//     FIFO swapped in during that window from blocking the collector.

var errNotRegular = errors.New("not a regular file")

// openAuthRoot opens dir as a Root, refusing a symlink or non-directory.
func openAuthRoot(dir string) (*os.Root, error) {
	before, err := os.Lstat(dir)
	if err != nil {
		return nil, err
	}
	if !before.IsDir() || before.Mode()&os.ModeSymlink != 0 {
		return nil, errors.New("not a directory")
	}
	root, err := os.OpenRoot(dir)
	if err != nil {
		return nil, err
	}
	opened, err := root.Stat(".")
	if err != nil || !os.SameFile(before, opened) {
		_ = root.Close()
		return nil, errors.New("directory changed while opening")
	}
	return root, nil
}

// openChildNoFollow opens the regular file name inside root without following symlinks.
func openChildNoFollow(root *os.Root, name string) (*os.File, error) {
	before, err := root.Lstat(name)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errNotRegular
	}
	file, err := root.OpenFile(name, os.O_RDONLY|nonBlockFlag, 0)
	if err != nil {
		return nil, err
	}
	return verifySameRegular(file, before)
}

// openAuthFileNoFollow opens the regular file at path without following a final symlink.
func openAuthFileNoFollow(path string) (*os.File, error) {
	before, err := os.Lstat(path)
	if err != nil {
		return nil, err
	}
	if !before.Mode().IsRegular() {
		return nil, errNotRegular
	}
	file, err := os.OpenFile(path, os.O_RDONLY|nonBlockFlag, 0)
	if err != nil {
		return nil, err
	}
	return verifySameRegular(file, before)
}

func verifySameRegular(file *os.File, before os.FileInfo) (*os.File, error) {
	after, err := file.Stat()
	if err != nil || !after.Mode().IsRegular() || !os.SameFile(before, after) {
		_ = file.Close()
		return nil, errNotRegular
	}
	return file, nil
}
