/*
 * JuiceFS, Copyright (C) 2021 Juicedata, Inc.
 *
 * This program is free software: you can use, redistribute, and/or modify
 * it under the terms of the GNU Affero General Public License, version 3
 * or later ("AGPL"), as published by the Free Software Foundation.
 *
 * This program is distributed in the hope that it will be useful, but WITHOUT
 * ANY WARRANTY; without even the implied warranty of MERCHANTABILITY or
 * FITNESS FOR A PARTICULAR PURPOSE.
 *
 * You should have received a copy of the GNU Affero General Public License
 * along with this program. If not, see <http://www.gnu.org/licenses/>.
 */

package main

import (
	"os/exec"
	"strconv"
	"strings"
)

func genAllUids() []pwent {
	out, err := exec.Command("wmic", "useraccount", "list", "brief").Output()
	if err != nil {
		logger.Errorf("cmd : %s", err)
		return nil
	}
	lines := strings.Split(string(out), "\r\n")
	if len(lines) < 2 {
		logger.Errorf("no uids: %s", string(out))
		return nil
	}
	var uids []pwent
	for _, line := range lines[1 : len(lines)-1] {
		fields := strings.Fields(line)
		if len(fields) < 5 {
			continue
		}
		name := fields[len(fields)-2]
		sid := fields[len(fields)-1]
		ps := strings.Split(sid, "-")
		auth, _ := strconv.Atoi(ps[2])
		count := len(ps) - 3
		var subAuth int
		if count > 0 {
			subAuth, _ = strconv.Atoi(ps[3])
		}
		rid, _ := strconv.Atoi(ps[len(ps)-1])
		var uid int
		if auth == 5 {
			if count == 1 {
				// "SYSTEM" S-1-5-18                   <=> uid/gid: 18
				uid = rid
			} else if count == 2 && subAuth == 32 {
				// "Users"  S-1-5-32-545               <=> uid/gid: 545
				uid = rid
			} else if count >= 2 && subAuth == 5 {
				// not supported
			} else if count >= 5 && subAuth == 21 {
				// S-1-5-21-X-Y-Z-RID                  <=> uid/gid: 0x30000 + RID
				// S-1-5-21-X-Y-Z-RID                  <=> uid/gid: 0x100000 + RID
				uid = 0x30000 + rid
			} else if count == 2 {
				// S-1-5-X-RID                         <=> uid/gid: 0x1000 * X + RID
				uid = 0x1000*subAuth + rid
			}
		} else if auth == 16 {
			// S-1-16-RID                          <=> uid/gid: 0x60000 + RID
			uid = 0x60000*subAuth + rid
		}
		if uid > 0 {
			uids = append(uids, pwent{uid, name})
			logger.Tracef("found account %s -> %d (%s)", name, uid, sid)
		}
	}
	return uids
}

func genAllGids() []pwent {
	out, err := exec.Command("wmic", "group", "list", "brief").Output()
	if err != nil {
		logger.Errorf("cmd : %s", err)
		return nil
	}
	lines := strings.Split(string(out), "\r\n")
	if len(lines) < 2 {
		logger.Errorf("no gids: %s", string(out))
		return nil
	}
	title := lines[0]
	nameIndex := strings.Index(title, "Name")
	sidIndex := strings.Index(title, "SID")
	var gids []pwent
	for _, line := range lines[1 : len(lines)-1] {
		if len(line) < sidIndex {
			continue
		}
		name := strings.TrimSpace(line[nameIndex : sidIndex-1])
		sid := strings.TrimSpace(line[sidIndex:])
		ps := strings.Split(sid, "-")
		auth, _ := strconv.Atoi(ps[2])
		count := len(ps) - 3
		var subAuth int
		if count > 0 {
			subAuth, _ = strconv.Atoi(ps[3])
		}
		rid, _ := strconv.Atoi(ps[len(ps)-1])
		var gid int
		if auth == 5 {
			if count == 1 {
				// "SYSTEM" S-1-5-18                   <=> uid/gid: 18
				gid = rid
			} else if count == 2 && subAuth == 32 {
				// "Users"  S-1-5-32-545               <=> uid/gid: 545
				gid = rid
			} else if count >= 2 && subAuth == 5 {
				// not supported
			} else if count >= 5 && subAuth == 21 {
				// S-1-5-21-X-Y-Z-RID                  <=> uid/gid: 0x30000 + RID
				// S-1-5-21-X-Y-Z-RID                  <=> uid/gid: 0x100000 + RID
				gid = 0x30000 + rid
			} else if count == 2 {
				// S-1-5-X-RID                         <=> uid/gid: 0x1000 * X + RID
				gid = 0x1000*subAuth + rid
			}
		} else if auth == 16 {
			// S-1-16-RID                          <=> uid/gid: 0x60000 + RID
			gid = 0x60000*subAuth + rid
		}
		if gid > 0 {
			gids = append(gids, pwent{gid, name})
			logger.Tracef("found group %s -> %d (%s)", name, gid, sid)
		}
	}
	return gids
}
