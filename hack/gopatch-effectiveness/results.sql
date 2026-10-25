SELECT row_to_json(r)
FROM (
  SELECT m.id AS message_id, m.session_id, m.turn_id, m.sequence,
         m.source_line, m.occurred_at, p.part->>'toolCallId' AS tool_call_id,
         p.part->>'state' AS state, p.part->>'errorText' AS error,
         p.part->'output' AS output
  FROM public.captain_messages m
  CROSS JOIN LATERAL jsonb_array_elements(m.parts) p(part)
  WHERE m.session_id = :'session_id'::uuid
    AND p.part->>'toolCallId' = ANY(string_to_array(:'call_ids', ','))
    AND (p.part ? 'output' OR p.part ? 'errorText')
) r
ORDER BY r.sequence;
