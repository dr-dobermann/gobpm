-- the order-classification script: reads process data lazily through the
-- read-only `data` table (a typo'd name fails the task loud), probes
-- optional data with has(), and returns its outputs as a named table.

local total = data.total
local tier  = has("tier") and data.tier or "retail"

local pct
if tier == "vip" and total > 100 then
  pct = 25
elseif total > 100 then
  pct = 15
else
  pct = 5
end

print(string.format("  [script] tier=%s total=%d -> %d%%", tier, total, pct))

return {
  discount_pct = pct,
  lane         = pct >= 15 and "wholesale" or "retail",
}
