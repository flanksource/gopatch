SELECT row_to_json(u)
FROM (
  SELECT t.session_id, t.id AS turn_id, t.provider_turn_id, t.turn_index,
         c.model, c.provider, c.effort, c.started_at, c.ended_at,
         c.input_tokens, c.output_tokens, c.reasoning_tokens,
         c.cache_read_tokens, c.cache_write_tokens,
         c.provider_cost_usd, c.input_cost, c.output_cost,
         c.reasoning_cost, c.cache_read_cost, c.cache_write_cost
  FROM public.captain_turns t
  JOIN public.captain_model_calls c ON c.turn_id = t.id
  WHERE t.session_id = ANY(string_to_array(:'session_ids', ',')::uuid[])
) u
ORDER BY u.session_id, u.turn_index;
