/*
 * JuiceFS, Copyright 2020 Juicedata, Inc.
 *
 * Licensed under the Apache License, Version 2.0 (the "License");
 * you may not use this file except in compliance with the License.
 * You may obtain a copy of the License at
 *
 *     http://www.apache.org/licenses/LICENSE-2.0
 *
 * Unless required by applicable law or agreed to in writing, software
 * distributed under the License is distributed on an "AS IS" BASIS,
 * WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
 * See the License for the specific language governing permissions and
 * limitations under the License.
 */

package gateway

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	jsoniter "github.com/json-iterator/go"
	"github.com/minio/minio-go/v7/pkg/tags"
	"github.com/minio/minio/pkg/bucket/policy"
	"github.com/minio/minio/pkg/madmin"

	"github.com/google/uuid"
	"github.com/minio/minio-go/v7/pkg/s3utils"
	minio "github.com/minio/minio/cmd"
	xhttp "github.com/minio/minio/cmd/http"

	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"
)

const (
	sep          = "/"
	metaBucket   = ".sys"
	subDirPrefix = 3 // 16^3=4096 slots
)

var mctx meta.Context
var logger = utils.GetLogger("juicefs")

type Config struct {
	MultiBucket bool
	KeepEtag    bool
	Umask       uint16
	ObjTag      bool
	ObjMeta     bool
}

func NewJFSGateway(jfs *fs.FileSystem, conf *vfs.Config, gConf *Config) (minio.ObjectLayer, error) {
	mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	jfsObj := &jfsObjects{fs: jfs, conf: conf, listPool: minio.NewTreeWalkPool(time.Minute * 30), gConf: gConf, nsMutex: minio.NewNSLock(false)}
	go jfsObj.cleanup()
	return jfsObj, nil
}

type jfsObjects struct {
	conf     *vfs.Config
	fs       *fs.FileSystem
	listPool *minio.TreeWalkPool
	nsMutex  *minio.NsLockMap
	gConf    *Config
}

func (n *jfsObjects) PutObjectMetadata(ctx context.Context, s string, s2 string, options minio.ObjectOptions) (minio.ObjectInfo, error) {
	return minio.ObjectInfo{}, minio.NotImplemented{}
}

func (n *jfsObjects) NSScanner(ctx context.Context, bf *minio.BloomFilter, updates chan<- madmin.DataUsageInfo) error {
	return nil
}

func (n *jfsObjects) IsCompressionSupported() bool {
	return false
}

func (n *jfsObjects) IsEncryptionSupported() bool {
	return false
}

// IsReady returns whether the layer is ready to take requests.
func (n *jfsObjects) IsReady(_ context.Context) bool {
	return true
}

func (n *jfsObjects) Shutdown(ctx context.Context) error {
	return n.fs.Close()
}

func (n *jfsObjects) StorageInfo(ctx context.Context) (info minio.StorageInfo, errors []error) {
	sinfo := minio.StorageInfo{}
	sinfo.Backend.Type = madmin.FS
	return sinfo, nil
}

func jfsToObjectErr(ctx context.Context, err error, params ...string) error {
	if err == nil {
		return nil
	}
	bucket := ""
	object := ""
	uploadID := ""
	switch len(params) {
	case 3:
		uploadID = params[2]
		fallthrough
	case 2:
		object = params[1]
		fallthrough
	case 1:
		bucket = params[0]
	}

	if eno, ok := err.(syscall.Errno); !ok {
		logger.Errorf("error: %s bucket: %s, object: %s, uploadID: %s", err, bucket, object, uploadID)
		return err
	} else if eno == 0 {
		return nil
	}

	switch {
	case fs.IsNotExist(err):
		if uploadID != "" {
			return minio.InvalidUploadID{
				UploadID: uploadID,
			}
		}
		if object != "" {
			return minio.ObjectNotFound{Bucket: bucket, Object: object}
		}
		return minio.BucketNotFound{Bucket: bucket}
	case fs.IsExist(err):
		if object != "" {
			return minio.PrefixAccessDenied{Bucket: bucket, Object: object}
		}
		return minio.BucketAlreadyOwnedByYou{Bucket: bucket}
	case fs.IsNotEmpty(err):
		if object != "" {
			return minio.PrefixAccessDenied{Bucket: bucket, Object: object}
		}
		return minio.BucketNotEmpty{Bucket: bucket}
	default:
		logger.Errorf("other error: %s bucket: %s, object: %s, uploadID: %s", err, bucket, object, uploadID)
		return err
	}
}

// isValidBucketName verifies whether a bucket name is valid.
func (n *jfsObjects) isValidBucketName(bucket string) error {
	if strings.HasPrefix(bucket, minio.MinioMetaBucket) {
		return nil
	}
	if s3utils.CheckValidBucketNameStrict(bucket) != nil {
		return minio.BucketNameInvalid{Bucket: bucket}
	}
	if !n.gConf.MultiBucket && bucket != n.conf.Format.Name {
		return minio.BucketNotFound{Bucket: bucket}
	}
	return nil
}

func (n *jfsObjects) path(p ...string) string {
	if !n.gConf.MultiBucket && len(p) > 0 && p[0] == n.conf.Format.Name {
		p = p[1:]
	}
	return sep + minio.PathJoin(p...)
}

func (n *jfsObjects) tpath(p ...string) string {
	return sep + metaBucket + n.path(p...)
}

func (n *jfsObjects) upath(bucket, uploadID string) string {
	return n.tpath(bucket, "uploads", uploadID[:subDirPrefix], uploadID)
}

func (n *jfsObjects) ppath(bucket, uploadID, part string) string {
	return n.tpath(bucket, "uploads", uploadID[:subDirPrefix], uploadID, part)
}

func (n *jfsObjects) ppathFlat(bucket, uploadID, part string) string { // compatible with tmp files uploaded by old versions(<1.2)
	return n.tpath(bucket, "uploads", uploadID, part)
}

func (n *jfsObjects) DeleteBucket(ctx context.Context, bucket string, forceDelete bool) error {
	if err := n.isValidBucketName(bucket); err != nil {
		return err
	}
	if !n.gConf.MultiBucket {
		return minio.BucketNotEmpty{Bucket: bucket}
	}
	if eno := n.fs.Delete(mctx, n.path(minio.MinioMetaBucket, minio.BucketMetaPrefix, bucket, minio.BucketMetadataFile)); eno != 0 {
		logger.Errorf("delete bucket metadata: %s", eno)
	}
	_ = n.fs.Delete(mctx, n.path(minio.MinioMetaBucket, minio.BucketMetaPrefix, bucket))
	eno := n.fs.Delete(mctx, n.path(bucket))
	return jfsToObjectErr(ctx, eno, bucket)
}

func (n *jfsObjects) MakeBucketWithLocation(ctx context.Context, bucket string, options minio.BucketOptions) error {
	if bucket != minio.MinioMetaBucket {
		if err := n.isValidBucketName(bucket); err != nil {
			return err
		}
		if !n.gConf.MultiBucket {
			return nil
		}
	}
	eno := n.fs.Mkdir(mctx, n.path(bucket), 0777, n.gConf.Umask)
	if eno == 0 {
		metadata := minio.NewBucketMetadata(bucket)
		if err := metadata.Save(ctx, n); err != nil {
			return err
		}
	}
	return jfsToObjectErr(ctx, eno, bucket)
}

