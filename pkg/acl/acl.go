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
	for i := 0; i < es.Len(); i++ {
		if (*es)[i].Id != (*other)[i].Id || (*es)[i].Perm != (*other)[i].Perm {
			return false
		}
	}
	return true
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

// Rule acl rule
type Rule struct {
	Owner       uint16
	Group       uint16
	Mask        uint16
	Other       uint16
	NamedUsers  Entries
	NamedGroups Entries
}

func (r *Rule) String() string {
	return fmt.Sprintf("owner %o, group %o, mask %o, other %o, named users: %+v, named group %+v",
		r.Owner, r.Group, r.Mask, r.Other, r.NamedUsers, r.NamedGroups)
}

func (r *Rule) Dup() *Rule {
	if r != nil {
		newRule := *r
		// NamedUsers and NamedGroups are never modified
		return &newRule
	}
	return nil
}

func (r *Rule) Encode() []byte {
	w := utils.NewBuffer(uint32(16 + (len(r.NamedUsers)+len(r.NamedGroups))*6))
	w.Put16(r.Owner)
	w.Put16(r.Group)
	w.Put16(r.Mask)
	w.Put16(r.Other)
	w.Put32(uint32(len(r.NamedUsers)))
	for _, entry := range r.NamedUsers {
		w.Put32(entry.Id)
		w.Put16(entry.Perm)
	}
	w.Put32(uint32(len(r.NamedGroups)))
	for _, entry := range r.NamedGroups {
		w.Put32(entry.Id)
		w.Put16(entry.Perm)
	}
	return w.Bytes()
}

func (r *Rule) Decode(buf []byte) {
	rb := utils.ReadBuffer(buf)
	r.Owner = rb.Get16()
	r.Group = rb.Get16()
	r.Mask = rb.Get16()
	r.Other = rb.Get16()
	uCnt := rb.Get32()
	r.NamedUsers = make([]Entry, uCnt)
	for i := 0; i < int(uCnt); i++ {
		r.NamedUsers[i].Id = rb.Get32()
		r.NamedUsers[i].Perm = rb.Get16()
	}

	gCnt := rb.Get32()
	r.NamedGroups = make([]Entry, gCnt)
	for i := 0; i < int(gCnt); i++ {
		r.NamedGroups[i].Id = rb.Get32()
		r.NamedGroups[i].Perm = rb.Get16()
	}
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

// IsMinimal just like normal permission
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

// InheritPerms from normal permission
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

// ChildAccessACL return the child node access acl with this default acl
func (r *Rule) ChildAccessACL(mode uint16) *Rule {
	cRule := &Rule{}
	cRule.Owner = (mode >> 6) & 7 & r.Owner
	cRule.Mask = (mode >> 3) & 7 & r.Mask
	cRule.Other = mode & 7 & r.Other

	cRule.Group = r.Group
	cRule.NamedUsers = r.NamedUsers
	cRule.NamedGroups = r.NamedGroups
	return cRule
}

var crc32c = crc32.MakeTable(crc32.Castagnoli)

func (r *Rule) Checksum() uint32 {
	return crc32.Checksum(r.Encode(), crc32c)
}

func (r *Rule) CanAccess(uid uint32, gids []uint32, fUid, fGid uint32, mMask uint8) bool {
	if uid == fUid {
		return uint8(r.Owner&7)&mMask == mMask
	}
	for _, nUser := range r.NamedUsers {
		if uid == nUser.Id {
			return uint8(nUser.Perm&r.Mask&7)&mMask == mMask
		}
	}

	isGrpMatched := false
	for _, gid := range gids {
		if gid == fGid {
			if uint8(r.Group&r.Mask&7)&mMask == mMask {
				return true
			}
			isGrpMatched = true
		}
	}
	for _, gid := range gids {
		for _, nGrp := range r.NamedGroups {
			if gid == nGrp.Id {
				if uint8(nGrp.Perm&r.Mask&7)&mMask == mMask {
					return true
				}
				isGrpMatched = true
			}
		}
	}
	if isGrpMatched {
		return false
	}

	return uint8(r.Other&7)&mMask == mMask
}

const (
	TypeNone = iota
	TypeAccess
	TypeDefault
)
