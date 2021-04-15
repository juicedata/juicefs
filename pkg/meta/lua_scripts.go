/*
 * JuiceFS, Copyright (C) 2020 Juicedata, Inc.
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

package meta

const scriptLookup = `
local buf = redis.call('HGET', KEYS[1], KEYS[2])
if not buf then
       return false
end
if string.len(buf) ~= 9 then
       return {err=string.format("Invalid entry data: %s", buf)}
end
local ino = string.unpack(">I8", string.sub(buf, 2))
return {ino, redis.call('GET', "i" .. tostring(ino))}
`

//nolint
const scriptResolve = `
local function parse_attr(buf)
    local buf_len = string.len(buf)
    local x = {}
    if buf_len ~= 72 and buf_len ~= 64 then
        error("Invalid attr")
    end
    local format = ">BHI4I4I8I4I8I4I8I4I4I8I4"
    if buf_len == 72 then
        format = format .. "I8"
    end
    x.flags, x.mode, x.uid, x.gid,
    x.atime, x.atime_nsec,
    x.mtime, x.mtime_nsec,
    x.ctime, x.ctime_nsec,
    x.nlink, x.length, x.rdev, x.parent = string.unpack(format, buf)
    x.mode = x.mode & 0xfff
    return x
end

local function get_attr(ino)
    -- TODO: Handle errors
    local encoded_attr = redis.call('GET', "i" .. tostring(ino))
    return parse_attr(encoded_attr)
end

local function lookup(parent_ino, name)
    local buf = redis.call('HGET', parent_ino, name)
    if not buf then
        return false
    end
    if string.len(buf) ~= 9 then
        return {err=string.format("Invalid entry data: %s", buf)}
    end
    local ino = string.unpack(">I8", string.sub(buf, 2))
    return {ino, get_attr(ino)}
end

local function can_access(ino, uid, gid)
    if uid == 0 then
        return 0
    end

    attr = get_attr(ino)
    local mask_x = 1
    if attr.uid == uid then
        mode = (attr.mode >> 6) & 7
    elseif attr.gid == gid then
        mode = (attr.mode >> 3) & 7
    else
        mode = attr.mode & 7
    end
    return mode&mask_x == mask_x
end
`