func (n *jfsObjects) GetBucketInfo(ctx context.Context, bucket string) (bi minio.BucketInfo, err error) {
	if err := n.isValidBucketName(bucket); err != nil {
		return bi, err
	}
	fi, eno := n.fs.Stat(mctx, n.path(bucket))
	if eno == 0 {
		bi = minio.BucketInfo{
			Name:    bucket,
			Created: time.Unix(fi.Atime()/1000, 0),
		}
	}
	return bi, jfsToObjectErr(ctx, eno, bucket)
}

// Ignores all reserved bucket names or invalid bucket names.
func isReservedOrInvalidBucket(bucketEntry string, strict bool) bool {
	if err := s3utils.CheckValidBucketName(bucketEntry); err != nil {
		return true
	}
	return bucketEntry == metaBucket
}

func (n *jfsObjects) ListBuckets(ctx context.Context) (buckets []minio.BucketInfo, err error) {
	if !n.gConf.MultiBucket {
		fi, eno := n.fs.Stat(mctx, "/")
		if eno != 0 {
			return nil, jfsToObjectErr(ctx, eno)
		}
		buckets = []minio.BucketInfo{{
			Name:    n.conf.Format.Name,
			Created: time.Unix(fi.Atime()/1000, 0),
		}}
		return buckets, nil
	}
	f, eno := n.fs.Open(mctx, sep, 0)
	if eno != 0 {
		return nil, jfsToObjectErr(ctx, eno)
	}
	defer f.Close(mctx)
	entries, eno := f.Readdir(mctx, 10000)
	if eno != 0 {
		return nil, jfsToObjectErr(ctx, eno)
	}

	for _, entry := range entries {
		// Ignore all reserved bucket names and invalid bucket names.
		if isReservedOrInvalidBucket(entry.Name(), false) || n.isValidBucketName(entry.Name()) != nil {
			continue
		}
		if entry.IsDir() {
			buckets = append(buckets, minio.BucketInfo{
				Name:    entry.Name(),
				Created: time.Unix(entry.(*fs.FileStat).Atime()/1000, 0),
			})
		}
	}

	// Sort bucket infos by bucket name.
	sort.Slice(buckets, func(i, j int) bool {
		return buckets[i].Name < buckets[j].Name
	})
	return buckets, nil
}

func (n *jfsObjects) isLeafDir(bucket, leafPath string) bool {
	return false
}

func (n *jfsObjects) isLeaf(bucket, leafPath string) bool {
	return !strings.HasSuffix(leafPath, "/")
}

func (n *jfsObjects) listDirFactory() minio.ListDirFunc {
	return func(bucket, prefixDir, prefixEntry string) (emptyDir bool, entries []*minio.Entry, delayIsLeaf bool) {
		f, eno := n.fs.Open(mctx, n.path(bucket, prefixDir), 0)
		if eno != 0 {
			return fs.IsNotExist(eno), nil, false
		}
		defer f.Close(mctx)
		if fi, _ := f.Stat(); fi.(*fs.FileStat).Atime() == 0 && prefixEntry == "" {
			entries = append(entries, &minio.Entry{Name: ""})
		}

		fis, eno := f.Readdir(mctx, 0)
		if eno != 0 {
			return
		}
		root := n.path(bucket, prefixDir) == "/"
		for _, fi := range fis {
			if root && (fi.Name() == metaBucket || fi.Name() == minio.MinioMetaBucket) {
				continue
			}
			if stat, ok := fi.(*fs.FileStat); ok && stat.IsSymlink() {
				var err syscall.Errno
				p := n.path(bucket, prefixDir, fi.Name())
				if fi, err = n.fs.Stat(mctx, p); err != 0 {
					logger.Errorf("stat %s: %s", p, err)
					continue
				}
			}
			entry := &minio.Entry{Name: fi.Name(),
				Info: &minio.ObjectInfo{
					Bucket:  bucket,
					Name:    fi.Name(),
					ModTime: fi.ModTime(),
					Size:    fi.Size(),
					IsDir:   fi.IsDir(),
					AccTime: fi.ModTime(),
				},
			}

			if fi.IsDir() {
				entry.Name += sep
				entry.Info.Size = 0
			}
			entries = append(entries, entry)
		}
		if len(entries) == 0 {
			return true, nil, false
		}
		entries, delayIsLeaf = minio.FilterListEntries(bucket, prefixDir, entries, prefixEntry, n.isLeaf)
		return false, entries, delayIsLeaf
	}
}

func (n *jfsObjects) checkBucket(ctx context.Context, bucket string) error {
	if err := n.isValidBucketName(bucket); err != nil {
		return err
	}
	bucketPath := n.path(bucket)
	if bucketPath != "/" { // no need to stat "/" in every request
		if _, eno := n.fs.Stat(mctx, bucketPath); eno != 0 {
			return jfsToObjectErr(ctx, eno, bucket)
		}
	}
	return nil
}

// ListObjects lists all blobs in JFS bucket filtered by prefix.
func (n *jfsObjects) ListObjects(ctx context.Context, bucket, prefix, marker, delimiter string, maxKeys int) (loi minio.ListObjectsInfo, err error) {
	if err := n.checkBucket(ctx, bucket); err != nil {
		return loi, err
	}
	getObjectInfo := func(ctx context.Context, bucket, object string, info *minio.ObjectInfo) (obj minio.ObjectInfo, err error) {
		var eno syscall.Errno
		if info == nil {
			var fi *fs.FileStat
			fi, eno = n.fs.Stat(mctx, n.path(bucket, object))
			if eno == 0 {
				size := fi.Size()
				if fi.IsDir() {
					size = 0
				}
				info = &minio.ObjectInfo{
					Bucket:  bucket,
					ModTime: fi.ModTime(),
					Size:    size,
					IsDir:   fi.IsDir(),
					AccTime: fi.ModTime(),
				}
			}

			// replace links to external file systems with empty files
			if errors.Is(eno, syscall.ENOTSUP) {
				now := time.Now()
				info = &minio.ObjectInfo{
					Bucket:  bucket,
					ModTime: now,
					Size:    0,
					IsDir:   false,
					AccTime: now,
				}
				eno = 0
			}
		}

		if info == nil {
			return obj, jfsToObjectErr(ctx, eno, bucket, object)
		}
		info.Name = object
		if n.gConf.KeepEtag && !strings.HasSuffix(object, sep) {
			etag, _ := n.fs.GetXattr(mctx, n.path(bucket, object), s3Etag)
			info.ETag = string(etag)
		}
		return *info, jfsToObjectErr(ctx, eno, bucket, object)
	}

	if maxKeys == 0 {
		maxKeys = -1 // list as many objects as possible
	}
	return minio.ListObjects(ctx, n, bucket, prefix, marker, delimiter, maxKeys, n.listPool, n.listDirFactory(), n.isLeaf, n.isLeafDir, getObjectInfo, getObjectInfo)
}

