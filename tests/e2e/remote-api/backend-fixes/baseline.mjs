import { request } from '@playwright/test'
import { writeFile, mkdir } from 'node:fs/promises'
const label=process.argv[2]
if(!['before','after'].includes(label)) throw new Error('Expected before/after label')
const base='http://10.60.226.43:31880/rainbow-bridge/api/v1/'
const context=await request.newContext({baseURL:base})
let csrf
try {
 const login=await context.post('auth/login',{headers:{Origin:'http://10.60.226.43:31880'},data:{username:process.env.RB_REMOTE_USER,password:process.env.RB_REMOTE_PASSWORD}})
 if(login.status()!==200)throw new Error('Baseline login failed')
 csrf=(await login.json()).data.csrf_token
 const get=async path=>{const r=await context.get(path);if(r.status()!==200)throw new Error('Baseline read failed');return (await r.json()).data}
 const rows=[]
 for(const env of (await get('environment/list')).list||[]) for(const scope of (await get('pipeline/list?environment_key='+encodeURIComponent(env.environment_key))).list||[]){
  const q=`environment_key=${encodeURIComponent(env.environment_key)}&pipeline_key=${encodeURIComponent(scope.pipeline_key)}&scope_id=${scope.scope_id}`
  const freeze=await get('freeze/status?'+q),active=await get('release/active?'+q)
  rows.push({environment_id:env.environment_id,scope_id:scope.scope_id,freeze:{state:freeze.state,revision:freeze.revision},active:active.active||null})
 }
 const output=process.env.RB_BASELINE_OUTPUT||'.local-qa/backend-fixes'
 await mkdir(output,{recursive:true})
 await writeFile(output+'/baseline-'+label+'.json',JSON.stringify(rows.sort((a,b)=>a.scope_id.localeCompare(b.scope_id)),null,2))
 console.log('Original scope metadata captured: '+rows.length)
} finally {
 if(csrf)await context.post('auth/logout',{headers:{Origin:'http://10.60.226.43:31880','X-CSRF-Token':csrf},data:{}})
 await context.dispose()
}
