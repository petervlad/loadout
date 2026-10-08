-- Wrap every table so a wide one scrolls inside itself on a phone instead
-- of widening the page.
function Table(t)
  return pandoc.Div(t, {class = "table"})
end
