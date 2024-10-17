/*
 * JuiceFS, Copyright 2024 Juicedata, Inc.
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

package sync

import (
	"context"
	"encoding/json"
	"fmt"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"github.com/redis/go-redis/v9"
	"net"
	"net/url"
	"os"
	"path"
	"path/filepath"
	"regexp"
	"runtime"
	"strconv"
	"strings"
	"sync"
	"time"
)

type MQ interface {
	register(string) error
	unregister(string) error
	fetchJob(chan<- object.Object) error
	updateJob(string, string) error
	updateSelfInfo(string) error
	getObject(alias string) (*StorageInfo, error)
}

type StorageInfo struct {
	Alias     string
	Type      string
	Endpoint  string
	Prefix    string
	AccessKey string
	SecretKey string
	Uri       string
}

type TaskInfo struct {
	TaskIdO string
	KeyO    string
	DstKeyO string
	SizeO   int64
	MtimeO  time.Time
	IsDirO  bool
	ScO     string
	SrcType string
	DstType string
}

func (t *TaskInfo) DstKey() string {
	return t.DstKeyO
}

type ObjectProvider struct {
	sync.Mutex
	ObjMap map[string]object.ObjectStorage
	mq     MQ
}

// Check if uri is local file path
func isFilePath(uri string) bool {
	// check drive pattern when running on Windows
	if runtime.GOOS == "windows" &&
		len(uri) > 1 && (('a' <= uri[0] && uri[0] <= 'z') ||
		('A' <= uri[0] && uri[0] <= 'Z')) && uri[1] == ':' {
		return true
	}
	return !strings.Contains(uri, ":")
}
func extractToken(uri string) (string, string) {
	if submatch := regexp.MustCompile(`^.*:.*:.*(:.*)@.*$`).FindStringSubmatch(uri); len(submatch) == 2 {
		return strings.ReplaceAll(uri, submatch[1], ""), strings.TrimLeft(submatch[1], ":")
	}
	return uri, ""
}
func supportHTTPS(name, endpoint string) bool {
	switch name {
	case "ufile":
		return !(strings.Contains(endpoint, ".internal-") || strings.HasSuffix(endpoint, ".ucloud.cn"))
	case "oss":
		return !(strings.Contains(endpoint, ".vpc100-oss") || strings.Contains(endpoint, "internal.aliyuncs.com"))
	case "jss":
		return false
	case "s3":
		ps := strings.SplitN(strings.Split(endpoint, ":")[0], ".", 2)
		if len(ps) > 1 && net.ParseIP(ps[1]) != nil {
			return false
		}
	case "minio":
		return false
	}
	return true
}

func isS3PathType(endpoint string) bool {
	//localhost[:8080] 127.0.0.1[:8080]  s3.ap-southeast-1.amazonaws.com[:8080] s3-ap-southeast-1.amazonaws.com[:8080]
	pattern := `^((localhost)|(s3[.-].*\.amazonaws\.com)|((1\d{2}|2[0-4]\d|25[0-5]|[1-9]\d|[1-9])\.((1\d{2}|2[0-4]\d|25[0-5]|[1-9]\d|\d)\.){2}(1\d{2}|2[0-4]\d|25[0-5]|[1-9]\d|\d)))?(:\d*)?$`
	return regexp.MustCompile(pattern).MatchString(endpoint)
}
func createSyncStorage(uri string, conf *Config) (object.ObjectStorage, error) {
	// nolint:staticcheck
	uri = strings.TrimPrefix(uri, "sftp://")
	if !strings.Contains(uri, "://") {
		if isFilePath(uri) {
			absPath, err := filepath.Abs(uri)
			if err != nil {
				logger.Fatalf("invalid path: %s", err.Error())
			}
			if !strings.HasPrefix(absPath, "/") { // Windows path
				absPath = "/" + strings.Replace(absPath, "\\", "/", -1)
			}
			if strings.HasSuffix(uri, "/") {
				absPath += "/"
			}

			// Windows: file:///C:/a/b/c, Unix: file:///a/b/c
			uri = "file://" + absPath
		} else { // sftp
			var user string
			if strings.Contains(uri, "@") {
				parts := strings.Split(uri, "@")
				user = parts[0]
				uri = parts[1]
			}
			var pass string
			if strings.Contains(user, ":") {
				parts := strings.Split(user, ":")
				user = parts[0]
				pass = parts[1]
			}
			return object.CreateStorage("sftp", uri, user, pass, "")
		}
	}
	uri, token := extractToken(uri)
	u, err := url.Parse(uri)
	if err != nil {
		logger.Fatalf("Can't parse %s: %s", uri, err.Error())
	}
	user := u.User
	var accessKey, secretKey string
	if user != nil {
		accessKey = user.Username()
		secretKey, _ = user.Password()
	}
	name := strings.ToLower(u.Scheme)

	var endpoint string
	if name == "file" {
		endpoint = u.Path
	} else if name == "hdfs" {
		endpoint = u.Host
	} else if name == "jfs" {
		endpoint, err = url.PathUnescape(u.Host)
		if err != nil {
			return nil, fmt.Errorf("unescape %s: %s", u.Host, err)
		}
		if os.Getenv(endpoint) != "" {
			conf.Env[endpoint] = os.Getenv(endpoint)
		}
	} else if name == "nfs" {
		endpoint = u.Host + u.Path
	} else if !conf.NoHTTPS && supportHTTPS(name, u.Host) {
		endpoint = "https://" + u.Host
	} else {
		endpoint = "http://" + u.Host
	}

	isS3PathTypeUrl := isS3PathType(u.Host)
	if name == "minio" || name == "s3" && isS3PathTypeUrl {
		// bucket name is part of path
		endpoint += u.Path
	}

	store, err := object.CreateStorage(name, endpoint, accessKey, secretKey, token)
	if name == "nfs" && err != nil {
		p := u.Path
		for err != nil && strings.Contains(err.Error(), "MNT3ERR_NOENT") {
			p = filepath.Dir(p)
			store, err = object.CreateStorage(name, u.Host+p, accessKey, secretKey, token)
		}
		if err == nil {
			store = object.WithPrefix(store, u.Path[len(p):])
		}
	}
	if err != nil {
		return nil, fmt.Errorf("create %s %s: %s", name, endpoint, err)
	}

	if conf.Links {
		if _, ok := store.(object.SupportSymlink); !ok {
			logger.Warnf("storage %s does not support symlink, ignore it", uri)
			conf.Links = false
		}
	}

	if conf.Perms {
		if _, ok := store.(object.FileSystem); !ok {
			logger.Warnf("%s is not a file system, can not preserve permissions", store)
			conf.Perms = false
		}
	}
	switch name {
	case "file", "nfs":
	case "minio":
		if strings.Count(u.Path, "/") > 1 {
			// skip bucket name
			store = object.WithPrefix(store, strings.SplitN(u.Path[1:], "/", 2)[1])
		}
	case "s3":
		if isS3PathTypeUrl && strings.Count(u.Path, "/") > 1 {
			store = object.WithPrefix(store, strings.SplitN(u.Path[1:], "/", 2)[1])
		} else if len(u.Path) > 1 {
			store = object.WithPrefix(store, u.Path[1:])
		}
	default:
		if len(u.Path) > 1 {
			store = object.WithPrefix(store, u.Path[1:])
		}
	}

	return store, nil
}

func (p *ObjectProvider) GetProvider(alias string) (object.ObjectStorage, error) {
	p.Lock()
	defer p.Unlock()
	storage, ok := p.ObjMap[alias]
	var err error
	if !ok {
		var info *StorageInfo
		info, err = mq.getObject(alias)
		if err != nil {
			return nil, err
		}
		storage, err = object.CreateStorage(info.Type, info.Endpoint, info.AccessKey, info.SecretKey, "")
		if err != nil {
			return nil, err
		}
		p.ObjMap[alias] = storage
	}
	return storage, nil
}

func (t *TaskInfo) TaskId() string {
	return t.TaskIdO
}

func (t *TaskInfo) SrcAlias() string {
	return t.SrcType
}

func (t *TaskInfo) DstAlias() string {
	return t.DstType
}

func (t *TaskInfo) Key() string {
	if t.KeyO != "" {
		return path.Base(t.KeyO)
	}
	return ""
}

func (t *TaskInfo) SrcPrefix() string {
	if t.KeyO != "" {
		if !strings.HasSuffix(path.Dir(t.KeyO), "/") {
			return path.Dir(t.KeyO) + "/"
		}
	}
	return "/"
}

func (t *TaskInfo) DstPrefix() string {
	if !strings.HasSuffix(path.Dir(t.DstKeyO), "/") {
		return path.Dir(t.DstKeyO) + "/"
	}
	return "/"
}

func (t *TaskInfo) Size() int64 {
	return t.SizeO
}

func (t *TaskInfo) Mtime() time.Time {
	return t.MtimeO
}

func (t *TaskInfo) IsDir() bool {
	return t.IsDirO
}

func (t *TaskInfo) IsSymlink() bool {
	return false
}

func (t *TaskInfo) StorageClass() string {
	return t.ScO
}

func newMQ(addr string) (MQ, error) {
	if !strings.HasPrefix(addr, "redis://") {
		logger.Fatalf("mq-addr does not support %s, only support redis://xxx", addr)
	}
	return newRedisMQ(addr)
}

type redisMQ struct {
	rdb redis.UniversalClient
}

func (r *redisMQ) getObject(alias string) (*StorageInfo, error) {
	info, err := r.rdb.HGet(context.Background(), "objects", alias).Result()
	if err != nil {
		return nil, fmt.Errorf("object %s not found", alias)
	}
	v := &StorageInfo{}
	err = json.Unmarshal([]byte(info), v)
	if err != nil {
		return nil, fmt.Errorf("object %s info unmarshal failed: %s", alias, err)
	}
	return v, err
}

type workerInfo struct {
	Uuid      string
	IPAddrs   []string
	HostName  string
	Version   string
	ProcessID int
	StartTime time.Time
	Group     string
}

func newWorkerInfo() []byte {
	host, err := os.Hostname()
	if err != nil {
		logger.Warnf("Failed to get hostname: %s", err)
	}
	ips, err := utils.FindLocalIPs()
	if err != nil {
		logger.Warnf("Failed to get local IP: %s", err)
	}
	addrs := make([]string, 0, len(ips))
	for _, i := range ips {
		if ip := i.String(); ip[0] == '?' {
			logger.Warnf("Invalid IP address: %s", ip)
		} else {
			addrs = append(addrs, ip)
		}
	}
	buf, err := json.Marshal(&workerInfo{
		Version:   version.Version(),
		HostName:  host,
		IPAddrs:   addrs,
		ProcessID: os.Getpid(),
	})
	if err != nil {
		panic(err)
	}
	return buf
}

var background = context.Background()

func (r *redisMQ) register(uuid string) error {
	go func() {
		for {
			if err := r.rdb.ZAdd(background, "allWorkers", redis.Z{
				Score:  float64(time.Now().Unix()),
				Member: uuid,
			}).Err(); err != nil {
				logger.Errorf("refresh worker %s: %s", uuid, err)
			}
			time.Sleep(5 * time.Second)
		}
	}()
	return r.rdb.HSet(background, "workers", uuid, newWorkerInfo()).Err()
}
func (r *redisMQ) unregister(s string) error {
	//TODO implement me
	panic("implement me")
}

func (r *redisMQ) fetchJob(taskCh chan<- object.Object) error {
	result, err := r.rdb.XRead(background, &redis.XReadArgs{
		Streams: []string{"tasks_queue", "0"},
		Count:   1,
		Block:   0,
	}).Result()
	if err != nil {
		return err
	}

	for _, stream := range result {
		for _, message := range stream.Messages {
			size, err := strconv.ParseInt(message.Values["size"].(string), 10, 64)
			if err != nil {
				logger.Errorf("parse size %s: %s", message.Values["size"], err)
				continue
			}

			op, err := strconv.ParseInt(message.Values["op"].(string), 10, 64)
			if err != nil {
				logger.Errorf("parse op %s: %s", message.Values["size"], err)
				continue
			}

			parseBool, err := strconv.ParseBool(message.Values["isDir"].(string))
			if err != nil {
				logger.Errorf("parse size %s: %s", message.Values["size"], err)
				continue
			}
			t, err := time.Parse(time.RFC3339, message.Values["mtime"].(string))
			if err != nil {
				logger.Errorf("parse time %s: %s", message.Values["mtime"], err)
				continue
			}
			tInfo := &TaskInfo{
				TaskIdO: message.ID,
				KeyO:    message.Values["key"].(string),
				DstKeyO: message.Values["dstKey"].(string),
				SizeO:   size,
				MtimeO:  t,
				IsDirO:  parseBool,
				ScO:     message.Values["sc"].(string),
				SrcType: message.Values["srcAlias"].(string),
				DstType: message.Values["dstAlias"].(string),
			}
			if op != 0 {
				taskCh <- &withSize{
					Object: tInfo,
					nsize:  op,
				}
			} else {
				taskCh <- tInfo
			}
		}
	}
	return nil
}

func (r *redisMQ) updateJob(s string, s2 string) error {
	//TODO implement me
	panic("implement me")
}

func (r *redisMQ) updateSelfInfo(s string) error {
	//TODO implement me
	panic("implement me")
}

func newRedisMQ(addr string) (MQ, error) {
	u, err := url.Parse(addr)
	if err != nil {
		return nil, fmt.Errorf("url parse %s: %s", addr, err)
	}
	opt, err := redis.ParseURL(u.String())
	c := redis.NewClient(opt)
	return &redisMQ{rdb: c}, nil
}
