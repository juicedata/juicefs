/*
 * Copyright 2023 Alibaba Cloud, Inc. or its affiliates.
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

package object

import (
	"io"
)

type dummystore struct {
	DefaultObjectStorage
}

func (d *dummystore) String() string {
	return "dummy"
}

func (d *dummystore) Get(_key string, _off, _limit int64) (io.ReadCloser, error) {
	return nil, notSupported
}

func (d *dummystore) Put(_key string, _in io.Reader) error {
	return nil
}

func (d *dummystore) Delete(_key string) error {
	return nil
}

func (d *dummystore) CompleteUpload(_key string, _uploadID string, _parts []*Part) error {
	return nil
}

func newDummyStore(root, accesskey, secretkey, token string) (ObjectStorage, error) {
	return &dummystore{}, nil
}

func init() {
	Register("dummy", newDummyStore)
}