// ListObjectsV2 lists all blobs in JFS bucket filtered by prefix
func (n *jfsObjects) ListObjectsV2(ctx context.Context, bucket, prefix, continuationToken, delimiter string, maxKeys int,
	fetchOwner bool, startAfter string) (loi minio.ListObjectsV2Info, err error) {
	if err := n.isValidBucketName(bucket); err != nil {
		return minio.ListObjectsV2Info{}, err
	}
	// fetchOwner is not supported and unused.
	marker := continuationToken
	if marker == "" {
		marker = startAfter
	}
	resultV1, err := n.ListObjects(ctx, bucket, prefix, marker, delimiter, maxKeys)
	if err == nil {
		loi = minio.ListObjectsV2Info{
			Objects:               resultV1.Objects,
			Prefixes:              resultV1.Prefixes,
			ContinuationToken:     continuationToken,
			NextContinuationToken: resultV1.NextMarker,
			IsTruncated:           resultV1.IsTruncated,
		}
	}
	return loi, err
}

func (n *jfsObjects) setFileAtime(p string, atime int64) {
	if f, eno := n.fs.Open(mctx, p, 0); eno == 0 {
		defer f.Close(mctx)
		if eno := f.Utime(mctx, atime, -1); eno != 0 {
			logger.Warnf("set atime of %s: %s", p, eno)
		}
	} else if eno != syscall.ENOENT {
		logger.Warnf("open %s: %s", p, eno)
	}
}

func (n *jfsObjects) DeleteObject(ctx context.Context, bucket, object string, options minio.ObjectOptions) (info minio.ObjectInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	info.Bucket = bucket
	info.Name = object
	p := path.Clean(n.path(bucket, object))
	root := n.path(bucket)
	if strings.HasSuffix(object, sep) {
		// reset atime
		n.setFileAtime(p, time.Now().Unix())
	}
	for p != root {
		if eno := n.fs.Delete(mctx, p); eno != 0 {
			if fs.IsNotEmpty(eno) || fs.IsNotExist(eno) {
				err = nil
			} else {
				err = eno
			}
			break
		}
		p = path.Dir(p)
		if fi, _ := n.fs.Stat(mctx, p); fi == nil || fi.Atime() == 0 {
			break
		}
	}
	return info, jfsToObjectErr(ctx, err, bucket, object)
}

func (n *jfsObjects) DeleteObjects(ctx context.Context, bucket string, objects []minio.ObjectToDelete, options minio.ObjectOptions) (objs []minio.DeletedObject, errs []error) {
	objs = make([]minio.DeletedObject, len(objects))
	errs = make([]error, len(objects))
	for idx, object := range objects {
		_, errs[idx] = n.DeleteObject(ctx, bucket, object.ObjectName, options)
		if errs[idx] == nil {
			objs[idx] = minio.DeletedObject{
				ObjectName: object.ObjectName,
			}
		}
	}
	return
}

type fReader struct {
	*fs.File
}

func (f *fReader) Read(b []byte) (int, error) {
	return f.File.Read(mctx, b)
}

func (n *jfsObjects) GetObjectNInfo(ctx context.Context, bucket, object string, rs *minio.HTTPRangeSpec, h http.Header, lockType minio.LockType, opts minio.ObjectOptions) (gr *minio.GetObjectReader, err error) {
	objInfo, err := n.GetObjectInfo(ctx, bucket, object, opts)
	if err != nil {
		return nil, err
	}

	var startOffset, length int64
	startOffset, length, err = rs.GetOffsetLength(objInfo.Size)
	if err != nil {
		return
	}
	f, eno := n.fs.Open(mctx, n.path(bucket, object), vfs.MODE_MASK_R)
	if eno != 0 {
		return nil, jfsToObjectErr(ctx, eno, bucket, object)
	}
	_, _ = f.Seek(mctx, startOffset, 0)
	r := &io.LimitedReader{R: &fReader{f}, N: length}
	closer := func() { _ = f.Close(mctx) }
	return minio.NewGetObjectReaderFromReader(r, objInfo, opts, closer)
}

func (n *jfsObjects) CopyObject(ctx context.Context, srcBucket, srcObject, dstBucket, dstObject string, srcInfo minio.ObjectInfo, srcOpts, dstOpts minio.ObjectOptions) (info minio.ObjectInfo, err error) {
	if err = n.checkBucket(ctx, srcBucket); err != nil {
		return
	}
	if err = n.checkBucket(ctx, dstBucket); err != nil {
		return
	}
	dst := n.path(dstBucket, dstObject)
	src := n.path(srcBucket, srcObject)

	if minio.IsStringEqual(src, dst) {
		// if we copy the same object for set metadata
		err = n.setObjMeta(dst, srcInfo.UserDefined)
		if err != nil {
			logger.Errorf("set object metadata error, path: %s error %s", dst, err)
		}
		return n.GetObjectInfo(ctx, srcBucket, srcObject, minio.ObjectOptions{})
	}
	uuid := minio.MustGetUUID()
	tmp := n.tpath(dstBucket, "tmp", uuid[:subDirPrefix], uuid)
	f, eno := n.fs.Create(mctx, tmp, 0666, n.gConf.Umask)
	if eno == syscall.ENOENT {
		_ = n.mkdirAll(ctx, path.Dir(tmp))
		f, eno = n.fs.Create(mctx, tmp, 0666, n.gConf.Umask)
	}
	if eno != 0 {
		logger.Errorf("create %s: %s", tmp, eno)
		return
	}
	defer func() {
		_ = f.Close(mctx)
		if err != nil {
			_ = n.fs.Delete(mctx, tmp)
		}
	}()

	_, eno = n.fs.CopyFileRange(mctx, src, 0, tmp, 0, 1<<63)
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, srcBucket, srcObject)
		logger.Errorf("copy %s to %s: %s", src, tmp, err)
		return
	}

	var etag []byte
	if n.gConf.KeepEtag {
		etag, _ = n.fs.GetXattr(mctx, src, s3Etag)
		if len(etag) != 0 {
			eno = n.fs.SetXattr(mctx, tmp, s3Etag, etag, 0)
			if eno != 0 {
				logger.Warnf("set xattr error, path: %s,xattr: %s,value: %s,flags: %d", tmp, s3Etag, etag, 0)
			}
		}
	}

	var tagStr string
	if n.gConf.ObjTag && srcInfo.UserDefined != nil {
		if tagStr = srcInfo.UserDefined[xhttp.AmzObjectTagging]; tagStr != "" {
			if eno := n.fs.SetXattr(mctx, tmp, s3Tags, []byte(tagStr), 0); eno != 0 {
				logger.Errorf("set object tags error, path: %s, value: %s error %s", tmp, tagStr, eno)
			}
		}
	}
	err = n.setObjMeta(tmp, srcInfo.UserDefined)
	if err != nil {
		logger.Errorf("set object metadata error, path: %s error %s", dst, err)
	}

	eno = n.fs.Rename(mctx, tmp, dst, 0)
	if eno == syscall.ENOENT {
		if err = n.mkdirAll(ctx, path.Dir(dst)); err != nil {
			logger.Errorf("mkdirAll %s: %s", path.Dir(dst), err)
			err = jfsToObjectErr(ctx, err, dstBucket, dstObject)
			return
		}
		eno = n.fs.Rename(mctx, tmp, dst, 0)
	}
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, srcBucket, srcObject)
		logger.Errorf("rename %s to %s: %s", tmp, dst, err)
		return
	}
	fi, eno := n.fs.Stat(mctx, dst)
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, dstBucket, dstObject)
		return
	}

	return minio.ObjectInfo{
		Bucket:      dstBucket,
		Name:        dstObject,
		ETag:        string(etag),
		ModTime:     fi.ModTime(),
		Size:        fi.Size(),
		IsDir:       fi.IsDir(),
		AccTime:     fi.ModTime(),
		UserTags:    tagStr,
		UserDefined: minio.CleanMetadata(srcInfo.UserDefined),
	}, nil
}

