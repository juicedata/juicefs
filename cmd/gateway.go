//+build !nogateway

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
	"path/filepath"

	"net/http"
	_ "net/http/pprof"
	"os"
	"path"
	"sort"
	"strconv"
	"strings"
	"sync"
	"syscall"
	"time"

	"github.com/juicedata/juicefs/pkg/chunk"
	"github.com/juicedata/juicefs/pkg/fs"
	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/usage"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/juicedata/juicefs/pkg/vfs"
	"github.com/urfave/cli/v2"

	mcli "github.com/minio/cli"
	"github.com/minio/minio-go/pkg/s3utils"
	minio "github.com/minio/minio/cmd"
	"github.com/minio/minio/pkg/auth"
)

const (
	sep        = "/"
	metaBucket = ".sys"
)

var mctx meta.Context

func gatewayFlags() *cli.Command {
	flags := append(clientFlags(),
		&cli.Float64Flag{
			Name:  "attr-cache",
			Value: 1.0,
			Usage: "attributes cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "entry-cache",
			Value: 0,
			Usage: "file entry cache timeout in seconds",
		},
		&cli.Float64Flag{
			Name:  "dir-entry-cache",
			Value: 1.0,
			Usage: "dir entry cache timeout in seconds",
		},
		&cli.StringFlag{
			Name:  "access-log",
			Usage: "path for JuiceFS access log",
		},
		&cli.StringFlag{
			Name:  "metrics",
			Value: "127.0.0.1:9567",
			Usage: "address to export metrics",
		},
		&cli.BoolFlag{
			Name:  "no-usage-report",
			Usage: "do not send usage report",
		},
		&cli.BoolFlag{
			Name:  "no-banner",
			Usage: "disable MinIO startup information",
		})
	return &cli.Command{
		Name:      "gateway",
		Usage:     "S3-compatible gateway",
		ArgsUsage: "META-URL ADDRESS",
		Flags:     flags,
		Action:    gateway,
	}
}

func gateway(c *cli.Context) error {
	setLoggerLevel(c)

	if c.Args().Len() < 2 {
		logger.Fatalf("Meta URL and listen address are required")
	}

	ak := os.Getenv("MINIO_ROOT_USER")
	if ak == "" {
		ak = os.Getenv("MINIO_ACCESS_KEY")
	}
	if len(ak) < 3 {
		logger.Fatalf("MINIO_ROOT_USER should be specified as an environment variable with at least 3 characters")
	}
	sk := os.Getenv("MINIO_ROOT_PASSWORD")
	if sk == "" {
		sk = os.Getenv("MINIO_SECRET_KEY")
	}
	if len(sk) < 8 {
		logger.Fatalf("MINIO_ROOT_PASSWORD should be specified as an environment variable with at least 8 characters")
	}

	address := c.Args().Get(1)
	gw = &GateWay{c}

	args := []string{"gateway", "--address", address, "--anonymous"}
	if c.Bool("no-banner") {
		args = append(args, "--quiet")
	}
	app := &mcli.App{
		Action: gateway2,
		Flags: []mcli.Flag{
			mcli.StringFlag{
				Name:  "address",
				Value: ":9000",
				Usage: "bind to a specific ADDRESS:PORT, ADDRESS can be an IP or hostname",
			},
			mcli.BoolFlag{
				Name:  "anonymous",
				Usage: "hide sensitive information from logging",
			},
			mcli.BoolFlag{
				Name:  "json",
				Usage: "output server logs and startup information in json format",
			},
			mcli.BoolFlag{
				Name:  "quiet",
				Usage: "disable MinIO startup information",
			},
		},
	}
	return app.Run(args)
}

var gw *GateWay

