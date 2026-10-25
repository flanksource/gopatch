SELECT row_to_json(s)
FROM (
  SELECT id, source, provider_session_id, history_file, cwd, model,
         message_count, tool_call_count, parent_session_id, root_session_id,
         parent_relation, activity_at
  FROM public.captain_session_overview_rows
  WHERE source IN ('claude', 'codex')
    AND (activity_at >= :'since'::timestamptz OR activity_at IS NULL)
    AND message_count > 0
  ORDER BY source, id
) s;
