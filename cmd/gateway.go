/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */
package main

import (
	"context"
	"fmt"
	"io"

	"net/http"
	_ "net/http/pprof"
	"os"
	"path"
	"runtime"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/vfs"

	"github.com/minio/cli"
	"github.com/minio/minio-go/pkg/s3utils"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/pkg/auth"
)

const (
	sep        = "/"
	metaBucket = ".sys"
)

var mctx meta.Context

var flags = []cli.Flag{}

func gatewayFlags() *cli.Command {
	var defaultCacheDir = "/var/jfsCache"
	if runtime.GOOS == "darwin" {
		homeDir, err := os.UserHomeDir()
		if err != nil {
			logger.Fatalf("%v", err)
			return nil
		}
		defaultCacheDir = path.Join(homeDir, ".juicefs", "cache")
	}
	return &cli.Command{
		Name:      "S3 gateway",
		Usage:     "S3 Gateway for JuiceFS",
		ArgsUsage: "REDIS-URL ADDR",
		Action:    gateway,
		Flags: []cli.Flag{
			cli.StringFlag{
				Name:  "address",
				Value: ":9000",
				Usage: "bind to a specific ADDRESS:PORT, ADDRESS can be an IP or hostname",
			},
			cli.BoolFlag{
				Name:  "anonymous",
				Usage: "hide sensitive information from logging",
			},
			cli.BoolFlag{
				Name:  "json",
				Usage: "output server logs and startup information in json format",
			},
			// This flag is hidden and to be used only during certain performance testing.
			cli.BoolFlag{
				Name:   "no-compat",
				Usage:  "disable strict S3 compatibility by turning on certain performance optimizations",
				Hidden: true,
			},
		},
	}
}

func gateway(ctx *cli.Context) error {
	// mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})
	utils.InitLoggers(false)
	go func() {
		for port := 6060; port < 6100; port++ {
			http.ListenAndServe(fmt.Sprintf("127.0.0.1:%d", port), nil)
		}
	}()

	minio.StartGateway(ctx, &GateWay{ctx})
	return nil
}

type GateWay struct {
	ctx *cli.Context
}

func (g *GateWay) Name() string {
	return "JuiceFS"
}

func (g *GateWay) Production() bool {
	return true
}

func (g *GateWay) NewGatewayLayer(creds auth.Credentials) (minio.ObjectLayer, error) {
	conf := g.conf
	m := meta.Init(conf.Mountpoint, conf.Meta)
	jfs, err := fs.Init(conf.Primary.Name, conf, m, true)
	if err != nil {
		logger.Fatalf("Initialize failed: %s", err)
	}
	return &jfsObjects{jfs: jfs, listPool: minio.NewTreeWalkPool(time.Minute * 30)}, nil
}

type jfsObjects struct {
	minio.GatewayUnsupported
	conf     *vfs.Config
	jfs      *fs.FileSystem
	listPool *minio.TreeWalkPool
}

func (n *jfsObjects) IsCompressionSupported() bool {
	return n.conf.Chunk.Compress != "" && n.conf.Chunk.Compress != "none"
}

func (n *jfsObjects) IsEncryptionSupported() bool {
	return false
}

// IsReady returns whether the layer is ready to take requests.
func (n *jfsObjects) IsReady(_ context.Context) bool {
	return true
}

func (n *jfsObjects) Shutdown(ctx context.Context) error {
	n.jfs.Close()
	return nil
}

