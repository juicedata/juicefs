/*
 * JuiceFS, Copyright 2026 Juicedata, Inc.
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

package fs

import (
	"fmt"
	"reflect"

	"github.com/juicedata/juicefs/pkg/meta"
	"github.com/juicedata/juicefs/pkg/object"
)

type storageHolder struct {
	object.ObjectStorage
	config object.VolumeConfig
}

func (h *storageHolder) Shutdown() {
	object.Shutdown(h.ObjectStorage)
}

// NewReloadableStorage creates object storage and recreates it when storage settings change on reload.
func NewReloadableStorage(format *meta.Format, m meta.Meta, patch func(*meta.Format)) (object.ObjectStorage, error) {
	format = format.Clone()
	if patch != nil {
		patch(format)
	}
	config, err := format.VolumeConfig()
	if err != nil {
		return nil, err
	}
	blob, err := object.CreateVolumeStorage(config)
	if err != nil {
		return nil, err
	}
	holder := &storageHolder{ObjectStorage: blob, config: config}
	m.OnReload(func(format *meta.Format) error {
		if patch != nil {
			patch(format)
		}
		config, err := format.VolumeConfig()
		if err != nil {
			return fmt.Errorf("object storage: %w", err)
		}
		old := &holder.config
		if config.Storage == old.Storage && config.Bucket == old.Bucket && config.AccessKey == old.AccessKey &&
			config.SecretKey == old.SecretKey && config.SessionToken == old.SessionToken && reflect.DeepEqual(config.Tiers, old.Tiers) {
			return nil
		}
		logger.Infof("found new configuration: storage=%q bucket=%q ak=%q storageClass=%q tiers=%v", config.Storage, config.Bucket, config.AccessKey, config.Tiers[0].Sc, config.Tiers)
		blob, err := object.CreateVolumeStorage(config)
		if err != nil {
			return fmt.Errorf("object storage: %w", err)
		}
		holder.ObjectStorage = blob
		holder.config = config
		return nil
	})
	return holder, nil
}