var buffPool = sync.Pool{
	New: func() interface{} {
		buf := make([]byte, 1<<17)
		return &buf
	},
}

func (n *jfsObjects) GetObject(ctx context.Context, bucket, object string, startOffset, length int64, writer io.Writer, etag string, opts minio.ObjectOptions) (err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	f, eno := n.fs.Open(mctx, n.path(bucket, object), vfs.MODE_MASK_R)
	if eno != 0 {
		return jfsToObjectErr(ctx, eno, bucket, object)
	}
	defer func() { _ = f.Close(mctx) }()
	var buf = buffPool.Get().(*[]byte)
	defer buffPool.Put(buf)
	_, _ = f.Seek(mctx, startOffset, 0)
	for length > 0 {
		l := int64(len(*buf))
		if l > length {
			l = length
		}
		n, e := f.Read(mctx, (*buf)[:l])
		if n == 0 {
			if e != io.EOF {
				err = e
			}
			break
		}
		if _, err = writer.Write((*buf)[:n]); err != nil {
			break
		}
		length -= int64(n)
	}
	return jfsToObjectErr(ctx, err, bucket, object)
}

func (n *jfsObjects) GetObjectInfo(ctx context.Context, bucket, object string, opts minio.ObjectOptions) (objInfo minio.ObjectInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	fi, eno := n.fs.Stat(mctx, n.path(bucket, object))
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, bucket, object)
		return
	}
	// put /dir1/key1; head /dir1 return 404; head /dir1/ return 404; head /dir1/key1 return 200
	// put /dir1/key1/; head /dir1/key1 return 404; head /dir1/key1/ return 200
	var isObject bool
	if strings.HasSuffix(object, sep) && fi.IsDir() && fi.Atime() == 0 {
		isObject = true
	} else if !strings.HasSuffix(object, sep) && !fi.IsDir() {
		isObject = true
	}
	if !isObject {
		err = jfsToObjectErr(ctx, syscall.ENOENT, bucket, object)
		return
	}
	var etag []byte
	if n.gConf.KeepEtag && !fi.IsDir() {
		etag, _ = n.fs.GetXattr(mctx, n.path(bucket, object), s3Etag)
	}
	size := fi.Size()
	if fi.IsDir() {
		size = 0
	}
	// key1=value1&key2=value2
	var tagStr []byte
	if n.gConf.ObjTag {
		var errno syscall.Errno
		if tagStr, errno = n.fs.GetXattr(mctx, n.path(bucket, object), s3Tags); errno != 0 && errno != meta.ENOATTR {
			return minio.ObjectInfo{}, errno
		}
	}
	objMeta, err := n.getObjMeta(n.path(bucket, object))
	if err != nil {
		return minio.ObjectInfo{}, err
	}
	if opts.UserDefined == nil {
		opts.UserDefined = make(map[string]string)
	}
	for k, v := range objMeta {
		opts.UserDefined[k] = v
	}
	contentType := utils.GuessMimeType(object)
	if c, exist := objMeta["content-type"]; exist && len(c) > 0 {
		contentType = c
	}
	return minio.ObjectInfo{
		Bucket:      bucket,
		Name:        object,
		ModTime:     fi.ModTime(),
		Size:        size,
		IsDir:       fi.IsDir(),
		AccTime:     fi.ModTime(),
		ETag:        string(etag),
		ContentType: contentType,
		UserTags:    string(tagStr),
		UserDefined: minio.CleanMetadata(opts.UserDefined),
	}, nil
}

func (n *jfsObjects) mkdirAll(ctx context.Context, p string) error {
	if fi, eno := n.fs.Stat(mctx, p); eno == 0 {
		if !fi.IsDir() {
			return fmt.Errorf("%s is not directory", p)
		}
		return nil
	}
	eno := n.fs.Mkdir(mctx, p, 0777, n.gConf.Umask)
	if eno != 0 && fs.IsNotExist(eno) {
		if err := n.mkdirAll(ctx, path.Dir(p)); err != nil {
			return err
		}
		eno = n.fs.Mkdir(mctx, p, 0777, n.gConf.Umask)
	}
	if eno != 0 && fs.IsExist(eno) {
		eno = 0
	}
	if eno == 0 {
		return nil
	}
	return eno
}

func (n *jfsObjects) putObject(ctx context.Context, bucket, object string, r *minio.PutObjReader, opts minio.ObjectOptions, applyObjTaggingFunc func(tmpName string)) (err error) {
	uuid := minio.MustGetUUID()
	tmpname := n.tpath(bucket, "tmp", uuid[:subDirPrefix], uuid)
	f, eno := n.fs.Create(mctx, tmpname, 0666, n.gConf.Umask)
	if eno == syscall.ENOENT {
		_ = n.mkdirAll(ctx, path.Dir(tmpname))
		f, eno = n.fs.Create(mctx, tmpname, 0666, n.gConf.Umask)
	}
	if eno != 0 {
		logger.Errorf("create %s: %s", tmpname, eno)
		err = eno
		return
	}
	defer func() {
		if err != nil {
			_ = n.fs.Delete(mctx, tmpname)
		}
	}()
	var buf = buffPool.Get().(*[]byte)
	defer buffPool.Put(buf)
	for {
		var n int
		n, err = io.ReadFull(r, *buf)
		if n == 0 {
			if err == io.EOF {
				err = nil
			}
			break
		}
		_, eno := f.Write(mctx, (*buf)[:n])
		if eno != 0 {
			err = eno
			break
		}
	}
	if err == nil {
		eno = f.Close(mctx)
		if eno != 0 {
			err = eno
		}
	} else {
		_ = f.Close(mctx)
	}
	if err != nil {
		return
	}

	applyObjTaggingFunc(tmpname)

	eno = n.fs.Rename(mctx, tmpname, object, 0)
	if eno == syscall.ENOENT {
		if err = n.mkdirAll(ctx, path.Dir(object)); err != nil {
			logger.Errorf("mkdirAll %s: %s", path.Dir(object), err)
			err = jfsToObjectErr(ctx, err, bucket, object)
			return
		}
		eno = n.fs.Rename(mctx, tmpname, object, 0)
	}
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, bucket, object)
	}
	return
}

