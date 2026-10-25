SELECT row_to_json(c)
FROM (
  SELECT m.id AS message_id, m.session_id, m.turn_id, m.sequence,
         m.source_line, m.occurred_at, m.role,
         CASE WHEN p.part ? 'text' THEN p.part->>'text' END AS text,
         CASE WHEN p.part ? 'toolCallId' THEN p.part END AS tool_part
  FROM public.captain_messages m
  CROSS JOIN LATERAL jsonb_array_elements(m.parts) p(part)
  WHERE m.session_id = :'session_id'::uuid
    AND m.sequence BETWEEN :'first'::bigint AND :'last'::bigint
    AND p.part->>'type' <> 'reasoning'
) c
ORDER BY c.sequence;
