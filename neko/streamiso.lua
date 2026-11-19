-- A VLC interface (Lua) that runs the commands VLC's remote-control port cannot carry.
--
-- The catalog writes "<sequence> <command>" to the file "command" next to the state file (STATE_FILE), and
-- this interface runs each new line once. The launcher loads it for DVDs and Blu-ray discs (--lua-intf=streamiso).
--   popup       a Blu-ray's pop-up menu: the remote's Pop-up key. During the film discs refuse the top menu, but
--               most open this one, which usually offers the way back to the main menu.
--   menu-title  the disc's main menu: a DVD's Title menu, else the Root menu of the part playing; a Blu-ray's
--               Top menu, which discs open during the film too (the Menu button of VLC's window). VLC's menu
--               key calls the Root menu first and, where there is none, jumps to a chapter of the film instead.
--   menu-<n>    a DVD's menu n of VLC's menu title (1 Root, 2 Title, 3 Chapters, 4 Subtitles, 5 Audio,
--               6 Angle). VLC calls the Root menu, then that one.

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
  elseif command == "menu-title" then
    local ok, err = pcall(vlc.var.trigger_callback, input, "menu-title")
    if not ok then vlc.msg.warn("streamiso: main menu failed: " .. tostring(err)) end
  elseif command:match("^menu%-%d$") then
    -- "title  0" (two spaces) is the variable behind VLC's menu title; its value picks the menu
    local ok, err = pcall(vlc.var.set, input, "title  0", tonumber(command:sub(6)))
    if not ok then vlc.msg.warn("streamiso: " .. command .. " failed: " .. tostring(err)) end
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
