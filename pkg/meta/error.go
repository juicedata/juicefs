package meta

import "github.com/pkg/errors"

var (
	ErrMetadataVer     = errors.New("incompatible metadata version, please upgrade the client") // client's max supported metadata version not match
	ErrClientVerTooOld = errors.New("client version is too old, please upgrade the client")
	ErrClientVerTooNew = errors.New("client version is too new, please use an older client")
)