func (n *jfsObjects) StorageInfo(ctx context.Context) (info minio.StorageInfo, errors []error) {
	sinfo := minio.StorageInfo{}
	sinfo.Backend.Type = minio.BackendGateway
	sinfo.Backend.GatewayOnline = true
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

	if _, ok := err.(syscall.Errno); !ok {
		logger.Errorf("error: %s bucket: %s, object: %s, uploadID: %s", err, bucket, object, uploadID)
		return err
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
func (n *jfsObjects) isValidBucketName(bucket string) bool {
	if n.conf.Format.Name != "" && bucket != n.conf.Format.Name {
		return false
	}
	return s3utils.CheckValidBucketNameStrict(bucket) == nil
}

func (n *jfsObjects) path(p ...string) string {
	if len(p) > 0 && p[0] == n.conf.Format.Name {
		p = p[1:]
	}
	return sep + minio.PathJoin(p...)
}

func (n *jfsObjects) tpath(p ...string) string {
	return sep + metaBucket + n.path(p...)
}

func (n *jfsObjects) upath(bucket, uploadID string) string {
	return n.tpath(bucket, "uploads", uploadID)
}

func (n *jfsObjects) ppath(bucket, uploadID, part string) string {
	return n.tpath(bucket, "uploads", uploadID, part)
}

func (n *jfsObjects) DeleteBucket(ctx context.Context, bucket string, forceDelete bool) error {
	if !n.isValidBucketName(bucket) {
		return minio.BucketNameInvalid{Bucket: bucket}
	}
	if n.conf.Format.Name != "" {
		return minio.BucketNotEmpty{Bucket: bucket}
	}
	err := n.jfs.Delete(mctx, n.path(bucket))
	return jfsToObjectErr(ctx, err, bucket)
}

func (n *jfsObjects) MakeBucketWithLocation(ctx context.Context, bucket string, options minio.BucketOptions) error {
	if !n.isValidBucketName(bucket) {
		return minio.BucketNameInvalid{Bucket: bucket}
	}
	if n.conf.Format.Name != "" {
		return nil
	}
	err := n.jfs.Mkdir(mctx, n.path(bucket), 0755)
	return jfsToObjectErr(ctx, err, bucket)
}

func (n *jfsObjects) GetBucketInfo(ctx context.Context, bucket string) (bi minio.BucketInfo, err error) {
	if !n.isValidBucketName(bucket) {
		return bi, minio.BucketNameInvalid{Bucket: bucket}
	}
	fi, err := n.jfs.Stat(mctx, n.path(bucket))
	if err == nil {
		bi = minio.BucketInfo{
			Name:    bucket,
			Created: time.Unix(fi.Atime(), 0),
		}
	}
	return bi, jfsToObjectErr(ctx, err, bucket)
}

// byBucketName is a collection satisfying sort.Interface.
type byBucketName []minio.BucketInfo

func (d byBucketName) Len() int           { return len(d) }
func (d byBucketName) Swap(i, j int)      { d[i], d[j] = d[j], d[i] }
func (d byBucketName) Less(i, j int) bool { return d[i].Name < d[j].Name }

// Ignores all reserved bucket names or invalid bucket names.
func isReservedOrInvalidBucket(bucketEntry string, strict bool) bool {
	if err := s3utils.CheckValidBucketName(bucketEntry); err != nil {
		return true
	}
	return bucketEntry == metaBucket
}

func (n *jfsObjects) ListBuckets(ctx context.Context) (buckets []minio.BucketInfo, err error) {
	if n.conf.Format.Name != "" {
		fi, err := n.jfs.Stat(mctx, "/")
		if err != 0 {
			return nil, jfsToObjectErr(ctx, err)
		}
		buckets = []minio.BucketInfo{{
			Name:    n.conf.Format.Name,
			Created: time.Unix(fi.Atime(), 0),
		}}
		return buckets, nil
	}
	f, err := n.jfs.Open(mctx, sep, 0)
	if err != nil {
		return nil, jfsToObjectErr(ctx, err)
	}
	entries, err := f.Readdir(mctx, 10000)
	if err != nil {
		return nil, jfsToObjectErr(ctx, err)
	}

	for _, entry := range entries[2:] {
		// Ignore all reserved bucket names and invalid bucket names.
		if isReservedOrInvalidBucket(entry.Name(), false) || !n.isValidBucketName(entry.Name()) {
			continue
		}
		if entry.IsDir() {
			buckets = append(buckets, minio.BucketInfo{
				Name:    entry.Name(),
				Created: time.Unix(entry.(*fs.FileStat).Atime(), 0),
			})
		}
	}

	// Sort bucket infos by bucket name.
	sort.Sort(byBucketName(buckets))
	return buckets, nil
}

func (n *jfsObjects) listDirFactory() minio.ListDirFunc {
	// listDir - lists all the entries at a given prefix and given entry in the prefix.
	listDir := func(bucket, prefixDir, prefixEntry string) (emptyDir bool, entries []string, delayIsLeaf bool) {
		f, err := n.jfs.Open(mctx, n.path(bucket, prefixDir), 0)
		if err != 0 {
			return fs.IsNotExist(err), nil, false
		}
		defer f.Close(mctx)
		fis, err := f.Readdir(mctx, 0)
		if err != 0 {
			return
		}
		if len(fis) == 2 {
			return true, nil, false
		}
		root := n.path(bucket, prefixDir) == "/"
		for _, fi := range fis[2:] {
			if root && len(fi.Name()) == len(metaBucket) && string(fi.Name()) == metaBucket {
				continue
			}
			if fi.IsDir() {
				entries = append(entries, fi.Name()+sep)
			} else {
				entries = append(entries, fi.Name())
			}
		}
		return false, minio.FilterMatchingPrefix(entries, prefixEntry), false
	}

	// Return list factory instance.
	return listDir
}

func (n *jfsObjects) checkBucket(ctx context.Context, bucket string) error {
	if !n.isValidBucketName(bucket) {
		return minio.BucketNameInvalid{Bucket: bucket}
	}
	if _, err := n.jfs.Stat(mctx, n.path(bucket)); err != 0 {
		return jfsToObjectErr(ctx, err, bucket)
	}
	return nil
}

// ListObjects lists all blobs in JFS bucket filtered by prefix.
func (n *jfsObjects) ListObjects(ctx context.Context, bucket, prefix, marker, delimiter string, maxKeys int) (loi minio.ListObjectsInfo, err error) {
	if err := n.checkBucket(ctx, bucket); err != nil {
		return loi, err
	}
	getObjectInfo := func(ctx context.Context, bucket, object string) (obj minio.ObjectInfo, err error) {
		fi, err := n.jfs.Stat(mctx, n.path(bucket, object))
		if err == nil {
			obj = minio.ObjectInfo{
				Bucket:  bucket,
				Name:    object,
				ModTime: fi.ModTime(),
				Size:    fi.Size(),
				IsDir:   fi.IsDir(),
				AccTime: fi.ModTime(),
			}
		}
		return obj, jfsToObjectErr(ctx, err, bucket, object)
	}

	return minio.ListObjects(ctx, n, bucket, prefix, marker, delimiter, maxKeys, n.listPool, n.listDirFactory(), nil, nil, getObjectInfo, getObjectInfo)
}

// ListObjectsV2 lists all blobs in JFS bucket filtered by prefix
func (n *jfsObjects) ListObjectsV2(ctx context.Context, bucket, prefix, continuationToken, delimiter string, maxKeys int,
	fetchOwner bool, startAfter string) (loi minio.ListObjectsV2Info, err error) {
	if !n.isValidBucketName(bucket) {
		return minio.ListObjectsV2Info{}, minio.BucketNameInvalid{Bucket: bucket}
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

func (n *jfsObjects) DeleteObject(ctx context.Context, bucket, object string, options minio.ObjectOptions) (info minio.ObjectInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	p := n.path(bucket, object)
	root := n.path(bucket)
	for p != root {
		if err = n.jfs.Delete(mctx, p); err != nil {
			if fs.IsNotEmpty(err) {
				err = nil
			}
			break
		}
		p = path.Dir(p)
	}
	return minio.ObjectInfo{}, nil
}

func (n *jfsObjects) DeleteObjects(ctx context.Context, bucket string, objects []minio.ObjectToDelete, options minio.ObjectOptions) (objs []minio.DeletedObject, errs []error) {
	if err := n.checkBucket(ctx, bucket); err != nil {
		return
	}
	errs = make([]error, len(objects))
	for idx, object := range objects {
		_, errs[idx] = n.DeleteObject(ctx, bucket, object.ObjectName, minio.ObjectOptions{})
	}
	return nil, nil
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
	f, err := n.jfs.Open(mctx, n.path(bucket, object), 0)
	if err != nil {
		return nil, jfsToObjectErr(ctx, err, bucket, object)
	}
	f.Seek(mctx, startOffset, 0)
	r := &io.LimitedReader{&fReader{f}, length}
	closer := func() { f.Close(mctx) }
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
		return n.GetObjectInfo(ctx, srcBucket, srcObject, minio.ObjectOptions{})
	}
	dir := path.Dir(dst)
	if dir != "" {
		n.mkdirAll(ctx, dir, os.FileMode(0755))
	}
	// err = n.jfs.CreateSnapshot(mctx, src, dst, common.SNAPSHOT_MODE_CAN_OVERWRITE|common.SNAPSHOT_MODE_CPLIKE_ATTR)
	if err != nil {
		return info, jfsToObjectErr(ctx, err, dstBucket, dst)
	}
	return n.GetObjectInfo(ctx, dstBucket, dstObject, minio.ObjectOptions{})
}

var buffPool = sync.Pool{
	New: func() interface{} {
		return make([]byte, 1<<17)
	},
}

func (n *jfsObjects) GetObject(ctx context.Context, bucket, object string, startOffset, length int64, writer io.Writer, etag string, opts minio.ObjectOptions) (err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	f, err := n.jfs.Open(mctx, n.path(bucket, object), 0)
	if err != nil {
		return jfsToObjectErr(ctx, err, bucket, object)
	}
	defer f.Close(mctx)
	var buf = buffPool.Get().([]byte)
	defer buffPool.Put(buf)
	f.Seek(mctx, startOffset, 0)
	for length > 0 {
		l := int64(len(buf))
		if l > length {
			l = length
		}
		n, e := f.Read(mctx, buf[:l])
		if n == 0 {
			if e != io.EOF {
				err = e
			}
			break
		}
		if _, err = writer.Write(buf[:n]); err != nil {
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
	fi, err := n.jfs.Stat(mctx, n.path(bucket, object))
	if err != nil {
		err = jfsToObjectErr(ctx, err, bucket, object)
		return
	}
	if strings.HasSuffix(object, sep) && !fi.IsDir() {
		err = jfsToObjectErr(ctx, os.ErrNotExist, bucket, object)
		return
	}
	return minio.ObjectInfo{
		Bucket:  bucket,
		Name:    object,
		ModTime: fi.ModTime(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		AccTime: fi.ModTime(),
	}, nil
}

func (n *jfsObjects) mkdirAll(ctx context.Context, p string, mode os.FileMode) error {
	if fi, err := n.jfs.Stat(mctx, p); err == 0 {
		if !fi.IsDir() {
			return fmt.Errorf("% is not directory", p)
		}
		return nil
	}
	err := n.jfs.Mkdir(mctx, p, uint16(mode))
	if err != 0 && fs.IsNotExist(err) {
		if err := n.mkdirAll(ctx, path.Dir(p), 0755); err != nil {
			return err
		}
		err = n.jfs.Mkdir(mctx, p, uint16(mode))
	}
	if err != 0 && fs.IsExist(err) {
		err = 0
	}
	return err
}

func (n *jfsObjects) putObject(ctx context.Context, bucket, p string, r *minio.PutObjReader, opts minio.ObjectOptions) (err error) {
	tmpname := n.tpath(bucket, "tmp", minio.MustGetUUID())
	n.mkdirAll(ctx, path.Dir(tmpname), 0755)
	var f *fs.File
	f, err = n.jfs.Create(mctx, tmpname, 0644)
	if err != nil {
		logger.Errorf("create %s: %s", tmpname, err)
		return
	}
	defer n.jfs.Delete(mctx, tmpname)
	var buf = buffPool.Get().([]byte)
	defer buffPool.Put(buf)
	for {
		n, err := io.ReadFull(r, buf)
		if n == 0 {
			if err == io.EOF {
				err = nil
			}
			break
		}
		_, err = f.Write(mctx, buf[:n])
		if err != nil {
			break
		}
	}
	if err == nil {
		err = f.Close(mctx)
	} else {
		f.Close(mctx)
	}
	if err != nil {
		return
	}
	dir := path.Dir(p)
	if dir != "" {
		n.mkdirAll(ctx, dir, os.FileMode(0755))
	}
	if err = n.jfs.Rename(mctx, tmpname, p); err != nil {
		return
	}
	return
}

func (n *jfsObjects) PutObject(ctx context.Context, bucket string, object string, r *minio.PutObjReader, opts minio.ObjectOptions) (objInfo minio.ObjectInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}

	p := n.path(bucket, object)
	if strings.HasSuffix(object, sep) && r.Size() == 0 {
		if err = n.mkdirAll(ctx, p, os.FileMode(0755)); err != nil {
			err = jfsToObjectErr(ctx, err, bucket, object)
			return
		}
	} else if err = n.putObject(ctx, bucket, p, r, opts); err != nil {
		return
	}
	fi, err := n.jfs.Stat(mctx, p)
	if err != nil {
		return objInfo, jfsToObjectErr(ctx, err, bucket, object)
	}
	return minio.ObjectInfo{
		Bucket:  bucket,
		Name:    object,
		ETag:    r.MD5CurrentHexString(),
		ModTime: fi.ModTime(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		AccTime: fi.ModTime(),
	}, nil
}

func (n *jfsObjects) NewMultipartUpload(ctx context.Context, bucket string, object string, opts minio.ObjectOptions) (uploadID string, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	uploadID = minio.MustGetUUID()
	p := n.upath(bucket, uploadID)
	err = n.mkdirAll(ctx, p, os.FileMode(0755))
	if err == nil {
		n.jfs.SetXattr(mctx, p, "s3-object", []byte(object), 0)
	}
	return
}

const uploadKeyName = "s3-object"
const partEtag = "s3-etag"

func (n *jfsObjects) ListMultipartUploads(ctx context.Context, bucket string, prefix string, keyMarker string, uploadIDMarker string, delimiter string, maxUploads int) (lmi minio.ListMultipartsInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	f, e := n.jfs.Open(mctx, n.tpath(bucket, "uploads"), 0)
	if e != 0 {
		return // no found
	}
	defer f.Close(mctx)
	entries, e := f.ReaddirPlus(mctx, 0)
	if e != 0 {
		err = jfsToObjectErr(ctx, e, bucket)
		return
	}
	lmi.Prefix = prefix
	lmi.KeyMarker = keyMarker
	lmi.UploadIDMarker = uploadIDMarker
	lmi.MaxUploads = maxUploads
	for _, e := range entries {
		uploadID := string(e.Name)
		if uploadID > uploadIDMarker {
			object_, _ := n.jfs.GetXattr(mctx, n.upath(bucket, uploadID), uploadKeyName)
			object := string(object_)
			if strings.HasPrefix(object, prefix) && object > keyMarker {
				lmi.Uploads = append(lmi.Uploads, minio.MultipartInfo{
					Object:    object,
					UploadID:  uploadID,
					Initiated: time.Unix(int64(e.Attr.Atime), int64(e.Attr.Atimensec)),
				})
			}
		}
	}
	if len(lmi.Uploads) > maxUploads {
		lmi.IsTruncated = true
		lmi.Uploads = lmi.Uploads[:maxUploads]
		lmi.NextKeyMarker = keyMarker
		lmi.NextUploadIDMarker = lmi.Uploads[maxUploads-1].UploadID
	}
	return lmi, jfsToObjectErr(ctx, err, bucket)
}

func (n *jfsObjects) checkUploadIDExists(ctx context.Context, bucket, object, uploadID string) (err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	_, err = n.jfs.Stat(mctx, n.upath(bucket, uploadID))
	return jfsToObjectErr(ctx, err, bucket, object, uploadID)
}

type sortPartInfo []minio.PartInfo

func (s sortPartInfo) Len() int           { return len(s) }
func (s sortPartInfo) Less(i, j int) bool { return s[i].PartNumber < s[j].PartNumber }
func (s sortPartInfo) Swap(i, j int)      { s[i], s[j] = s[j], s[i] }

func (n *jfsObjects) ListObjectParts(ctx context.Context, bucket, object, uploadID string, partNumberMarker int, maxParts int, opts minio.ObjectOptions) (result minio.ListPartsInfo, err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return result, err
	}
	f, e := n.jfs.Open(mctx, n.upath(bucket, uploadID), 0)
	if e != 0 {
		err = jfsToObjectErr(ctx, e, bucket, object, uploadID)
		return
	}
	defer f.Close(mctx)
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
			etag, _ := n.jfs.GetXattr(mctx, n.ppath(bucket, uploadID, string(entry.Name)), partEtag)
			result.Parts = append(result.Parts, minio.PartInfo{
				PartNumber:   num,
				Size:         int64(entry.Attr.Length),
				LastModified: time.Unix(int64(entry.Attr.Mtime), 0),
				ETag:         string(etag),
			})
		}
	}
	sort.Sort(sortPartInfo(result.Parts))
	if len(result.Parts) > maxParts {
		result.IsTruncated = true
		result.Parts = result.Parts[:maxParts]
		result.NextPartNumberMarker = result.Parts[maxParts-1].PartNumber
	}
	return
}

func (n *jfsObjects) CopyObjectPart(ctx context.Context, srcBucket, srcObject, dstBucket, dstObject, uploadID string, partID int,
	startOffset int64, length int64, srcInfo minio.ObjectInfo, srcOpts, dstOpts minio.ObjectOptions) (result minio.PartInfo, err error) {
	if !n.isValidBucketName(srcBucket) {
		err = minio.BucketNameInvalid{Bucket: srcBucket}
		return
	}
	if err = n.checkUploadIDExists(ctx, dstBucket, dstObject, uploadID); err != nil {
		return
	}
	return n.PutObjectPart(ctx, dstBucket, dstObject, uploadID, partID, srcInfo.PutObjReader, dstOpts)
}

func (n *jfsObjects) PutObjectPart(ctx context.Context, bucket, object, uploadID string, partID int, r *minio.PutObjReader, opts minio.ObjectOptions) (info minio.PartInfo, err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return
	}
	p := n.ppath(bucket, uploadID, strconv.Itoa(partID))
	if err = n.putObject(ctx, bucket, p, r, opts); err != nil {
		err = jfsToObjectErr(ctx, err, bucket, object)
		return
	}
	etag := r.MD5CurrentHexString()
	n.jfs.SetXattr(mctx, p, partEtag, []byte(etag), 0)
	info.PartNumber = partID
	info.ETag = etag
	info.LastModified = minio.UTCNow()
	info.Size = r.Reader.Size()
	return
}

func (n *jfsObjects) CompleteMultipartUpload(ctx context.Context, bucket, object, uploadID string, parts []minio.CompletePart, opts minio.ObjectOptions) (objInfo minio.ObjectInfo, err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return
	}

	var ps []string
	for _, part := range parts {
		p := n.ppath(bucket, uploadID, strconv.Itoa(part.PartNumber))
		ps = append(ps, p)
	}
	tmp := n.ppath(bucket, uploadID, "complete")
	n.jfs.Delete(mctx, tmp)

	if f, e := n.jfs.Create(mctx, tmp, 0755); e != 0 {
		err = jfsToObjectErr(ctx, e, bucket, object)
		return
	} else {
		f.Close(mctx)
	}
	// FIXME
	// err = n.jfs.Concat(mctx, tmp, ps)
	if err != nil {
		err = jfsToObjectErr(ctx, err, bucket, object)
		logger.Errorf("merge parts %s: %s", uploadID, err)
		return
	}

	name := n.path(bucket, object)
	dir := path.Dir(name)
	if dir != "" {
		if err = n.mkdirAll(ctx, dir, os.FileMode(0755)); err != nil {
			n.jfs.Delete(mctx, tmp)
			err = jfsToObjectErr(ctx, err, bucket, object)
			return
		}
	}

	err = n.jfs.Rename(mctx, tmp, name)
	if err != nil {
		n.jfs.Delete(mctx, tmp)
		err = jfsToObjectErr(ctx, err, bucket, object)
		logger.Errorf("Rename %s -> %s: %s", tmp, name, err)
		return
	}

	fi, err := n.jfs.Stat(mctx, name)
	if err != nil {
		err = jfsToObjectErr(ctx, err, bucket, object)
		return
	}
	go n.jfs.Rmr(mctx, n.upath(bucket, uploadID))

	// Calculate s3 compatible md5sum for complete multipart.
	s3MD5 := minio.ComputeCompleteMultipartMD5(parts)
	return minio.ObjectInfo{
		Bucket:  bucket,
		Name:    object,
		ETag:    s3MD5,
		ModTime: fi.ModTime(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		AccTime: fi.ModTime(),
	}, nil
}

func (n *jfsObjects) AbortMultipartUpload(ctx context.Context, bucket, object, uploadID string, option minio.ObjectOptions) (err error) {
	if err = n.checkUploadIDExists(ctx, bucket, object, uploadID); err != nil {
		return
	}
	// err = n.jfs.Rmr(mctx, n.upath(bucket, uploadID))
	return jfsToObjectErr(ctx, err, bucket, object, uploadID)
}
