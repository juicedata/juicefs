package versioninfo

import "fmt"

var (
	NAME         = "juicesync"
	VERSION      = "unknown"
	REVISION     = "HEAD"
	REVISIONDATE = "now"
	USAGE        = `Usage: juicesync [options] SRC DST
    SRC and DST should be [NAME://][ACCESS_KEY:SECRET_KEY@]BUCKET[.ENDPOINT][/PREFIX]`
)

func Version() string {
	return fmt.Sprintf("%v, commit %v, built at %v", VERSION, REVISION, REVISIONDATE)
}
