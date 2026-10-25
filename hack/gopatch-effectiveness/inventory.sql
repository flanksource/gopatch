SELECT row_to_json(c)
FROM (
  SELECT m.session_id, m.id AS message_id, m.turn_id, m.sequence,
         m.source_line, p.position AS part_index, m.occurred_at,
         p.part->>'toolCallId' AS tool_call_id,
         p.part->>'toolName' AS tool, p.part->>'state' AS state,
         CASE
           WHEN p.part->>'toolName' ~* '(bash|edit|write|exec|shell|patch|rename|move)'
             THEN CASE WHEN p.part->>'toolName' = 'Bash'
                             AND p.part->'input' ? 'command'
                       THEN jsonb_build_object('command', p.part#>>'{input,command}')
                       ELSE p.part->'input' END
           ELSE NULL
         END AS input,
         p.part ? 'output' AS output_present
  FROM public.captain_messages m
  CROSS JOIN LATERAL jsonb_array_elements(m.parts)
    WITH ORDINALITY p(part, position)
  WHERE m.session_id = ANY(string_to_array(:'session_ids', ',')::uuid[])
    AND m.occurred_at >= :'since'::timestamptz
    AND m.occurred_at < :'before'::timestamptz
    AND p.part ? 'toolName'
) c
ORDER BY c.session_id, c.sequence, c.part_index;
