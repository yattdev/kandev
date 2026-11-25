"""Read-only launch-policy comparison; never selects credential/environment values."""
import json,sqlite3
c=sqlite3.connect('file:/data/data/kandev.db?mode=ro',uri=True,timeout=5);c.row_factory=sqlite3.Row
c.execute('PRAGMA query_only=ON');c.execute('BEGIN')
ws='25f53734-5aea-490e-b7ba-d5da6fe821dd';task='5129db6a-0707-421b-b80f-cce4c36a6ad1';session='16bcc779-85b0-45a1-9ab7-46f3668fe410';old='62434899-7d6b-432d-b93f-0b71c67eb35d'
assert c.execute('SELECT workspace_id FROM tasks WHERE id=?',(task,)).fetchone()[0]==ws
assert c.execute('SELECT agent_profile_id FROM task_sessions WHERE id=? AND task_id=?',(session,task)).fetchone()[0]==old
fields=['agent_id','model','mode','auto_approve','dangerously_skip_permissions','cli_passthrough','allow_indexing','role','skill_ids','desired_skills','permissions','cli_flags','command_prefix','mcp_selection_mode','mcp_selected_servers','copilot_selected_plugins','fallback_model','auto_fallback','execution_agent_profile_id','provider_kind','require_exact_model']
result=[]
for candidate in ['2653283e-3df4-48d6-8242-774b24b54f38','ce9f96f7-4434-4729-b23b-926b4b72f417']:
 row=c.execute('SELECT '+','.join('o.'+f+' IS n.'+f+' AS '+f for f in fields)+',json_extract(o.settings,\'$.config_options.reasoning_effort\') AS old_reasoning_effort,json_extract(n.settings,\'$.config_options.reasoning_effort\') AS new_reasoning_effort FROM agent_profiles o JOIN agent_profiles n ON n.id=? AND n.deleted_at IS NULL AND n.workspace_id IN (?,?) WHERE o.id=?',(candidate,'',ws,old)).fetchone()
 result.append({'candidate_id':candidate,'equal_fields_and_reasoning':dict(row)})
print(json.dumps(result,indent=2));c.rollback();c.close()