func gateway2(ctx *mcli.Context) error {
	minio.StartGateway(ctx, gw)
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
	mctx = meta.NewContext(uint32(os.Getpid()), uint32(os.Getuid()), []uint32{uint32(os.Getgid())})

	c := g.ctx
	addr := c.Args().Get(0)
	m := meta.NewClient(addr, &meta.Config{
		Retries:   10,
		Strict:    true,
		ReadOnly:  c.Bool("read-only"),
		OpenCache: time.Duration(c.Float64("open-cache") * 1e9),
	})
	format, err := m.Load()
	if err != nil {
		logger.Fatalf("load setting: %s", err)
	}

	chunkConf := chunk.Config{
		BlockSize: format.BlockSize * 1024,
		Compress:  format.Compression,

		GetTimeout:    time.Second * time.Duration(c.Int("get-timeout")),
		PutTimeout:    time.Second * time.Duration(c.Int("put-timeout")),
		MaxUpload:     c.Int("max-uploads"),
		Writeback:     c.Bool("writeback"),
		Prefetch:      c.Int("prefetch"),
		BufferSize:    c.Int("buffer-size") << 20,
		UploadLimit:   c.Int64("upload-limit") * 1e6 / 8,
		DownloadLimit: c.Int64("download-limit") * 1e6 / 8,

		CacheDir:       c.String("cache-dir"),
		CacheSize:      int64(c.Int("cache-size")),
		FreeSpace:      float32(c.Float64("free-space-ratio")),
		CacheMode:      os.FileMode(0600),
		CacheFullBlock: !c.Bool("cache-partial-only"),
		AutoCreate:     true,
	}
	if chunkConf.CacheDir != "memory" {
		ds := utils.SplitDir(chunkConf.CacheDir)
		for i := range ds {
			ds[i] = filepath.Join(ds[i], format.UUID)
		}
		chunkConf.CacheDir = strings.Join(ds, string(os.PathListSeparator))
	}
	if c.IsSet("bucket") {
		format.Bucket = c.String("bucket")
	}
	blob, err := createStorage(format)
	if err != nil {
		logger.Fatalf("object storage: %s", err)
	}
	logger.Infof("Data use %s", blob)

	store := chunk.NewCachedStore(blob, chunkConf)
	m.OnMsg(meta.DeleteChunk, meta.MsgCallback(func(args ...interface{}) error {
		chunkid := args[0].(uint64)
		length := args[1].(uint32)
		return store.Remove(chunkid, int(length))
	}))
	m.OnMsg(meta.CompactChunk, meta.MsgCallback(func(args ...interface{}) error {
		slices := args[0].([]meta.Slice)
		chunkid := args[1].(uint64)
		return vfs.Compact(chunkConf, store, slices, chunkid)
	}))
	err = m.NewSession()
	if err != nil {
		logger.Fatalf("new session: %s", err)
	}

	conf := &vfs.Config{
		Meta: &meta.Config{
			Retries: 10,
		},
		Format:          format,
		Version:         version.Version(),
		AttrTimeout:     time.Millisecond * time.Duration(c.Float64("attr-cache")*1000),
		EntryTimeout:    time.Millisecond * time.Duration(c.Float64("entry-cache")*1000),
		DirEntryTimeout: time.Millisecond * time.Duration(c.Float64("dir-entry-cache")*1000),
		AccessLog:       c.String("access-log"),
		Chunk:           &chunkConf,
	}

	exposeMetrics(m, c.String("metrics"))
	if !c.Bool("no-usage-report") {
		go usage.ReportUsage(m, "gateway "+version.Version())
	}

	jfs, err := fs.NewFileSystem(conf, m, store)
	if err != nil {
		logger.Fatalf("Initialize failed: %s", err)
	}
	return &jfsObjects{fs: jfs, conf: conf, listPool: minio.NewTreeWalkPool(time.Minute * 30)}, nil
}

type jfsObjects struct {
	minio.GatewayUnsupported
	conf     *vfs.Config
	fs       *fs.FileSystem
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
	n.fs.Close()
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
	eno := n.fs.Delete(mctx, n.path(bucket))
	return jfsToObjectErr(ctx, eno, bucket)
}

func (n *jfsObjects) MakeBucketWithLocation(ctx context.Context, bucket string, options minio.BucketOptions) error {
	if !n.isValidBucketName(bucket) {
		return minio.BucketNameInvalid{Bucket: bucket}
	}
	if n.conf.Format.Name != "" {
		return nil
	}
	eno := n.fs.Mkdir(mctx, n.path(bucket), 0755)
	return jfsToObjectErr(ctx, eno, bucket)
}

