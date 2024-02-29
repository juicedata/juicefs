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
	"fmt"
	"hash/crc32"
	"sort"
	"strings"

	"github.com/juicedata/juicefs/pkg/utils"
)

const Version uint8 = 2

type Entry struct {
	Id   uint32
	Perm uint16
}

type Entries []Entry

func (es *Entries) Len() int           { return len(*es) }
func (es *Entries) Less(i, j int) bool { return (*es)[i].Id < (*es)[j].Id }
func (es *Entries) Swap(i, j int)      { (*es)[i], (*es)[j] = (*es)[j], (*es)[i] }

func (es *Entries) IsEqual(other *Entries) bool {
	if es.Len() != other.Len() {
		return false
	}
	sort.Sort(es)
	sort.Sort(other)
	for i := 0; i < es.Len(); i++ {
		if (*es)[i].Id != (*other)[i].Id || (*es)[i].Perm != (*other)[i].Perm {
			return false
		}
	}
	return true
}

func (es *Entries) String() string {
	var builder strings.Builder
	builder.WriteString("{")
	for _, e := range *es {
		builder.WriteString(fmt.Sprintf("{id: %d, perm: %o}, ", e.Id, e.Perm))
	}
	builder.WriteString("}")
	return builder.String()
}

func (es *Entries) Encode() []byte {
	w := utils.NewBuffer(uint32(es.Len() * 6))
	for _, e := range *es {
		w.Put32(e.Id)
		w.Put16(e.Perm)
	}
	return w.Bytes()
}

func (es *Entries) Decode(data []byte) {
	r := utils.ReadBuffer(data)
	for r.HasMore() {
		*es = append(*es, Entry{
			Id:   r.Get32(),
			Perm: r.Get16(),
		})
	}
}

type Rule struct {
	Owner       uint16
	Group       uint16
	Mask        uint16
	Other       uint16
	NamedUsers  Entries
	NamedGroups Entries
}

func (r *Rule) String() string {
	return fmt.Sprintf("owner %o, group %o, mask %o, other %o, named users: %s, named group %s",
		r.Owner, r.Group, r.Mask, r.Other, r.NamedUsers.String(), r.NamedGroups.String())
}

func (r *Rule) Encode() []byte {
	w := utils.NewBuffer(uint32(8 + (len(r.NamedUsers)+len(r.NamedGroups))*6))
	w.Put16(r.Owner)
	w.Put16(r.Group)
	w.Put16(r.Mask)
	w.Put16(r.Other)
	for _, entry := range r.NamedUsers {
		w.Put32(entry.Id)
		w.Put16(entry.Perm)
	}
	for _, entry := range r.NamedGroups {
		w.Put32(entry.Id)
		w.Put16(entry.Perm)
	}
	return w.Bytes()
}

func EmptyRule() *Rule {
	return &Rule{
		Owner: 0xFFFF,
		Group: 0xFFFF,
		Other: 0xFFFF,
		Mask:  0xFFFF,
	}
}

func (r *Rule) IsEmpty() bool {
	return len(r.NamedUsers)+len(r.NamedGroups) == 0 &&
		r.Owner&r.Group&r.Other&r.Mask == 0xFFFF
}

func (r *Rule) IsMinimal() bool {
	return len(r.NamedGroups)+len(r.NamedUsers) == 0 && r.Mask == 0xFFFF
}

func (r *Rule) IsEqual(other *Rule) bool {
	if r.Owner != other.Owner || r.Group != other.Group || r.Mask != other.Mask || r.Other != other.Other {
		return false
	}

	return r.NamedUsers.IsEqual(&other.NamedUsers) &&
		r.NamedGroups.IsEqual(&other.NamedGroups)
}

func (r *Rule) InheritPerms(mode uint16) {
	if r.Owner == 0xFFFF {
		r.Owner = (mode >> 6) & 7
	}
	if r.Group == 0xFFFF {
		r.Group = (mode >> 3) & 7
	}
	if r.Other == 0xFFFF {
		r.Other = mode & 7
	}
}

func (r *Rule) SetMode(mode uint16) {
	r.Owner &= 0xFFF8
	r.Owner |= (mode >> 6) & 7

	if r.IsMinimal() {
		r.Group &= 0xFFF8
		r.Group |= (mode >> 3) & 7
	} else {
		r.Mask &= 0xFFF8
		r.Mask |= (mode >> 3) & 7
	}
	r.Other &= 0xFFF8
	r.Other |= mode & 7
}

func (r *Rule) GetMode() uint16 {
	if r.IsMinimal() {
		return ((r.Owner & 7) << 6) | ((r.Group & 7) << 3) | (r.Other & 7)
	}
	return ((r.Owner & 7) << 6) | ((r.Mask & 7) << 3) | (r.Other & 7)
}

func (r *Rule) ChildAccessACL(mode uint16) *Rule {
	cRule := &Rule{}
	cRule.Owner = (mode >> 6) & 7
	cRule.Owner &= r.Owner

	cRule.Mask = (mode >> 3) & 7
	cRule.Mask &= r.Mask

	cRule.Other = mode & 7
	cRule.Other &= r.Other

	cRule.Group = r.Group
	cRule.NamedUsers = r.NamedUsers
	cRule.NamedGroups = r.NamedGroups
	return cRule
}

var crc32c = crc32.MakeTable(crc32.Castagnoli)

func (r *Rule) Checksum() uint32 {
	return crc32.Checksum(r.Encode(), crc32c)
}

const (
	TypeNone = iota
	TypeAccess
	TypeDefault
)
