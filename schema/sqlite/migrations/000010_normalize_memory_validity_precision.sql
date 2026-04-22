-- Normalize memories.valid_from / valid_to to a fixed-width RFC3339Nano
-- representation so lex comparison (and the idx_memories_valid_window
-- index created in 000009) can be used without wrapping the column in
-- SQLite's datetime(), which truncates to second precision.
--
-- Before: variable-width fractional seconds ("...00Z", "...00.1Z",
-- "...00.123456789Z"). Lex compare fails because `.` (0x2E) sorts
-- before `Z` (0x5A), so "...00Z" > "...00.100Z".
-- After:  always nine fractional digits followed by Z
-- ("...00.000000000Z"). Lex compare matches real temporal order and
-- the filter can drop the datetime() wrap, finally using the index.
--
-- The normalization preserves full precision: values that were
-- already written with fewer than 9 fractional digits are padded
-- with trailing zeros (semantically identical); values without a
-- fractional component gain .000000000.

UPDATE memories
SET valid_from =
  CASE
    WHEN instr(valid_from, '.') = 0
      THEN substr(valid_from, 1, length(valid_from) - 1) || '.000000000Z'
    ELSE
      substr(valid_from, 1, instr(valid_from, '.')) ||
      substr(substr(valid_from, instr(valid_from, '.') + 1, length(valid_from) - instr(valid_from, '.') - 1) || '000000000', 1, 9) ||
      'Z'
  END
WHERE valid_from IS NOT NULL;

UPDATE memories
SET valid_to =
  CASE
    WHEN instr(valid_to, '.') = 0
      THEN substr(valid_to, 1, length(valid_to) - 1) || '.000000000Z'
    ELSE
      substr(valid_to, 1, instr(valid_to, '.')) ||
      substr(substr(valid_to, instr(valid_to, '.') + 1, length(valid_to) - instr(valid_to, '.') - 1) || '000000000', 1, 9) ||
      'Z'
  END
WHERE valid_to IS NOT NULL;