func (n *jfsObjects) PutObject(ctx context.Context, bucket string, object string, r *minio.PutObjReader, opts minio.ObjectOptions) (objInfo minio.ObjectInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	var tagStr string
	var etag string
	p := n.path(bucket, object)
	if strings.HasSuffix(object, sep) {
		if err = n.mkdirAll(ctx, p); err != nil {
			err = jfsToObjectErr(ctx, err, bucket, object)
			return
		}
		if r.Size() > 0 {
			err = minio.ObjectExistsAsDirectory{
				Bucket: bucket,
				Object: object,
				Err:    syscall.EEXIST,
			}
			return
		}
		// if the put object is a directory, set its atime to 0
		n.setFileAtime(p, 0)
	} else {
		if err = n.putObject(ctx, bucket, p, r, opts, func(tmpName string) {
			etag = r.MD5CurrentHexString()
			if n.gConf.KeepEtag && !strings.HasSuffix(object, sep) {
				if eno := n.fs.SetXattr(mctx, tmpName, s3Etag, []byte(etag), 0); eno != 0 {
					logger.Errorf("set xattr error, path: %s,xattr: %s,value: %s,flags: %d", tmpName, s3Etag, etag, 0)
				}
			}
			// tags: key1=value1&key2=value2&key3=value3
			if n.gConf.ObjTag && opts.UserDefined != nil {
				if tagStr = opts.UserDefined[xhttp.AmzObjectTagging]; tagStr != "" {
					if eno := n.fs.SetXattr(mctx, tmpName, s3Tags, []byte(tagStr), 0); eno != 0 {
						logger.Errorf("set object tags error, path: %s, value: %s error: %s", tmpName, tagStr, eno)
					}
				}
			}
			err = n.setObjMeta(tmpName, opts.UserDefined)
			if err != nil {
				logger.Errorf("set object metadata error, path: %s error %s", p, err)
			}
		}); err != nil {
			return
		}
	}
	fi, eno := n.fs.Stat(mctx, p)
	if eno != 0 {
		return objInfo, jfsToObjectErr(ctx, eno, bucket, object)
	}

	return minio.ObjectInfo{
		Bucket:      bucket,
		Name:        object,
		ETag:        etag,
		ModTime:     fi.ModTime(),
		Size:        fi.Size(),
		IsDir:       fi.IsDir(),
		AccTime:     fi.ModTime(),
		UserTags:    tagStr,
		UserDefined: minio.CleanMetadata(opts.UserDefined),
	}, nil
}

func (n *jfsObjects) NewMultipartUpload(ctx context.Context, bucket string, object string, opts minio.ObjectOptions) (uploadID string, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	uploadID = minio.MustGetUUID()
	p := n.upath(bucket, uploadID)
	err = n.mkdirAll(ctx, p)
	if err == nil {
		eno := n.fs.SetXattr(mctx, p, uploadKeyName, []byte(object), 0)
		if eno != 0 {
			logger.Warnf("set object %s on upload %s: %s", object, uploadID, eno)
		}
		if n.gConf.ObjTag && opts.UserDefined != nil {
			if tagStr := opts.UserDefined[xhttp.AmzObjectTagging]; tagStr != "" {
				if eno := n.fs.SetXattr(mctx, p, s3Tags, []byte(tagStr), 0); eno != 0 {
					logger.Errorf("set object tags error, path: %s, value: %s errors: %s", p, tagStr, eno)
				}
			}
		}
		err = n.setObjMeta(p, opts.UserDefined)
		if err != nil {
			logger.Errorf("set object metadata error, path: %s  error %s", p, err)
		}
	}
	return
}

const uploadKeyName = "s3-object"
const s3Etag = "s3-etag"

// less than 64k ref: https://docs.aws.amazon.com/AWSEC2/latest/UserGuide/Using_Tags.html#tag-restrictions
const s3Tags = "s3-tags"

// S3 object metadata
const s3Meta = "s3-meta"
const amzMeta = "x-amz-meta-"

var s3UserControlledSystemMeta = []string{
	"cache-control",
	"content-disposition",
	"content-type",
}

func (n *jfsObjects) getObjMeta(p string) (objMeta map[string]string, err error) {
	if n.gConf.ObjMeta {
		var errno syscall.Errno
		var metadataStr []byte
		if metadataStr, errno = n.fs.GetXattr(mctx, p, s3Meta); errno != 0 && errno != meta.ENOATTR {
			return objMeta, errno
		}
		if len(metadataStr) > 0 {
			err = json.Unmarshal(metadataStr, &objMeta)
			return objMeta, err
		}
	} else {
		objMeta = make(map[string]string)
	}
	return objMeta, nil
}

func (n *jfsObjects) setObjMeta(p string, metadata map[string]string) error {
	if n.gConf.ObjMeta && metadata != nil {
		meta := make(map[string]string)
		for k, v := range metadata {
			k = strings.ToLower(k)
			if strings.HasPrefix(k, amzMeta) {
				meta[k] = v
			} else {
				for _, systemMetaKey := range s3UserControlledSystemMeta {
					if k == systemMetaKey {
						meta[k] = v
						break
					}
				}
			}
		}
		if len(meta) > 0 {
			s3MetadataValue, err := json.Marshal(meta)
			if err != nil {
				return err
			}
			if eno := n.fs.SetXattr(mctx, p, s3Meta, s3MetadataValue, 0); eno != 0 {
				logger.Errorf("set object metadata error, path: %s,value: %s error: %s", p, string(s3Meta), eno)
			}
		}
	}
	return nil
}

func (n *jfsObjects) ListMultipartUploads(ctx context.Context, bucket string, prefix string, keyMarker string, uploadIDMarker string, delimiter string, maxUploads int) (lmi minio.ListMultipartsInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	f, eno := n.fs.Open(mctx, n.tpath(bucket, "uploads"), 0)
	if eno != 0 {
		return // no found
	}
	defer f.Close(mctx)
	parents, eno := f.ReaddirPlus(mctx, 0)
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, bucket)
		return
	}
	lmi.Prefix = prefix
	lmi.KeyMarker = keyMarker
	lmi.UploadIDMarker = uploadIDMarker
	lmi.MaxUploads = maxUploads
	lmi.Delimiter = delimiter
	commPrefixSet := make(map[string]struct{})
	for _, p := range parents {
		f, eno := n.fs.Open(mctx, n.tpath(bucket, "uploads", string(p.Name)), 0)
		if eno != 0 {
			return
		}
		defer f.Close(mctx)
		entries, eno := f.ReaddirPlus(mctx, 0)
		if eno != 0 {
			err = jfsToObjectErr(ctx, eno, bucket)
			return
		}

		for _, e := range entries {
			if len(e.Name) != 36 {
				continue // not an uuid
			}
			uploadID := string(e.Name)
			// todo: parallel
			object_, eno := n.fs.GetXattr(mctx, n.upath(bucket, uploadID), uploadKeyName)
			if eno != 0 {
				logger.Warnf("get object xattr error %s: %s, ignore this item", n.upath(bucket, uploadID), eno)
				continue
			}
			object := string(object_)
			if strings.HasPrefix(object, prefix) {
				if keyMarker != "" && object+uploadID > keyMarker+uploadIDMarker || keyMarker == "" {
					lmi.Uploads = append(lmi.Uploads, minio.MultipartInfo{
						Object:    object,
						UploadID:  uploadID,
						Initiated: time.Unix(e.Attr.Atime, int64(e.Attr.Atimensec)),
					})
				}
			}
		}
	}

	sort.Slice(lmi.Uploads, func(i, j int) bool {
		if lmi.Uploads[i].Object == lmi.Uploads[j].Object {
			return lmi.Uploads[i].UploadID < lmi.Uploads[j].UploadID
		} else {
			return lmi.Uploads[i].Object < lmi.Uploads[j].Object
		}
	})

	if delimiter != "" {
		var tmp []minio.MultipartInfo
		for _, info := range lmi.Uploads {
			if maxUploads == 0 {
				lmi.IsTruncated = true
				break
			}
			index := strings.Index(strings.TrimPrefix(info.Object, prefix), delimiter)
			if index == -1 {
				tmp = append(tmp, info)
				maxUploads--
			} else {
				commPrefix := info.Object[:index+1]
				if _, ok := commPrefixSet[commPrefix]; ok {
					continue
				}
				commPrefixSet[commPrefix] = struct{}{}
				maxUploads--
			}
		}
		lmi.Uploads = tmp
		for prefix := range commPrefixSet {
			lmi.CommonPrefixes = append(lmi.CommonPrefixes, prefix)
		}
		sort.Strings(lmi.CommonPrefixes)
	} else if len(lmi.Uploads) > maxUploads {
		lmi.IsTruncated = true
		lmi.Uploads = lmi.Uploads[:maxUploads]
	}

	if len(lmi.Uploads) != 0 {
		lmi.NextKeyMarker = lmi.Uploads[len(lmi.Uploads)-1].Object
		lmi.NextUploadIDMarker = lmi.Uploads[len(lmi.Uploads)-1].UploadID
	}
	return lmi, jfsToObjectErr(ctx, err, bucket)
}

