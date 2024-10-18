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
	"encoding/json"
	"errors"
	"fmt"
	"github.com/juicedata/juicefs/pkg/object"
	"github.com/redis/go-redis/v9"
	"net/url"
	"strconv"
	"time"
)

const (
	sessionsKey    = "sessions"
	workersKey     = "workers"
	taskStreamKey  = "taskStream"
	storageInfoKey = "storageInfo"
	taskStatusKey  = "taskStatus"
)

type redisMQ struct {
	rdb redis.UniversalClient
}

func (r *redisMQ) getTaskStatus(taskId string) (*TaskStatus, error) {
	var ts TaskStatus
	result, err := r.rdb.HGet(background, taskStatusKey, taskId).Result()
	if errors.Is(err, redis.Nil) {
		return &TaskStatus{TaskId: taskId}, nil
	}
	if err != nil {
		return nil, err
	}
	err = json.Unmarshal([]byte(result), &ts)
	if err != nil {
		return nil, err
	}
	return &ts, nil
}

func (r *redisMQ) updateTaskStatus(ts *TaskStatus) error {
	jsonBytes, err := json.Marshal(ts)
	if err != nil {
		return err
	}
	return r.rdb.HSet(background, taskStatusKey, ts.TaskId, jsonBytes).Err()
}

func (r *redisMQ) getStorageInfo(alias string) (*StorageInfo, error) {
	info, err := r.rdb.HGet(background, storageInfoKey, alias).Result()
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

func (r *redisMQ) register() error {
	sprintf := fmt.Sprintf("%s-%s", syncConfig.Group, syncConfig.Consumer)
	go func() {
		for {
			if err := r.rdb.ZAdd(background, sessionsKey, redis.Z{
				Score:  float64(time.Now().Unix()),
				Member: sprintf,
			}).Err(); err != nil {
				logger.Errorf("refresh worker %s: %s", sprintf, err)
			}
			time.Sleep(5 * time.Second)
		}
	}()
	return r.rdb.HSet(background, workersKey, sprintf, newWorkerInfo()).Err()
}

func (r *redisMQ) unregister(s string) error {
	//todo: update the stop time in workerInfo
	panic("implement me")
}

func (r *redisMQ) fetchTask(taskCh chan<- object.Object) error {
	result, err := r.rdb.XReadGroup(background, &redis.XReadGroupArgs{
		Group:    syncConfig.Group,
		Consumer: syncConfig.Consumer,
		Streams:  []string{taskStreamKey, ">"},
		Count:    1,
	}).Result()
	logger.Debugf("fetch task %v", result)
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
			tInfo := &taskInfo{
				TaskId_:   message.ID,
				Key_:      message.Values["srcPath"].(string),
				DstKey_:   message.Values["dstPath"].(string),
				Size_:     size,
				Mtime_:    t,
				IsDir_:    parseBool,
				Sc_:       message.Values["sc"].(string),
				SrcAlias_: message.Values["srcAlias"].(string),
				DstAlias_: message.Values["dstAlias"].(string),
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

	//todo: Scan the pending list to put messages that have been missing for a long time back into the queue
	// r.rdb.XPending()
	return nil
}

func (r *redisMQ) ackTask(taskId string) error {
	return r.rdb.XAck(background, taskStreamKey, syncConfig.Group, taskId).Err()
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