func (n *jfsObjects) GetBucketInfo(ctx context.Context, bucket string) (bi minio.BucketInfo, err error) {
	if !n.isValidBucketName(bucket) {
		return bi, minio.BucketNameInvalid{Bucket: bucket}
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
	if n.conf.Format.Name != "" {
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

	for _, entry := range entries[2:] {
		// Ignore all reserved bucket names and invalid bucket names.
		if isReservedOrInvalidBucket(entry.Name(), false) || !n.isValidBucketName(entry.Name()) {
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
	return n.isObjectDir(context.Background(), bucket, leafPath)
}

func (n *jfsObjects) isLeaf(bucket, leafPath string) bool {
	return !strings.HasSuffix(leafPath, "/")
}

func (n *jfsObjects) listDirFactory() minio.ListDirFunc {
	return func(bucket, prefixDir, prefixEntry string) (emptyDir bool, entries []string, delayIsLeaf bool) {
		f, eno := n.fs.Open(mctx, n.path(bucket, prefixDir), 0)
		if eno != 0 {
			return fs.IsNotExist(eno), nil, false
		}
		defer f.Close(mctx)
		fis, eno := f.Readdir(mctx, 0)
		if eno != 0 {
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
		entries, delayIsLeaf = minio.FilterListEntries(bucket, prefixDir, entries, prefixEntry, n.isLeaf)
		return false, entries, delayIsLeaf
	}
}

func (n *jfsObjects) checkBucket(ctx context.Context, bucket string) error {
	if !n.isValidBucketName(bucket) {
		return minio.BucketNameInvalid{Bucket: bucket}
	}
	if _, eno := n.fs.Stat(mctx, n.path(bucket)); eno != 0 {
		return jfsToObjectErr(ctx, eno, bucket)
	}
	return nil
}

// ListObjects lists all blobs in JFS bucket filtered by prefix.
func (n *jfsObjects) ListObjects(ctx context.Context, bucket, prefix, marker, delimiter string, maxKeys int) (loi minio.ListObjectsInfo, err error) {
	if err := n.checkBucket(ctx, bucket); err != nil {
		return loi, err
	}
	getObjectInfo := func(ctx context.Context, bucket, object string) (obj minio.ObjectInfo, err error) {
		fi, eno := n.fs.Stat(mctx, n.path(bucket, object))
		if eno == 0 {
			obj = minio.ObjectInfo{
				Bucket:  bucket,
				Name:    object,
				ModTime: fi.ModTime(),
				Size:    fi.Size(),
				IsDir:   fi.IsDir(),
				AccTime: fi.ModTime(),
			}
		}
		return obj, jfsToObjectErr(ctx, eno, bucket, object)
	}

	if maxKeys == 0 {
		maxKeys = -1 // list as many objects as possible
	}
	return minio.ListObjects(ctx, n, bucket, prefix, marker, delimiter, maxKeys, n.listPool, n.listDirFactory(), n.isLeaf, n.isLeafDir, getObjectInfo, getObjectInfo)
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
	info.Bucket = bucket
	info.Name = object
	p := n.path(bucket, object)
	root := n.path(bucket)
	for p != root {
		if eno := n.fs.Delete(mctx, p); eno != 0 {
			if fs.IsNotEmpty(eno) {
				err = nil
			} else {
				err = eno
			}
			break
		}
		p = path.Dir(p)
	}
	return info, jfsToObjectErr(ctx, err, bucket, object)
}

func (n *jfsObjects) DeleteObjects(ctx context.Context, bucket string, objects []minio.ObjectToDelete, options minio.ObjectOptions) (objs []minio.DeletedObject, errs []error) {
	for _, object := range objects {
		_, err := n.DeleteObject(ctx, bucket, object.ObjectName, options)
		if err == nil {
			objs = append(objs, minio.DeletedObject{ObjectName: object.ObjectName})
		} else {
			errs = append(errs, err)
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
	f, eno := n.fs.Open(mctx, n.path(bucket, object), 0)
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
		return n.GetObjectInfo(ctx, srcBucket, srcObject, minio.ObjectOptions{})
	}
	tmp := n.tpath(dstBucket, "tmp", minio.MustGetUUID())
	_ = n.mkdirAll(ctx, path.Dir(tmp), 0755)
	_, eno := n.fs.Create(mctx, tmp, 0644)
	if eno != 0 {
		logger.Errorf("create %s: %s", tmp, eno)
		return
	}
	defer func() { _ = n.fs.Delete(mctx, tmp) }()

	_, eno = n.fs.CopyFileRange(mctx, src, 0, tmp, 0, 1<<63)
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, srcBucket, srcObject)
		logger.Errorf("copy %s to %s: %s", src, tmp, err)
		return
	}
	eno = n.fs.Rename(mctx, tmp, dst, 0)
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
		Bucket: dstBucket,
		Name:   dstObject,
		// ETag:    r.MD5CurrentHexString(),
		ModTime: fi.ModTime(),
		Size:    fi.Size(),
		IsDir:   fi.IsDir(),
		AccTime: fi.ModTime(),
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

func (n *jfsObjects) isObjectDir(ctx context.Context, bucket, object string) bool {
	fi, eno := n.fs.Stat(mctx, minio.PathJoin(bucket, object))
	if eno != 0 {
		return false
	}
	return fi.IsDir()
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
	if fi, eno := n.fs.Stat(mctx, p); eno == 0 {
		if !fi.IsDir() {
			return fmt.Errorf("%s is not directory", p)
		}
		return nil
	}
	eno := n.fs.Mkdir(mctx, p, uint16(mode))
	if eno != 0 && fs.IsNotExist(eno) {
		if err := n.mkdirAll(ctx, path.Dir(p), 0755); err != nil {
			return err
		}
		eno = n.fs.Mkdir(mctx, p, uint16(mode))
	}
	if eno != 0 && fs.IsExist(eno) {
		eno = 0
	}
	if eno == 0 {
		return nil
	}
	return eno
}

func (n *jfsObjects) putObject(ctx context.Context, bucket, object string, r *minio.PutObjReader, opts minio.ObjectOptions) (err error) {
	tmpname := n.tpath(bucket, "tmp", minio.MustGetUUID())
	_ = n.mkdirAll(ctx, path.Dir(tmpname), 0755)
	f, eno := n.fs.Create(mctx, tmpname, 0644)
	if eno != 0 {
		logger.Errorf("create %s: %s", tmpname, eno)
		err = eno
		return
	}
	defer func() { _ = n.fs.Delete(mctx, tmpname) }()
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
	dir := path.Dir(object)
	if dir != "" {
		_ = n.mkdirAll(ctx, dir, os.FileMode(0755))
	}
	if eno := n.fs.Rename(mctx, tmpname, object, 0); eno != 0 {
		err = jfsToObjectErr(ctx, eno, bucket, object)
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
	fi, eno := n.fs.Stat(mctx, p)
	if eno != 0 {
		return objInfo, jfsToObjectErr(ctx, eno, bucket, object)
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
		eno := n.fs.SetXattr(mctx, p, "s3-object", []byte(object), 0)
		if eno != 0 {
			logger.Warnf("set object %s on upload %s: %s", object, uploadID, eno)
		}
	}
	return
}

const uploadKeyName = "s3-object"
const partEtag = "s3-etag"

func (n *jfsObjects) ListMultipartUploads(ctx context.Context, bucket string, prefix string, keyMarker string, uploadIDMarker string, delimiter string, maxUploads int) (lmi minio.ListMultipartsInfo, err error) {
	if err = n.checkBucket(ctx, bucket); err != nil {
		return
	}
	f, eno := n.fs.Open(mctx, n.tpath(bucket, "uploads"), 0)
	if eno != 0 {
		return // no found
	}
	defer f.Close(mctx)
	entries, eno := f.ReaddirPlus(mctx, 0)
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, bucket)
		return
	}
	lmi.Prefix = prefix
	lmi.KeyMarker = keyMarker
	lmi.UploadIDMarker = uploadIDMarker
	lmi.MaxUploads = maxUploads
	for _, e := range entries {
		uploadID := string(e.Name)
		if uploadID > uploadIDMarker {
			object_, _ := n.fs.GetXattr(mctx, n.upath(bucket, uploadID), uploadKeyName)
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
			etag, _ := n.fs.GetXattr(mctx, n.ppath(bucket, uploadID, string(entry.Name)), partEtag)
			result.Parts = append(result.Parts, minio.PartInfo{
				PartNumber:   num,
				Size:         int64(entry.Attr.Length),
				LastModified: time.Unix(int64(entry.Attr.Mtime), 0),
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
	if !n.isValidBucketName(srcBucket) {
		err = minio.BucketNameInvalid{Bucket: srcBucket}
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
	if err = n.putObject(ctx, bucket, p, r, opts); err != nil {
		err = jfsToObjectErr(ctx, err, bucket, object)
		return
	}
	etag := r.MD5CurrentHexString()
	_ = n.fs.SetXattr(mctx, p, partEtag, []byte(etag), 0)
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
	_, eno := n.fs.Create(mctx, tmp, 0755)
	if eno != 0 {
		err = jfsToObjectErr(ctx, eno, bucket, object, uploadID)
		logger.Errorf("create complete: %s", err)
		return
	}
	var total uint64
	for _, part := range parts {
		p := n.ppath(bucket, uploadID, strconv.Itoa(part.PartNumber))
		copied, eno := n.fs.CopyFileRange(mctx, p, 0, tmp, total, 1<<30)
		if eno != 0 {
			err = jfsToObjectErr(ctx, eno, bucket, object, uploadID)
			logger.Errorf("merge parts: %s", err)
			return
		}
		total += copied
	}

	name := n.path(bucket, object)
	dir := path.Dir(name)
	if dir != "" {
		if err = n.mkdirAll(ctx, dir, os.FileMode(0755)); err != nil {
			_ = n.fs.Delete(mctx, tmp)
			err = jfsToObjectErr(ctx, err, bucket, object, uploadID)
			return
		}
	}

	eno = n.fs.Rename(mctx, tmp, name, 0)
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
	_ = n.fs.Rmr(mctx, n.upath(bucket, uploadID))

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
	eno := n.fs.Rmr(mctx, n.upath(bucket, uploadID))
	return jfsToObjectErr(ctx, eno, bucket, object, uploadID)
}