func (n *jfsObjects) checkUploadIDExists(ctx context.Context, bucket, object, uploadID string) (err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	_, eno := n.fs.Stat(mctx, n.upath(bucket, uploadID))
	return jfsToObjectErr(ctx, eno, bucket, object, uploadID)
}

func (n *jfsObjects) ListObjectParts(ctx context.Context, bucket, object, uploadID string, partNumberMarker int, maxParts int, opts minio.ObjectOptions) (result minio.ListPartsInfo, err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return result, err
	}
	f, e := n.fs.Open(mctx, n.upath(bucket, uploadID), 0)
	if e != 0 {
		err = jfsToObjectErr(ctx, e, bucket, object, uploadID)
		return
	}
	defer func() { _ = f.Close(mctx) }()
	entries, e := f.ReaddirPlus(mctx, 0)
	if e != 0 {
		err = jfsToObjectErr(ctx, e, bucket, object, uploadID)
		return
	}
	result.Bucket = bucket
	result.Object = object
	result.UploadID = uploadID
	result.PartNumberMarker = partNumberMarker
	result.MaxParts = maxParts
	for _, entry := range entries {
		num, er := strconv.Atoi(string(entry.Name))
		if er == nil && num > partNumberMarker {
			etag, _ := n.fs.GetXattr(mctx, n.ppath(bucket, uploadID, string(entry.Name)), s3Etag)
			result.Parts = append(result.Parts, minio.PartInfo{
				PartNumber:   num,
				Size:         int64(entry.Attr.Length),
				LastModified: time.Unix(entry.Attr.Mtime, 0),
				ETag:         string(etag),
			})
		}
	}
	sort.Slice(result.Parts, func(i, j int) bool {
		return result.Parts[i].PartNumber < result.Parts[j].PartNumber
	})
	if len(result.Parts) > maxParts {
		result.IsTruncated = true
		result.Parts = result.Parts[:maxParts]
		result.NextPartNumberMarker = result.Parts[maxParts-1].PartNumber
	}
	return
}

func (n *jfsObjects) CopyObjectPart(ctx context.Context, srcBucket, srcObject, dstBucket, dstObject, uploadID string, partID int,
	startOffset int64, length int64, srcInfo minio.ObjectInfo, srcOpts, dstOpts minio.ObjectOptions) (result minio.PartInfo, err error) {
	if err = n.isValidBucketName(srcBucket); err != nil {
		return
	}
	if err = n.checkUploadIDExists(ctx, dstBucket, dstObject, uploadID); err != nil {
		return
	}
	// TODO: use CopyFileRange
	return n.PutObjectPart(ctx, dstBucket, dstObject, uploadID, partID, srcInfo.PutObjReader, dstOpts)
}

func (n *jfsObjects) PutObjectPart(ctx context.Context, bucket, object, uploadID string, partID int, r *minio.PutObjReader, opts minio.ObjectOptions) (info minio.PartInfo, err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return
	}
	p := n.ppath(bucket, uploadID, strconv.Itoa(partID))
	var etag string
	if err = n.putObject(ctx, bucket, p, r, opts, func(tmpName string) {
		etag = r.MD5CurrentHexString()
		if n.fs.SetXattr(mctx, tmpName, s3Etag, []byte(etag), 0) != 0 {
			logger.Warnf("set xattr error, path: %s,xattr: %s,value: %s,flags: %d", tmpName, s3Etag, etag, 0)
		}
	}); err != nil {
		err = jfsToObjectErr(ctx, err, bucket, object)
		return
	}
	info.PartNumber = partID
	info.ETag = etag
	info.LastModified = minio.UTCNow()
	info.Size = r.Reader.Size()
	return
}

func (n *jfsObjects) GetMultipartInfo(ctx context.Context, bucket, object, uploadID string, opts minio.ObjectOptions) (result minio.MultipartInfo, err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return
	}
	result.Bucket = bucket
	result.Object = object
	result.UploadID = uploadID
	return
}

func (n *jfsObjects) CompleteMultipartUpload(ctx context.Context, bucket, object, uploadID string, parts []minio.CompletePart, opts minio.ObjectOptions) (objInfo minio.ObjectInfo, err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return
	}

	tmp := n.ppath(bucket, uploadID, "complete")
	_ = n.fs.Delete(mctx, tmp)
	f, eno := n.fs.Create(mctx, tmp, 0666, n.gConf.Umask)
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, bucket, object, uploadID)
		logger.Errorf("create complete: %s", err)
		return
	}
	defer func() {
		_ = f.Close(mctx)
	}()
	var total uint64
	for _, part := range parts {
		p := n.ppath(bucket, uploadID, strconv.Itoa(part.PartNumber))
		copied, eno := n.fs.CopyFileRange(mctx, p, 0, tmp, total, 5<<30)
		if eno == syscall.ENOENT { // try lookup from old path
			p = n.ppathFlat(bucket, uploadID, strconv.Itoa(part.PartNumber))
			copied, eno = n.fs.CopyFileRange(mctx, p, 0, tmp, total, 5<<30)
		}
		if eno != 0 {
			err = jfsToObjectErr(ctx, eno, bucket, object, uploadID)
			logger.Errorf("merge parts: %s", err)
			return
		}
		total += copied
	}

	// Calculate s3 compatible md5sum for complete multipart.
	s3MD5 := minio.ComputeCompleteMultipartMD5(parts)
	if n.gConf.KeepEtag {
		eno = n.fs.SetXattr(mctx, tmp, s3Etag, []byte(s3MD5), 0)
		if eno != 0 {
			logger.Warnf("set xattr error, path: %s,xattr: %s,value: %s,flags: %d", tmp, s3Etag, s3MD5, 0)
		}
	}

	var tagStr []byte
	if n.gConf.ObjTag {
		var eno syscall.Errno
		if tagStr, eno = n.fs.GetXattr(mctx, n.upath(bucket, uploadID), s3Tags); eno != 0 {
			if eno != meta.ENOATTR {
				logger.Errorf("get object tags error, path: %s, error: %s", n.upath(bucket, uploadID), eno)
			}
		} else if eno = n.fs.SetXattr(mctx, tmp, s3Tags, tagStr, 0); eno != 0 {
			logger.Errorf("set object tags error, path: %s, tags: %s, error: %s", tmp, string(tagStr), eno)
		}
	}

	var objMeta map[string]string
	if n.gConf.ObjMeta {
		if objMeta, err = n.getObjMeta(n.upath(bucket, uploadID)); err != nil {
			logger.Errorf("get object meta error, path: %s, error: %s", n.upath(bucket, uploadID), err)
		} else if err = n.setObjMeta(tmp, objMeta); err != nil {
			logger.Errorf("set object meta error, path: %s, error: %s", tmp, err)
		}
	}

	name := n.path(bucket, object)
	eno = n.fs.Rename(mctx, tmp, name, 0)
	if eno == syscall.ENOENT {
		if err = n.mkdirAll(ctx, path.Dir(name)); err != nil {
			logger.Errorf("mkdirAll %s: %s", path.Dir(name), err)
			_ = n.fs.Delete(mctx, tmp)
			err = jfsToObjectErr(ctx, err, bucket, object, uploadID)
			return
		}
		eno = n.fs.Rename(mctx, tmp, name, 0)
	}
	if eno != 0 {
		_ = n.fs.Delete(mctx, tmp)
		err = jfsToObjectErr(ctx, eno, bucket, object, uploadID)
		logger.Errorf("Rename %s -> %s: %s", tmp, name, err)
		return
	}

	fi, eno := n.fs.Stat(mctx, name)
	if eno != 0 {
		_ = n.fs.Delete(mctx, name)
		err = jfsToObjectErr(ctx, eno, bucket, object, uploadID)
		return
	}

	// remove parts
	_ = n.fs.Rmr(mctx, n.upath(bucket, uploadID), meta.RmrDefaultThreads)
	return minio.ObjectInfo{
		Bucket:      bucket,
		Name:        object,
		ETag:        s3MD5,
		ModTime:     fi.ModTime(),
		Size:        fi.Size(),
		IsDir:       fi.IsDir(),
		AccTime:     fi.ModTime(),
		UserTags:    string(tagStr),
		UserDefined: minio.CleanMetadata(opts.UserDefined),
	}, nil
}

