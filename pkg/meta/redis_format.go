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

package meta

import (
	"fmt"
	"strconv"

	"github.com/redis/go-redis/v9"
)

// EXEC does not roll back runtime errors. Check log inputs under WATCH before
// queuing cleanup and the format write, including the two-transition overflow.
func (m *redisMeta) checkFormatChangeLog(ctx Context, tx *redis.Tx, entries int64) error {
	if err := tx.Watch(ctx, m.txnLogKey(), m.txnLastLog()).Err(); err != nil {
		return err
	}
	typ, err := tx.Type(ctx, m.txnLogKey()).Result()
	if err != nil {
		return err
	}
	if typ != "none" && typ != "list" {
		return fmt.Errorf("invalid change-log type %s", typ)
	}
	value, err := tx.Get(ctx, m.txnLastLog()).Result()
	if err == redis.Nil {
		return nil
	} else if err != nil {
		return err
	}
	version, err := strconv.ParseInt(value, 10, 64)
	if err != nil || strconv.FormatInt(version, 10) != value {
		return fmt.Errorf("invalid change-log version %q", value)
	}
	if version > (1<<63-1)-entries {
		return fmt.Errorf("change-log version would overflow")
	}
	return nil
}
