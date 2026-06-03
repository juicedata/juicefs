package gateway

import (
	"context"
	"errors"
	"net/http"
	"path"
	"syscall"
	"time"

	minio "github.com/minio/minio/cmd"

	"github.com/juicedata/juicefs/pkg/meta"
)

const (
	conditionalWriteLockTimeout = 30 * time.Second
	conditionalWriteLockMin     = 1 * time.Second
)

func (n *jfsObjects) withTargetWriteLock(ctx context.Context, bucket, object string, fn func() error) error {
	locker := n.nsMutex.NewNSLock(nil, bucket, object)
	if _, err := locker.GetLock(ctx, minio.NewDynamicTimeout(conditionalWriteLockTimeout, conditionalWriteLockMin)); err != nil {
		return err
	}
	defer locker.Unlock()
	return fn()
}

func (n *jfsObjects) targetConditions(ctx context.Context) (Conditions, bool) {
	conds, ok := TargetConditionsFromContext(ctx)
	return conds, ok && conds.HasWriteConditions()
}

func (n *jfsObjects) currentObjectInfo(ctx context.Context, bucket, object string) (minio.ObjectInfo, bool, error) {
	info, err := n.GetObjectInfo(ctx, bucket, object, minio.ObjectOptions{})
	if err == nil {
		return info, true, nil
	}
	var notFound minio.ObjectNotFound
	if errors.As(err, &notFound) {
		return minio.ObjectInfo{}, false, nil
	}
	return minio.ObjectInfo{}, false, err
}

func (n *jfsObjects) checkTargetConditions(ctx context.Context, bucket, object string, conds Conditions) error {
	info, exists, err := n.currentObjectInfo(ctx, bucket, object)
	if err != nil {
		return err
	}
	switch EvaluateWriteConditions(exists, info.ETag, conds) {
	case ConditionMatch:
		return nil
	case ConditionNotFound:
		return minio.ObjectNotFound{Bucket: bucket, Object: object}
	default:
		return conditionalPreconditionFailed(ctx)
	}
}

func (n *jfsObjects) renamePreparedObject(ctx context.Context, src, dst string, flags uint32, params ...string) error {
	eno := n.fs.Rename(mctx, src, dst, flags)
	if eno == syscall.ENOENT {
		if err := n.mkdirAll(ctx, path.Dir(dst)); err != nil {
			return jfsToObjectErr(ctx, err, params...)
		}
		eno = n.fs.Rename(mctx, src, dst, flags)
	}
	if eno == syscall.EEXIST && flags&meta.RenameNoReplace != 0 {
		return conditionalPreconditionFailed(ctx)
	}
	return jfsToObjectErr(ctx, eno, params...)
}

func (n *jfsObjects) withCheckedTargetConditions(ctx context.Context, bucket, object string, fn func() error) error {
	conds, ok := n.targetConditions(ctx)
	if !ok {
		return fn()
	}
	return n.withTargetWriteLock(ctx, bucket, object, func() error {
		if err := n.checkTargetConditions(ctx, bucket, object, conds); err != nil {
			return err
		}
		return fn()
	})
}

func (n *jfsObjects) commitPreparedObject(ctx context.Context, bucket, object, src, dst string, params ...string) error {
	conds, ok := n.targetConditions(ctx)
	if !ok {
		return n.renamePreparedObject(ctx, src, dst, 0, params...)
	}
	return n.withTargetWriteLock(ctx, bucket, object, func() error {
		if err := n.checkTargetConditions(ctx, bucket, object, conds); err != nil {
			return err
		}
		flags := uint32(0)
		if RequiresCreateOnlyRename(conds) {
			flags = meta.RenameNoReplace
		}
		return n.renamePreparedObject(ctx, src, dst, flags, params...)
	})
}

func conditionalPreconditionFailed(ctx context.Context) error {
	err := minio.PreConditionFailed{}
	markConditionalFailure(ctx, http.StatusPreconditionFailed, "PreconditionFailed", err.Error())
	return err
}