func (n *jfsObjects) AbortMultipartUpload(ctx context.Context, bucket, object, uploadID string, option minio.ObjectOptions) (err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return
	}
	eno := n.fs.Rmr(mctx, n.upath(bucket, uploadID), meta.RmrDefaultThreads)
	return jfsToObjectErr(ctx, eno, bucket, object, uploadID)
}

func (n *jfsObjects) cleanup() {
	for range time.Tick(24 * time.Hour) {
		// default bucket tmp dirs
		tmpDirs := []string{".sys/tmp/", ".sys/uploads/"}
		if n.gConf.MultiBucket {
			buckets, err := n.ListBuckets(context.Background())
			if err != nil {
				logger.Errorf("list buckets error: %v", err)
				continue
			}
			for _, bucket := range buckets {
				tmpDirs = append(tmpDirs, fmt.Sprintf(".sys/%s/tmp", bucket.Name))
				tmpDirs = append(tmpDirs, fmt.Sprintf(".sys/%s/uploads", bucket.Name))
			}
		}
		for _, dir := range tmpDirs {
			n.cleanupDir(dir)
		}
	}
}

func (n *jfsObjects) cleanupDir(dir string) bool {
	f, errno := n.fs.Open(mctx, dir, 0)
	if errno != 0 {
		return false
	}
	defer f.Close(mctx)
	entries, _ := f.ReaddirPlus(mctx, 0)
	now := time.Now()
	deleted := 0
	for _, entry := range entries {
		dirPath := n.path(dir, string(entry.Name))
		if entry.Attr.Typ == meta.TypeDirectory && len(entry.Name) == subDirPrefix {
			if !n.cleanupDir(strings.TrimPrefix(dirPath, "/")) {
				continue
			}
		} else if _, err := uuid.Parse(string(entry.Name)); err != nil {
			logger.Warnf("unexpected file path: %s", dirPath)
			continue
		}
		if now.Sub(time.Unix(entry.Attr.Mtime, 0)) > 7*24*time.Hour {
			if errno = n.fs.Rmr(mctx, dirPath, meta.RmrDefaultThreads); errno != 0 {
				logger.Errorf("failed to delete expired temporary files path: %s, err: %s", dirPath, errno)
			} else {
				deleted += 1
				logger.Infof("delete expired temporary files path: %s, mtime: %s", dirPath, time.Unix(entry.Attr.Mtime, 0).Format(time.RFC3339))
			}
		}
	}
	return deleted == len(entries)
}

type jfsFLock struct {
	inode     meta.Ino
	owner     uint64
	meta      meta.Meta
	localLock sync.RWMutex
}

func (j *jfsFLock) GetLock(ctx context.Context, timeout *minio.DynamicTimeout) (newCtx context.Context, timedOutErr error) {
	return j.getFlockWithTimeOut(ctx, meta.F_WRLCK, timeout)
}

func (j *jfsFLock) getFlockWithTimeOut(ctx context.Context, ltype uint32, timeout *minio.DynamicTimeout) (context.Context, error) {
	if os.Getenv("JUICEFS_META_READ_ONLY") != "" {
		return ctx, nil
	}
	if j.inode == 0 {
		logger.Warnf("failed to get lock")
		return ctx, nil
	}
	start := time.Now()
	deadline := start.Add(timeout.Timeout())
	lockStr := "write"

	var getLockFunc func() bool
	var unlockFunc func()
	var getLock bool
	if ltype == meta.F_RDLCK {
		getLockFunc = j.localLock.TryRLock
		unlockFunc = j.localLock.RUnlock
		lockStr = "read"
	} else {
		getLockFunc = j.localLock.TryLock
		unlockFunc = j.localLock.Unlock
	}

	for {
		getLock = getLockFunc()
		if getLock {
			break
		}
		if time.Now().After(deadline) {
			timeout.LogFailure()
			logger.Errorf("get %s lock timed out ino:%d", lockStr, j.inode)
			return ctx, minio.OperationTimedOut{}
		}
		time.Sleep(5 * time.Millisecond)
	}

	for {
		if errno := j.meta.Flock(mctx, j.inode, j.owner, ltype, false); errno != 0 {
			if !errors.Is(errno, syscall.EAGAIN) {
				logger.Errorf("failed to get %s lock for inode %d by owner %d, error : %s", lockStr, j.inode, j.owner, errno)
			}
		} else {
			timeout.LogSuccess(time.Since(start))
			return ctx, nil
		}

		if time.Now().After(deadline) {
			unlockFunc()
			timeout.LogFailure()
			logger.Errorf("get %s lock timed out ino:%d", lockStr, j.inode)
			return ctx, minio.OperationTimedOut{}
		}
		time.Sleep(5 * time.Millisecond)
	}
}

func (j *jfsFLock) Unlock() {
	if j.inode == 0 || os.Getenv("JUICEFS_META_READ_ONLY") != "" {
		return
	}
	if errno := j.meta.Flock(mctx, j.inode, j.owner, meta.F_UNLCK, true); errno != 0 {
		logger.Errorf("failed to release lock for inode %d by owner %d, error : %s", j.inode, j.owner, errno)
	}
	j.localLock.Unlock()
}

func (j *jfsFLock) GetRLock(ctx context.Context, timeout *minio.DynamicTimeout) (newCtx context.Context, timedOutErr error) {
	return j.getFlockWithTimeOut(ctx, meta.F_RDLCK, timeout)
}

func (j *jfsFLock) RUnlock() {
	if j.inode == 0 || os.Getenv("JUICEFS_META_READ_ONLY") != "" {
		return
	}
	if errno := j.meta.Flock(mctx, j.inode, j.owner, meta.F_UNLCK, true); errno != 0 {
		logger.Errorf("failed to release lock for inode %d by owner %d, error : %s", j.inode, j.owner, errno)
	}
	j.localLock.RUnlock()
}

