-- The What's new page (docs/CHANGES.md). Drops the deploy markers, turns the
-- Feature: and Fix: words into labels, and wraps each day heading in a
-- <time> element. Anything it does not understand stops the build, so a
-- malformed file never ships from a careless rebuild.
-- docs/guide/changes_test.go checks the same format without pandoc.

local months = {
  January = 1, February = 2, March = 3, April = 4, May = 5, June = 6,
  July = 7, August = 8, September = 9, October = 10, November = 11, December = 12,
}

-- The colon stays for screen readers and for the page without its style,
-- so the label does not run into the sentence; the eye has the tag's shape.
local tags = {
  ["Feature:"] = '<span class="tag tag-feature">Feature</span><span class="vh">:</span>',
  ["Fix:"] = '<span class="tag tag-fix">Fix</span><span class="vh">:</span>',
}

-- <!-- through HASH --> names the last commit a deploy shipped; it is for
-- the next deploy, not for readers.
function RawBlock(b)
  if b.format == "html" and b.text:match("^<!%-%- through [0-9a-f]+ %-%->%s*$") then
    return {}
  end
end

function BulletList(list)
  for _, item in ipairs(list.content) do
    local first = item[1]
    local word = first and first.content and first.content[1]
    local label = word and word.t == "Str" and tags[word.text]
    if not label then
      error("untagged item: " .. pandoc.utils.stringify(item))
    end
    first.content[1] = pandoc.RawInline("html", label)
  end
  return list
end

function Header(h)
  if h.level ~= 3 then
    return nil
  end
  local text = pandoc.utils.stringify(h.content)
  local d, m, y = text:match("^(%d%d?) (%a+) (%d%d%d%d)$")
  if not months[m] then
    error("day heading is not D Month YYYY: " .. text)
  end
  local datetime = string.format("%s-%02d-%02d", y, months[m], tonumber(d))
  local content = { pandoc.RawInline("html", '<time datetime="' .. datetime .. '">') }
  for _, inline in ipairs(h.content) do
    content[#content + 1] = inline
  end
  content[#content + 1] = pandoc.RawInline("html", "</time>")
  h.content = content
  return h
end
