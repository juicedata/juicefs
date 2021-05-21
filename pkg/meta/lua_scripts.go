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

//nolint
package meta

const scriptLookup = `
local buf = redis.call('HGET', KEYS[1], KEYS[2])
if not buf then
       return false
end
local ino = struct.unpack(">I8", string.sub(buf, 2))
return {ino, redis.call('GET', "i" .. tostring(ino))}
`

const scriptResolve = `
local function unpack_attr(buf)
    local x = {}
    x.flags, x.mode, x.uid, x.gid = struct.unpack(">BHI4I4", string.sub(buf, 0, 11))
    x.type = math.floor(x.mode / 4096) % 8
    x.mode = x.mode % 4096
    return x
end

local function get_attr(ino)
    local encoded_attr = redis.call('GET', "i" .. tostring(ino))
    if not encoded_attr then
        error("ENOENT")
    end
    return unpack_attr(encoded_attr)
end

local function lookup(parent, name)
    local buf = redis.call('HGET', "d" .. tostring(parent), name)
    if not buf then
        error("ENOENT")
    end
    return struct.unpack(">BI8", buf)
end

local function can_access(ino, uid, gid)
    if uid == 0 then
        return true
    end

    local attr = get_attr(ino)
    local mode = 0
    if attr.uid == uid then
        mode = math.floor(attr.mode / 64) % 8
    elseif attr.gid == gid then
        mode = math.floor(attr.mode / 8) % 8
    else
        mode = attr.mode % 8
    end
    return mode % 2 == 1
end

local function resolve(parent, path, uid, gid)
    local _type = 2
    for name in string.gmatch(path, "[^/]+") do
        if _type == 3 then
            error("ENOTSUP")
        elseif _type ~= 2 then
            error("ENOTDIR")
        elseif parent > 1 and not can_access(parent, uid, gid) then 
            error("EACCESS")
        end
        _type, parent = lookup(parent, name)
    end
    return {parent, redis.call('GET', "i" .. tostring(parent))}
end

return resolve(tonumber(KEYS[1]), KEYS[2], tonumber(KEYS[3]), tonumber(KEYS[4]))
`
