-- A VLC interface (Lua) that runs the commands VLC's remote-control port cannot carry.
--
-- The catalog writes "<sequence> <command>" to the file "command" next to the state file (STATE_FILE), and
-- this interface runs each new line once. The launcher loads it for Blu-ray discs (--lua-intf=streamiso).
--   popup  the disc's pop-up menu: the remote's Pop-up key. During the film discs refuse the top menu, but
--          most open this one, which usually offers the way back to the main menu.

local state = os.getenv("STATE_FILE") or "/state/current"
local path = (state:match("^(.*)/[^/]*$") or ".") .. "/command"

local function read()
  local f = io.open(path, "r")
  if not f then return nil end
  local line = f:read("*l")
  f:close()
  return line
end

local function run(command)
  local input = vlc.object.input()
  if not input then return end
  if command == "popup" then
    -- the input variable behind VLC's own pop-up menu action (it sends BD_VK_POPUP to the disc)
    local ok, err = pcall(vlc.var.trigger_callback, input, "menu-popup")
    if not ok then vlc.msg.warn("streamiso: pop-up menu failed: " .. tostring(err)) end
  else
    vlc.msg.warn("streamiso: unknown command " .. command)
  end
end

-- A command written before this VLC started belongs to an earlier disc.
local last = read()
vlc.msg.info("streamiso: watching " .. path)
-- VLC 3 has no should_die(): it ends the interface while it waits in mwait().
local should_die = vlc.misc.should_die or function() return false end
while not should_die() do
  local line = read()
  if line and line ~= last then
    last = line
    local command = line:match("^%S+%s+(%S+)")
    if command then run(command) end
  end
  vlc.misc.mwait(vlc.misc.mdate() + 200000) -- 0.2 s
end
