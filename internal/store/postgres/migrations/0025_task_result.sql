-- A one-off task keeps the one line its owner gave when completing it (an untimed task had nowhere to put it: results were
-- only kept per occurrence, and an occurrence needs a time). For an agent's task it is what was found or done, and it is what
-- a task_done watch wakes the agent with.
ALTER TABLE life.items ADD COLUMN result text NOT NULL DEFAULT '' CHECK (char_length(result) <= 2000);
