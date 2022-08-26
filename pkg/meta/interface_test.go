/*
 * JuiceFS, Copyright 2022 Juicedata, Inc.
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
	"os"
	"testing"
)

func Test_setPasswordFromEnv(t *testing.T) {
	os.Setenv("META_PASSWORD", "dbPasswd")
	defer os.Unsetenv("META_PASSWORD")
	tests := []struct {
		args string
		want string
	}{
		//mysql
		{
			args: "mysql://root:password@(127.0.0.1:3306)/juicefs",
			want: "mysql://root:password@(127.0.0.1:3306)/juicefs",
		},
		{
			args: "mysql://root:@(127.0.0.1:3306)/juicefs",
			want: "mysql://root:dbPasswd@(127.0.0.1:3306)/juicefs",
		},
		{
			args: "mysql://root@(127.0.0.1:3306)/juicefs",
			want: "mysql://root:dbPasswd@(127.0.0.1:3306)/juicefs",
		},
		// no user is ok
		{
			args: "mysql://:@(127.0.0.1:3306)/juicefs",
			want: "mysql://:dbPasswd@(127.0.0.1:3306)/juicefs",
		},
		{
			args: "mysql://:pwd@(127.0.0.1:3306)/juicefs",
			want: "mysql://:pwd@(127.0.0.1:3306)/juicefs",
		},
		{
			args: "mysql://a:b:c:@(127.0.0.1:3306)/juicefs",
			want: "",
		},
		//postgres
		{
			args: "postgres://root:password@192.168.1.6:5432/juicefs",
			want: "postgres://root:password@192.168.1.6:5432/juicefs",
		},
		{
			args: "postgres://root:@192.168.1.6:5432/juicefs",
			want: "postgres://root:dbPasswd@192.168.1.6:5432/juicefs",
		},
		{
			args: "postgres://root@192.168.1.6:5432/juicefs",
			want: "postgres://root:dbPasswd@192.168.1.6:5432/juicefs",
		},
		{
			args: "postgres://root@/pgtest?host=/tmp/pgsocket/&port=5433",
			want: "postgres://root:dbPasswd@/pgtest?host=/tmp/pgsocket/&port=5433",
		},
		{
			args: "postgres://@/pgtest?host=/tmp/pgsocket/&port=5433&user=pguser",
			want: "postgres://:dbPasswd@/pgtest?host=/tmp/pgsocket/&port=5433&user=pguser",
		},
	}
	for _, tt := range tests {
		t.Run("", func(t *testing.T) {
			if got, _ := setPasswordFromEnv(tt.args); got != tt.want {
				t.Errorf("setPasswordFromEnv() = %v, want %v", got, tt.want)
			}
		})
	}
}
