-- Pandoc filter: one book Markdown file -> Typst for the printed volumes.
--
-- Metadata (from the build's metadata file):
--   self     the file's book-relative path, e.g. vol1/07-cluster-state.md
--   key      the file's label prefix, e.g. ch07 or appA
--   filemap  book-relative path -> key, for every chapter and appendix
--
-- It (1) prefixes every heading identifier with the file's key so labels are
-- unique across the volume, (2) rewrites links between book files to those
-- labels, (3) sizes diagrams from their SVG viewBox and (4) turns the
-- At-a-glance block quote into the template's glance box.

local self_path, self_key, filemap = "", "", {}

-- Diagram scale: D2 draws text at 16px; at 0.5pt per px it prints at 8pt.
local PT_PER_PX = 0.5
local MAX_W_PT = 440
local MAX_H_PT = 620

local function dirname(p)
  return p:match("^(.*)/[^/]*$") or ""
end

-- clean joins a relative path onto a directory and resolves ".." segments.
local function clean(dir, rel)
  local parts = {}
  for seg in ((dir ~= "" and dir .. "/" or "") .. rel):gmatch("[^/]+") do
    if seg == ".." then
      table.remove(parts)
    elseif seg ~= "." then
      table.insert(parts, seg)
    end
  end
  return table.concat(parts, "/")
end

function Meta(meta)
  self_path = pandoc.utils.stringify(meta.self)
  self_key = pandoc.utils.stringify(meta.key)
  for k, v in pairs(meta.filemap or {}) do
    filemap[k] = pandoc.utils.stringify(v)
  end
end

local function Header(el)
  if el.level == 1 then
    el.identifier = self_key
  else
    el.identifier = self_key .. "-" .. el.identifier
  end
  return el
end

local function Link(el)
  local target = el.target
  if target:match("^%a+://") or target:match("^mailto:") then
    return el
  end
  local path, frag = target:match("^([^#]*)#?(.*)$")
  local key = self_key
  if path ~= "" then
    key = filemap[clean(dirname(self_path), path)]
    if not key then
      return el.content
    end
  end
  el.target = "#" .. key .. (frag ~= "" and ("-" .. frag) or "")
  return el
end

-- svgSize reads the viewBox of an SVG to size it on the page.
local function svgSize(path)
  local f = io.open(path, "r")
  if not f then
    return nil
  end
  local head = f:read(4096) or ""
  f:close()
  local w, h = head:match('viewBox="[%d%.%-]+ [%d%.%-]+ ([%d%.]+) ([%d%.]+)"')
  return tonumber(w), tonumber(h)
end

local function figureTypst(src, caption)
  local rootPath = "/" .. clean(dirname(self_path), src)
  local w, h = svgSize(PANDOC_STATE.resource_path[1] .. rootPath)
  local width = "100%"
  if w and h then
    local scale = math.min(1, MAX_W_PT / (w * PT_PER_PX), MAX_H_PT / (h * PT_PER_PX))
    width = string.format("%.1fpt", w * PT_PER_PX * scale)
  end
  local cap = pandoc.write(pandoc.Pandoc({ pandoc.Plain(caption) }), "typst")
  return string.format('#figure(image("%s", width: %s), caption: [%s])', rootPath, width, cap:gsub("%s+$", ""))
end

local function Figure(el)
  local img
  el.content:walk({ Image = function(i) img = i end })
  if not img then
    return el
  end
  local caption = pandoc.utils.blocks_to_inlines(el.caption.long)
  return pandoc.RawBlock("typst", figureTypst(img.src, caption))
end

local function BlockQuote(el)
  local first = el.content[1]
  if not first or first.t ~= "Para" then
    return el
  end
  local lead = first.content[1]
  if not (lead and lead.t == "Strong" and pandoc.utils.stringify(lead) == "At a glance.") then
    return el
  end
  local blocks = { pandoc.RawBlock("typst", "#glance[") }
  for i = 2, #el.content do
    table.insert(blocks, el.content[i])
  end
  table.insert(blocks, pandoc.RawBlock("typst", "]"))
  return blocks
end

return {
  { Meta = Meta },
  { Header = Header, Link = Link, Figure = Figure, BlockQuote = BlockQuote },
}
