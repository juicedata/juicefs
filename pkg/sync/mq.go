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
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/juicedata/juicefs/pkg/utils"
	"github.com/juicedata/juicefs/pkg/version"
	"os"
	"strings"
	"sync"
	"time"
)

type MQ interface {
	register() error
	unregister(string) error
	fetchTask(chan<- object.Object) error
	ackTask(taskId string) error
	getTaskStatus(taskId string) (*TaskStatus, error)
	updateTaskStatus(*TaskStatus) error
	getStorageInfo(alias string) (*StorageInfo, error)
}

var background = context.Background()

type TaskStatus struct {
	TaskId string

	SrcPath      string
	DstPath      string
	Size         int64
	Mtime        time.Time
	IsDir        bool
	StorageClass string
	SrcAlias     string
	DstAlias     string
	Operate      int

	EnqueueTime time.Time
	StartTime   time.Time
	EndTime     time.Time
	ExitCode    string
	Message     string
}

type StorageInfo struct {
	Alias     string
	Type      string
	Endpoint  string
	AccessKey string
	SecretKey string
}

type workerInfo struct {
	IPAddrs   []string
	HostName  string
	Version   string
	ProcessID int
	StartTime time.Time
	StopTime  time.Time
	Group     string
	Consumer  string
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
		StartTime: time.Now(),
		Group:     syncConfig.Group,
		Consumer:  syncConfig.Consumer,
	})
	if err != nil {
		panic(err)
	}
	return buf
}

type storageProvider struct {
	sync.Mutex
	ObjMap map[string]object.ObjectStorage
	mq     MQ
}

func (p *storageProvider) GetProvider(alias string) (object.ObjectStorage, error) {
	p.Lock()
	defer p.Unlock()
	storage, ok := p.ObjMap[alias]
	var err error
	if !ok {
		var info *StorageInfo
		info, err = mq.getStorageInfo(alias)
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

type taskInfo struct {
	TaskId_   string
	Key_      string
	DstKey_   string
	Size_     int64
	Mtime_    time.Time
	IsDir_    bool
	Sc_       string
	SrcAlias_ string
	DstAlias_ string
}

func (t *taskInfo) DstKey() string {
	return t.DstKey_
}

func (t *taskInfo) TaskId() string {
	return t.TaskId_
}

func (t *taskInfo) SrcAlias() string {
	return t.SrcAlias_
}

func (t *taskInfo) DstAlias() string {
	return t.DstAlias_
}

func (t *taskInfo) Key() string {
	return t.Key_
}

func (t *taskInfo) Size() int64 {
	return t.Size_
}

func (t *taskInfo) Mtime() time.Time {
	return t.Mtime_
}

func (t *taskInfo) IsDir() bool {
	return t.IsDir_
}

func (t *taskInfo) IsSymlink() bool {
	return false
}

func (t *taskInfo) StorageClass() string {
	return t.Sc_
}

func newMQ(addr string) (MQ, error) {
	if !strings.HasPrefix(addr, "redis://") {
		logger.Fatalf("mq-addr does not support %s, only support redis://xxx", addr)
	}
	return newRedisMQ(addr)
}
