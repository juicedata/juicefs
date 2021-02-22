package meta

import "syscall"

const ENOATTR = syscall.ENODATA

func (r *redisMeta) Flock(ctx Context, inode Ino, owner uint64, ltype uint32, block bool) syscall.Errno {
	return syscall.ENOTSUP
}

func (r *redisMeta) Getlk(ctx Context, inode Ino, owner uint64, ltype *uint32, start, end *uint64, pid *uint32) syscall.Errno {
	return syscall.ENOTSUP
}

func (r *redisMeta) Setlk(ctx Context, inode Ino, owner uint64, block bool, ltype uint32, start, end uint64, pid uint32) syscall.Errno {
	return syscall.ENOTSUP
}
