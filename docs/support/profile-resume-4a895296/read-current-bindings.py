"""Exact-workflow public configuration and queue identity projection only."""
import json
import sqlite3
WORKSPACE='25f53734-5aea-490e-b7ba-d5da6fe821dd'
ROOT='8ace1738-e080-47a8-85f4-f4d2e0f133db'
TASK='5129db6a-0707-421b-b80f-cce4c36a6ad1'
SESSION='16bcc779-85b0-45a1-9ab7-46f3668fe410'
OLD='62434899-7d6b-432d-b93f-0b71c67eb35d'
c=sqlite3.connect('file:/data/data/kandev.db?mode=ro',uri=True,timeout=5);c.row_factory=sqlite3.Row
c.execute('PRAGMA query_only=ON');c.execute('BEGIN')
assert c.execute('SELECT workspace_id FROM tasks WHERE id=?',(ROOT,)).fetchone()[0]==WORKSPACE
task=c.execute('SELECT workflow_id FROM tasks WHERE id=? AND workspace_id=?',(TASK,WORKSPACE)).fetchone();assert task
session=c.execute('SELECT agent_profile_id FROM task_sessions WHERE id=? AND task_id=?',(SESSION,TASK)).fetchone();assert session[0]==OLD
result={'workflow_defaults':dict(c.execute('SELECT id,agent_profile_id,updated_at FROM workflows WHERE id=? AND workspace_id=?',(task[0],WORKSPACE)).fetchone()),'workspace_defaults':dict(c.execute('SELECT id,default_agent_profile_id FROM workspaces WHERE id=?',(WORKSPACE,)).fetchone()),'steps':[]}
ids={OLD}
for row in c.execute('SELECT id,name,agent_profile_id,events,updated_at FROM workflow_steps WHERE workflow_id=? ORDER BY position LIMIT 20',(task[0],)):
 d=dict(row);events=json.loads(d.pop('events') or '{}');d['event_action_types']={key:[e.get('type') for e in values if isinstance(e,dict)] for key,values in events.items() if isinstance(values,list)}
 result['steps'].append(d)
 if d['agent_profile_id']:ids.add(d['agent_profile_id'])
for key in ('workflow_defaults','workspace_defaults'):
 value=result[key].get('agent_profile_id') or result[key].get('default_agent_profile_id')
 if value:ids.add(value)
fields='p.id,p.name,p.agent_id,a.name AS agent_name,p.model,p.mode,p.workspace_id,p.enabled,p.deleted_at,p.auto_approve,p.dangerously_skip_permissions,p.cli_passthrough,p.role,p.mcp_selection_mode,p.updated_at'
result['configured_profiles']=[]
for profile in sorted(ids):
 row=c.execute('SELECT '+fields+' FROM agent_profiles p LEFT JOIN agents a ON a.id=p.agent_id WHERE p.id=? AND p.workspace_id IN (?,?)',(profile,'',WORKSPACE)).fetchone()
 result['configured_profiles'].append(dict(row) if row else {'id':profile,'not_found_or_foreign':True})
result['same_agent_live_profiles']=[dict(r) for r in c.execute('SELECT '+fields+' FROM agent_profiles p LEFT JOIN agents a ON a.id=p.agent_id WHERE p.agent_id=(SELECT agent_id FROM agent_profiles WHERE id=?) AND p.model=(SELECT model FROM agent_profiles WHERE id=?) AND p.deleted_at IS NULL AND p.workspace_id IN (?,?) ORDER BY p.id LIMIT 20',(OLD,OLD,'',WORKSPACE))]
result['queued_message_identities']=[dict(r) for r in c.execute('SELECT id,session_id,task_id,position,queued_at,queued_by FROM queued_messages WHERE session_id=? AND task_id=? ORDER BY position LIMIT 10',(SESSION,TASK))]
result['same_task_session_identities']=[dict(r) for r in c.execute('SELECT id,agent_profile_id,state,is_primary FROM task_sessions WHERE task_id=? ORDER BY id LIMIT 10',(TASK,))]
print(json.dumps(result,indent=2));c.rollback();c.close()
