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

package acl

import (
	"testing"

	"github.com/stretchr/testify/assert"
)

func TestCache(t *testing.T) {
	rule := &Rule{
		Owner: 6,
		Group: 4,
		Mask:  4,
		Other: 4,
		NamedUsers: Entries{
			{
				Id:   2,
				Perm: 2,
			},
			{
				Id:   1,
				Perm: 1,
			},
		},
		NamedGroups: Entries{
			{
				Id:   4,
				Perm: 4,
			},
			{
				Id:   3,
				Perm: 3,
			},
		},
	}

	c := NewCache()
	c.Put(1, rule)
	c.Put(2, rule)
	assert.True(t, rule.IsEqual(c.Get(1)))
	assert.True(t, rule.IsEqual(c.Get(2)))
	assert.Equal(t, uint32(1), c.GetId(rule))

	rule2 := &Rule{}
	*rule2 = *rule
	rule2.Owner = 4

	c.Put(3, rule2)
	assert.Equal(t, uint32(3), c.GetId(rule2))

	c.Put(8, rule2)
	assert.Equal(t, []uint32{4, 5, 6, 7}, c.GetMissIds())

	assert.NotPanics(t, func() {
		c.Put(10, nil)
	})
}
