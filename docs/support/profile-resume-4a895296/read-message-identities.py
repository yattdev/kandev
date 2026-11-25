"""Read-only fixed failed-call window; identities/status only, never payload."""
import json,sqlite3
c=sqlite3.connect('file:/data/data/kandev.db?mode=ro',uri=True,timeout=5);c.row_factory=sqlite3.Row
c.execute('PRAGMA query_only=ON');c.execute('BEGIN')
ws='25f53734-5aea-490e-b7ba-d5da6fe821dd';root='8ace1738-e080-47a8-85f4-f4d2e0f133db';task='5129db6a-0707-421b-b80f-cce4c36a6ad1';session='16bcc779-85b0-45a1-9ab7-46f3668fe410'
assert c.execute('SELECT workspace_id FROM tasks WHERE id=?',(root,)).fetchone()[0]==ws
assert c.execute('SELECT workspace_id FROM tasks WHERE id=?',(task,)).fetchone()[0]==ws
row=c.execute('SELECT id,task_id,agent_profile_id,execution_profile_id,queue_incarnation_id,exact_profile_generation,exact_profile_revision,route_generation,route_state FROM task_sessions WHERE id=? AND task_id=?',(session,task)).fetchone();assert row
result={'session_binding_identity':dict(row),'time_window_utc':['2026-11-25 23:05:00','2026-11-25 23:07:00']}
for table in ('agent_delivery_submissions','task_message_send_operations'):
 exists=c.execute("SELECT 1 FROM sqlite_master WHERE type='table' AND name=?",(table,)).fetchone()
 result[table]={'table_present':bool(exists)}
 if not exists:continue
 if table=='agent_delivery_submissions':
  sql='SELECT id,session_id,incarnation_id,harness_generation,owner_generation,dispatch_attempt_id,state,outcome,created_at,updated_at FROM agent_delivery_submissions WHERE session_id=? AND datetime(created_at)>=? AND datetime(created_at)<=? ORDER BY created_at DESC LIMIT 10';params=(session,*result['time_window_utc'])
 else:
  sql='SELECT id,operation_id,sender_task_id,sender_session_id,target_task_id,target_session_id,requested_session_id,state,delivery_status,message_id,queued_entry_id,failure_code,created_at,settled_at FROM task_message_send_operations WHERE sender_task_id=? AND target_task_id=? AND (target_session_id=? OR requested_session_id=?) AND datetime(created_at)>=? AND datetime(created_at)<=? ORDER BY created_at DESC LIMIT 10';params=(root,task,session,session,*result['time_window_utc'])
 result[table]['rows']=[dict(r) for r in c.execute(sql,params)]
print(json.dumps(result,indent=2));c.rollback();c.close()
