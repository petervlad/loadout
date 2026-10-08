-- Turn every chapter (a level-2 heading and what follows it, up to the next
-- one) into a collapsible <details> section. The heading stays a real
-- heading, so it keeps its id and its place in the contents. The chapters
-- in always_open stay plain sections that cannot be folded.
local always_open = { ["the-one-rule"] = true }

-- The first foldable chapter gets the Open all and Close all buttons above
-- it; they stay hidden until docs.js runs.
local tools = '<div class="chapter-tools" id="chapter-tools" hidden>'
  .. '<button type="button" data-open>Open all</button>'
  .. '<button type="button" data-close>Close all</button></div>'

function Pandoc(doc)
  local out, open, first = {}, false, true
  for _, b in ipairs(doc.blocks) do
    if b.t == "Header" and b.level == 2 then
      if open then
        out[#out + 1] = pandoc.RawBlock("html", "</div></details>")
        open = false
      end
      if always_open[b.identifier] then
        out[#out + 1] = pandoc.RawBlock("html", '<section class="chapter-fixed">')
        out[#out + 1] = b
        out[#out + 1] = pandoc.RawBlock("html", "</section>")
      else
        if first then
          out[#out + 1] = pandoc.RawBlock("html", tools)
          first = false
        end
        out[#out + 1] = pandoc.RawBlock("html", '<details class="chapter"><summary>')
        out[#out + 1] = b
        out[#out + 1] = pandoc.RawBlock("html", '</summary><div class="chapter-body">')
        open = true
      end
    else
      out[#out + 1] = b
    end
  end
  if open then
    out[#out + 1] = pandoc.RawBlock("html", "</div></details>")
  end
  doc.blocks = out
  return doc
end
