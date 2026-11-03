package update

import (
	"errors"
	"io/fs"
	"os"
	"syscall"
)

// keepOwner donne à tmp le propriétaire de target, s'il existe.
func keepOwner(target, tmp string) error {
	info, err := os.Stat(target)
	if errors.Is(err, fs.ErrNotExist) {
		return nil
	}
	if err != nil {
		return err
	}
	st, ok := info.Sys().(*syscall.Stat_t)
	if !ok {
		return nil
	}
	return os.Chown(tmp, int(st.Uid), int(st.Gid))
}
