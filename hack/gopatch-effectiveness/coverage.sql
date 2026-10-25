SELECT row_to_json(s)
FROM (
  SELECT source_kind, session_id, path, byte_offset, observed_size,
         parser_version, updated_at
  FROM public.captain_session_sources
  ORDER BY source_kind, path
) s;
