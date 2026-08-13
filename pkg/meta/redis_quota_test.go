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

package meta

import (
	"strconv"
	"testing"

	"github.com/redis/go-redis/v9"
	"github.com/stretchr/testify/require"
)

func TestRedisLoadQuotas(t *testing.T) {
	m := newTestRedisMeta(t, 7)
	ctx := Background()
	config, err := m.getQuotaKeys(DirQuotaType)
	require.NoError(t, err)

	_, err = m.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		for id := uint64(1); id <= quotaLoadBatchSize+1; id++ {
			key := strconv.FormatUint(id, 10)
			pipe.HSet(ctx, config.usedInodesKey, key, id*2)
			pipe.HSet(ctx, config.usedSpaceKey, key, id*10)
			pipe.HSet(ctx, config.quotaKey, key, m.packQuota(int64(id*100), int64(id*3)))
		}
		// entry with usedInodes only: usedSpace and quota are missing
		pipe.HSet(ctx, config.usedInodesKey, strconv.FormatUint(quotaLoadBatchSize+2, 10), 7)
		// entry with an invalid quota value (not 16 bytes): must be skipped
		pipe.HSet(ctx, config.usedInodesKey, strconv.FormatUint(quotaLoadBatchSize+3, 10), 8)
		pipe.HSet(ctx, config.usedSpaceKey, strconv.FormatUint(quotaLoadBatchSize+3, 10), 80)
		pipe.HSet(ctx, config.quotaKey, strconv.FormatUint(quotaLoadBatchSize+3, 10), "bad")
		// garbage field: invalid key and usedInodes, both must be skipped
		pipe.HSet(ctx, config.usedInodesKey, "not-a-key", "garbage")
		return nil
	})
	require.NoError(t, err)

	dirQuotas, userQuotas, groupQuotas, err := m.doLoadQuotas(ctx)
	require.NoError(t, err)
	require.Empty(t, userQuotas)
	require.Empty(t, groupQuotas)
	require.Len(t, dirQuotas, quotaLoadBatchSize+2)

	first := dirQuotas[1]
	require.NotNil(t, first)
	require.EqualValues(t, 100, first.MaxSpace)
	require.EqualValues(t, 3, first.MaxInodes)
	require.EqualValues(t, 10, first.UsedSpace)
	require.EqualValues(t, 2, first.UsedInodes)

	last := dirQuotas[quotaLoadBatchSize+1]
	require.NotNil(t, last)
	require.EqualValues(t, (quotaLoadBatchSize+1)*100, last.MaxSpace)
	require.EqualValues(t, (quotaLoadBatchSize+1)*3, last.MaxInodes)
	require.EqualValues(t, (quotaLoadBatchSize+1)*10, last.UsedSpace)
	require.EqualValues(t, (quotaLoadBatchSize+1)*2, last.UsedInodes)

	missing := dirQuotas[quotaLoadBatchSize+2]
	require.NotNil(t, missing)
	require.EqualValues(t, -1, missing.MaxSpace)
	require.EqualValues(t, -1, missing.MaxInodes)
	require.EqualValues(t, 0, missing.UsedSpace)
	require.EqualValues(t, 7, missing.UsedInodes)

	require.Nil(t, dirQuotas[quotaLoadBatchSize+3])
}

func TestRedisLoadUGQuotas(t *testing.T) {
	m := newTestRedisMeta(t, 8)
	m.getBase().getFormat().UserGroupQuota = true
	ctx := Background()

	dirConfig, err := m.getQuotaKeys(DirQuotaType)
	require.NoError(t, err)
	userConfig, err := m.getQuotaKeys(UserQuotaType)
	require.NoError(t, err)
	groupConfig, err := m.getQuotaKeys(GroupQuotaType)
	require.NoError(t, err)

	_, err = m.rdb.Pipelined(ctx, func(pipe redis.Pipeliner) error {
		pipe.HSet(ctx, dirConfig.usedInodesKey, "10", 2)
		pipe.HSet(ctx, dirConfig.usedSpaceKey, "10", 20)
		pipe.HSet(ctx, dirConfig.quotaKey, "10", m.packQuota(200, 4))
		pipe.HSet(ctx, userConfig.usedInodesKey, "100", 5)
		pipe.HSet(ctx, userConfig.usedSpaceKey, "100", 500)
		pipe.HSet(ctx, userConfig.quotaKey, "100", m.packQuota(1000, 10))
		pipe.HSet(ctx, groupConfig.usedInodesKey, "200", 6)
		pipe.HSet(ctx, groupConfig.usedSpaceKey, "200", 600)
		pipe.HSet(ctx, groupConfig.quotaKey, "200", m.packQuota(2000, 20))
		return nil
	})
	require.NoError(t, err)

	dirQuotas, userQuotas, groupQuotas, err := m.doLoadQuotas(ctx)
	require.NoError(t, err)
	require.Len(t, dirQuotas, 1)
	require.Len(t, userQuotas, 1)
	require.Len(t, groupQuotas, 1)

	dir := dirQuotas[10]
	require.NotNil(t, dir)
	require.EqualValues(t, 200, dir.MaxSpace)
	require.EqualValues(t, 4, dir.MaxInodes)
	require.EqualValues(t, 20, dir.UsedSpace)
	require.EqualValues(t, 2, dir.UsedInodes)

	user := userQuotas[100]
	require.NotNil(t, user)
	require.EqualValues(t, 1000, user.MaxSpace)
	require.EqualValues(t, 10, user.MaxInodes)
	require.EqualValues(t, 500, user.UsedSpace)
	require.EqualValues(t, 5, user.UsedInodes)

	group := groupQuotas[200]
	require.NotNil(t, group)
	require.EqualValues(t, 2000, group.MaxSpace)
	require.EqualValues(t, 20, group.MaxInodes)
	require.EqualValues(t, 600, group.UsedSpace)
	require.EqualValues(t, 6, group.UsedInodes)
}

func TestRedisLoadQuotasOddHScan(t *testing.T) {
	m := newTestRedisMeta(t, 9)
	config, err := m.getQuotaKeys(DirQuotaType)
	require.NoError(t, err)

	quotas := make(map[uint64]*Quota)
	err = m.loadQuotaBatch(Background(), config, "dir", []string{"1", "2", "3"}, quotas)
	require.Error(t, err)
	require.Empty(t, quotas)
}
