package meta

import (
	"syscall"

	sys "golang.org/x/sys/unix"
)

const ENOATTR = syscall.ENOATTR
const (
	F_UNLCK = syscall.F_UNLCK
	F_RDLCK = syscall.F_RDLCK
	F_WRLCK = syscall.F_WRLCK
)

const (
	XattrCreateOrReplace = 0
	XattrCreate          = sys.XATTR_CREATE
	XattrReplace         = sys.XATTR_REPLACE

	//not supported
	XattrNoSecurity      = sys.XATTR_NOSECURITY
	XattrNODEFAULT       = sys.XATTR_NODEFAULT
	XattrNoFollow        = sys.XATTR_NOFOLLOW
	XattrSHOWCOMPRESSION = sys.XATTR_SHOWCOMPRESSION
)