func (n *jfsObjects) NewNSLock(bucket string, objects ...string) minio.RWLocker {
	if os.Getenv("JUICEFS_META_READ_ONLY") != "" {
		return &jfsFLock{}
	}
	if len(objects) != 1 {
		panic(fmt.Errorf("jfsObjects.NewNSLock: the length of the objects parameter must be 1, current %s", objects))
	}

	lockfile := path.Join(minio.MinioMetaBucket, minio.MinioMetaLockFile)
	var file *fs.File
	var errno syscall.Errno
	file, errno = n.fs.Open(mctx, lockfile, vfs.MODE_MASK_W)
	if errno != 0 && !errors.Is(errno, syscall.ENOENT) {
		logger.Errorf("failed to open the file to be locked: %s error %s", lockfile, errno)
		return &jfsFLock{}
	}
	if errors.Is(errno, syscall.ENOENT) {
		if file, errno = n.fs.Create(mctx, lockfile, 0666, n.gConf.Umask); errno != 0 {
			if errors.Is(errno, syscall.EEXIST) {
				if file, errno = n.fs.Open(mctx, lockfile, vfs.MODE_MASK_W); errno != 0 {
					logger.Errorf("failed to open the file to be locked: %s error %s", lockfile, errno)
					return &jfsFLock{}
				}
			} else {
				logger.Errorf("failed to create gateway lock file err %s", errno)
				return &jfsFLock{}
			}
		}
	}
	defer file.Close(mctx)
	return &jfsFLock{owner: n.conf.Meta.Sid, inode: file.Inode(), meta: n.fs.Meta()}
}

func (n *jfsObjects) BackendInfo() madmin.BackendInfo {
	return madmin.BackendInfo{Type: madmin.FS}
}

func (n *jfsObjects) LocalStorageInfo(ctx context.Context) (minio.StorageInfo, []error) {
	return n.StorageInfo(ctx)
}

func (n *jfsObjects) ListObjectVersions(ctx context.Context, bucket, prefix, marker, versionMarker, delimiter string, maxKeys int) (loi minio.ListObjectVersionsInfo, err error) {
	return loi, minio.NotImplemented{}
}

func (n *jfsObjects) getObjectInfoNoFSLock(ctx context.Context, bucket, object string, info *minio.ObjectInfo) (oi minio.ObjectInfo, e error) {
	return n.GetObjectInfo(ctx, bucket, object, minio.ObjectOptions{})
}

func (n *jfsObjects) Walk(ctx context.Context, bucket, prefix string, results chan<- minio.ObjectInfo, opts minio.ObjectOptions) error {
	return minio.FsWalk(ctx, n, bucket, prefix, n.listDirFactory(), n.isLeaf, n.isLeafDir, results, n.getObjectInfoNoFSLock, n.getObjectInfoNoFSLock)
}

func (n *jfsObjects) SetBucketPolicy(ctx context.Context, bucket string, policy *policy.Policy) error {
	meta, err := minio.LoadBucketMetadata(ctx, n, bucket)
	if err != nil {
		return err
	}

	json := jsoniter.ConfigCompatibleWithStandardLibrary
	configData, err := json.Marshal(policy)
	if err != nil {
		return err
	}
	meta.PolicyConfigJSON = configData

	return meta.Save(ctx, n)
}

func (n *jfsObjects) GetBucketPolicy(ctx context.Context, bucket string) (*policy.Policy, error) {
	meta, err := minio.LoadBucketMetadata(ctx, n, bucket)
	if err != nil {
		return nil, err
	}
	if meta.PolicyConfig == nil {
		return nil, minio.BucketPolicyNotFound{Bucket: bucket}
	}
	return meta.PolicyConfig, nil
}

func (n *jfsObjects) DeleteBucketPolicy(ctx context.Context, bucket string) error {
	meta, err := minio.LoadBucketMetadata(ctx, n, bucket)
	if err != nil {
		return err
	}
	meta.PolicyConfigJSON = nil
	return meta.Save(ctx, n)
}

func (n *jfsObjects) SetDriveCounts() []int {
	return nil
}

func (n *jfsObjects) HealFormat(ctx context.Context, dryRun bool) (madmin.HealResultItem, error) {
	return madmin.HealResultItem{}, minio.NotImplemented{}
}

func (n *jfsObjects) HealBucket(ctx context.Context, bucket string, opts madmin.HealOpts) (madmin.HealResultItem, error) {
	return madmin.HealResultItem{}, minio.NotImplemented{}
}

func (n *jfsObjects) HealObject(ctx context.Context, bucket, object, versionID string, opts madmin.HealOpts) (res madmin.HealResultItem, err error) {
	return res, minio.NotImplemented{}
}

func (n *jfsObjects) HealObjects(ctx context.Context, bucket, prefix string, opts madmin.HealOpts, fn minio.HealObjectFn) error {
	return minio.NotImplemented{}
}

func (n *jfsObjects) GetMetrics(ctx context.Context) (*minio.BackendMetrics, error) {
	return &minio.BackendMetrics{}, minio.NotImplemented{}
}

func (n *jfsObjects) Health(ctx context.Context, opts minio.HealthOptions) minio.HealthResult {
	if _, errno := n.fs.Stat(mctx, minio.MinioMetaBucket); errno != 0 {
		return minio.HealthResult{}
	}
	return minio.HealthResult{
		Healthy: true,
	}
}

func (n *jfsObjects) ReadHealth(ctx context.Context) bool {
	_, errno := n.fs.Stat(mctx, minio.MinioMetaBucket)
	return errno == 0
}

func (n *jfsObjects) PutObjectTags(ctx context.Context, bucket, object string, tags string, opts minio.ObjectOptions) (minio.ObjectInfo, error) {
	if !n.gConf.ObjTag {
		return minio.ObjectInfo{}, minio.NotImplemented{}
	}
	if eno := n.fs.SetXattr(mctx, n.path(bucket, object), s3Tags, []byte(tags), 0); eno != 0 {
		return minio.ObjectInfo{}, eno
	}
	return n.GetObjectInfo(ctx, bucket, object, opts)
}

func (n *jfsObjects) GetObjectTags(ctx context.Context, bucket, object string, opts minio.ObjectOptions) (*tags.Tags, error) {
	if !n.gConf.ObjTag {
		return nil, minio.NotImplemented{}
	}
	oi, err := n.GetObjectInfo(ctx, bucket, object, minio.ObjectOptions{})
	if err != nil {
		return nil, err
	}

	return tags.ParseObjectTags(oi.UserTags)
}

func (n *jfsObjects) DeleteObjectTags(ctx context.Context, bucket, object string, opts minio.ObjectOptions) (minio.ObjectInfo, error) {
	if !n.gConf.ObjTag {
		return minio.ObjectInfo{}, minio.NotImplemented{}
	}
	if errno := n.fs.RemoveXattr(mctx, n.path(bucket, object), s3Tags); errno != 0 && errno != meta.ENOATTR {
		return minio.ObjectInfo{}, errno
	}
	return n.GetObjectInfo(ctx, bucket, object, opts)
}

func (n *jfsObjects) IsNotificationSupported() bool {
	return true
}

func (n *jfsObjects) IsListenSupported() bool {
	return true
}

func (n *jfsObjects) IsTaggingSupported() bool {
	return true
}
