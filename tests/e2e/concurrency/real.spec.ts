import {test,expect} from '@playwright/test'
import {uiLogin,management,apiUrl} from '../auth/real-helper'
import {mkdir} from 'node:fs/promises'
import {join} from 'node:path'

test('C13/C14: two real login contexts reject stale save, retain/copy draft, reload cancel/failure/success', async ({browser})=>{
 test.setTimeout(90_000)
 const a=await browser.newContext({permissions:['clipboard-read','clipboard-write']})
 const b=await browser.newContext({permissions:['clipboard-read','clipboard-write']})
 try {
  const pa=await a.newPage(), pb=await b.newPage()
  await uiLogin(pa);await uiLogin(pb)
  const api=await management(pa)
  const env='edit_'+Date.now().toString(36)
  await api.post('environment/create',{environment:{environment_key:env,environment_name:env,is_active:true}})
  const created=await api.post('config/create',{config:{environment_key:env,pipeline_key:'default',alias:'CTITLE',name:'并发配置',type:'text',content:'R1'}})
  const anonymous=await browser.newContext()
  try {
   const response=await anonymous.request.post(apiUrl(pa,'config/update'),{data:{config:created.config}})
   expect(response.status()).toBe(401)
  } finally {await anonymous.close()}
  const withoutCsrf=await pa.request.post(apiUrl(pa,'config/update'),{headers:{Origin:new URL(pa.url()).origin},data:{config:created.config,expected_edit_revision:created.config.edit_revision}})
  expect(withoutCsrf.status()).toBe(403)
  const query='config?env='+env+'&pipeline=default'
  for (const page of [pa,pb]) {await page.goto(query);await page.getByRole('button',{name:'编辑 并发配置',exact:true}).click();await expect(page.locator('#content')).toHaveValue('R1')}
  const da=pa.getByRole('dialog'), db=pb.getByRole('dialog')
  await da.locator('#content').fill('winner A')
  const ar=pa.waitForResponse(r=>r.url().endsWith('/config/update'))
  await da.getByRole('button',{name:'保存',exact:true}).click();expect((await ar).status()).toBe(200)
  await expect(da).toBeHidden()
  await db.locator('#content').fill('draft B 9007199254740993')
  const br=pb.waitForResponse(r=>r.url().endsWith('/config/update'))
  await db.getByRole('button',{name:'保存',exact:true}).click();expect((await br).status()).toBe(409)
  await expect(db.getByText('这条配置已被其他操作更新。你的修改尚未保存，请重新加载后核对。',{exact:true})).toBeVisible()
  await expect(db.locator('#content')).toHaveValue('draft B 9007199254740993')
  await expect(db.getByRole('button',{name:'保存',exact:true})).toBeDisabled()
  const latest=await api.get('config/detail?environment_key='+env+'&pipeline_key=default&resource_key='+created.config.resource_key)
  expect(latest.config.content).toBe('winner A')
  await db.getByRole('button',{name:'复制我的草稿',exact:true}).click()
  expect(JSON.parse(await pb.evaluate(()=>navigator.clipboard.readText())).content).toBe('draft B 9007199254740993')
  for(const width of [1280,1440]){
   await pb.setViewportSize({width,height:1000})
   const box=await db.boundingBox();expect(box!.x).toBeGreaterThanOrEqual(0);expect(box!.x+box!.width).toBeLessThanOrEqual(width)
   if(process.env.RB_QA_VISUAL_DIR){await mkdir(process.env.RB_QA_VISUAL_DIR,{recursive:true});await pb.screenshot({path:join(process.env.RB_QA_VISUAL_DIR,`conflict-${width}.png`)})}
  }
  pb.once('dialog',d=>d.dismiss());await db.getByRole('button',{name:'重新加载',exact:true}).click()
  await expect(db.locator('#content')).toHaveValue('draft B 9007199254740993')
  await pb.route('**/config/detail?**',route=>route.abort())
  pb.once('dialog',d=>d.accept());await db.getByRole('button',{name:'重新加载',exact:true}).click()
  await expect(db.getByRole('alert')).toContainText('服务暂不可用')
  await expect(db.locator('#content')).toHaveValue('draft B 9007199254740993')
  await expect(db.getByRole('button',{name:'保存',exact:true})).toBeDisabled()
  await pb.unroute('**/config/detail?**')
  pb.once('dialog',d=>d.accept());await db.getByRole('button',{name:'重新加载',exact:true}).click()
  await expect(db.locator('#content')).toHaveValue('winner A')
  await expect(db.getByRole('button',{name:'保存',exact:true})).toBeEnabled()
  await db.locator('#content').fill('reviewed B')
  const final=pb.waitForResponse(r=>r.url().endsWith('/config/update'))
  await db.getByRole('button',{name:'保存',exact:true}).click();expect((await final).status()).toBe(200)
  await expect(db).toBeHidden()
  expect((await api.get('config/detail?environment_key='+env+'&pipeline_key=default&resource_key='+created.config.resource_key)).config.content).toBe('reviewed B')
 } finally {await a.close();await b.close()}
})

test('C15: lost save response keeps draft, blocks repeat save and reloads committed result',async({page})=>{
 await uiLogin(page)
 const api=await management(page),env='lost_'+Date.now().toString(36)
 await api.post('environment/create',{environment:{environment_key:env,environment_name:env,is_active:true}})
 await api.post('config/create',{config:{environment_key:env,pipeline_key:'default',alias:'LOST',name:'未知结果',type:'number',content:'900719925474099312345'}})
 await page.goto('config?env='+env+'&pipeline=default')
 await page.getByRole('button',{name:'编辑 未知结果',exact:true}).click()
 const dialog=page.getByRole('dialog');await expect(dialog.locator('#content')).toHaveValue('900719925474099312345')
 let writes=0
 await page.route('**/config/update',async route=>{writes++;const response=await route.fetch();expect(response.status()).toBe(200);await route.abort()})
 await dialog.locator('#content').fill('900719925474099398765')
 await dialog.getByRole('button',{name:'保存',exact:true}).click()
 await expect(dialog.getByRole('alert')).toContainText('保存结果未确认，请重新加载核对')
 await expect(dialog.locator('#content')).toHaveValue('900719925474099398765')
 await expect(dialog.getByRole('button',{name:'保存',exact:true})).toBeDisabled()
 expect(writes).toBe(1)
 page.once('dialog',d=>d.accept());await dialog.getByRole('button',{name:'重新加载',exact:true}).click()
 await expect(dialog.getByRole('alert')).toHaveCount(0)
 await expect(dialog.locator('#content')).toHaveValue('900719925474099398765')
 await expect(dialog.getByRole('button',{name:'保存',exact:true})).toBeEnabled()
})
