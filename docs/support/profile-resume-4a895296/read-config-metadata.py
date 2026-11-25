"""Read-only fixed-request configuration metadata; no content or credentials."""
import json
import sqlite3

WORKSPACE = '25f53734-5aea-490e-b7ba-d5da6fe821dd'
ROOT = '8ace1738-e080-47a8-85f4-f4d2e0f133db'
TASK = '5129db6a-0707-421b-b80f-cce4c36a6ad1'
SESSION = '16bcc779-85b0-45a1-9ab7-46f3668fe410'
PROFILE = '62434899-7d6b-432d-b93f-0b71c67eb35d'
c = sqlite3.connect('file:/data/data/kandev.db?mode=ro', uri=True, timeout=5)
c.row_factory = sqlite3.Row
c.execute('PRAGMA query_only=ON'); c.execute('BEGIN')
assert c.execute('SELECT workspace_id FROM tasks WHERE id=?', (ROOT,)).fetchone()[0] == WORKSPACE
row = c.execute('SELECT id,workspace_id,workflow_id,workflow_step_id,state,archived_at FROM tasks WHERE id=? AND workspace_id=?', (TASK,WORKSPACE)).fetchone()
assert row is not None
result = dict(request_id='4a895296-0892-461d-857e-1f06f2fff8c4', task=dict(row), schemas={})
for table in ('agents','agent_profiles','workflows','workflow_steps','task_sessions','queued_messages'):
    result['schemas'][table] = [r['name'] for r in c.execute('PRAGMA table_info('+table+')')]
result['session'] = dict(c.execute('SELECT id,task_id,agent_profile_id,state,is_primary,completed_at FROM task_sessions WHERE id=? AND task_id=?',(SESSION,TASK)).fetchone())
result['workflow'] = dict(c.execute('SELECT id,name,workspace_id FROM workflows WHERE id=? AND workspace_id=?',(row['workflow_id'],WORKSPACE)).fetchone())
result['steps'] = [dict(r) for r in c.execute('SELECT id,workflow_id,name,position,agent_profile_id,allow_manual_move,auto_advance_requires_signal,updated_at FROM workflow_steps WHERE workflow_id=? ORDER BY position LIMIT 20',(row['workflow_id'],))]
result['stored_profile'] = dict(c.execute('SELECT id,agent_id,name,agent_display_name,model,mode,enabled,deleted_at,workspace_id,role,auto_approve,dangerously_skip_permissions,cli_passthrough,updated_at FROM agent_profiles WHERE id=?',(PROFILE,)).fetchone())
print(json.dumps(result,indent=2))
c.rollback();c.close()
