local parse = function(buf, idx, pos)
    return bit.lshift(string.byte(buf, idx), pos)
end

local buf = redis.call('HGET', KEYS[1], KEYS[2])
if not buf then
    return false
end
if string.len(buf) ~= 9 then
    return {err=string.format("Invalid entry data: %s", buf)}
end
buf = string.sub(buf, 2)
local ino =  parse(buf, 1, 56) +
        parse(buf, 2, 48) +
        parse(buf, 3, 40) +
        parse(buf, 4, 32) +
        parse(buf, 5, 24) +
        parse(buf, 6, 16) +
        parse(buf, 7, 8) +
        parse(buf, 8, 0)
return {ino, redis.call('GET', "i" .. tostring(ino))}
