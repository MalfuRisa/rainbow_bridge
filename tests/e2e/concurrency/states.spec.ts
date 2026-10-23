import {test,expect} from '@playwright/test'
import {mockApi,config,ok,fail} from '../auth/mock'

test('fresh detail supplies both form and fixed baseline; list revision cannot replace it',async({page})=>{
 const mock=await mockApi(page,true)
 const revision='00000000-0000-4000-8000-000000000002'
 mock.overrides.set('config/list',route=>ok(route,{list:[{...config,content:'list body',edit_revision:'00000000-0000-4000-8000-000000000003'}],total:1}))
 mock.overrides.set('config/detail',route=>ok(route,{config:{...config,content:'fresh detail',edit_revision:revision}}))
 mock.overrides.set('config/update',route=>fail(route,409,'CONFIG_EDIT_CONFLICT'))
 await page.goto('config?env=qa&pipeline=web')
 await page.getByRole('button',{name:'编辑 站点标题',exact:true}).click()
 const dialog=page.getByRole('dialog')
 await expect(dialog.locator('#content')).toHaveValue('fresh detail')
 await dialog.locator('#content').fill('my draft')
 await dialog.getByRole('button',{name:'保存',exact:true}).click()
 await expect(dialog.getByRole('button',{name:'保存',exact:true})).toBeDisabled()
 expect(mock.writes('config/update')[0].postDataJSON().expected_edit_revision).toBe(revision)
 await expect(dialog.locator('#content')).toHaveValue('my draft')
})

test('404 keeps draft and copy/cancel without an implicit create',async({page})=>{
 const mock=await mockApi(page,true)
 mock.overrides.set('config/update',route=>fail(route,404,'CONFIG_EDIT_TARGET_NOT_FOUND'))
 await page.goto('config?env=qa&pipeline=web');await page.getByRole('button',{name:'编辑 站点标题',exact:true}).click()
 const dialog=page.getByRole('dialog');await expect(dialog.locator('#content')).toHaveValue(config.content)
 await dialog.locator('#content').fill('deleted draft')
 await dialog.getByRole('button',{name:'保存',exact:true}).click()
 await expect(dialog.getByRole('button',{name:'复制我的草稿',exact:true})).toBeVisible()
 await expect(dialog.getByRole('button',{name:'重新加载',exact:true})).toHaveCount(0)
 await expect(dialog.getByRole('button',{name:'保存',exact:true})).toBeDisabled()
 await expect(dialog.locator('#content')).toHaveValue('deleted draft')
 expect(mock.count('config/create')).toBe(0)
 await dialog.getByRole('button',{name:'取消',exact:true}).click();await expect(dialog).toBeHidden()
})

test('late detail from an unmounted scope cannot populate the next editing dialog',async({page})=>{
 const mock=await mockApi(page,true)
 let release!:()=>void
 const pending=new Promise<void>(resolve=>{release=resolve})
 mock.overrides.set('config/detail',async route=>{await pending;await ok(route,{config:{...config,content:'old response'}}).catch(()=>{})})
 await page.goto('config?env=qa&pipeline=web');await page.getByRole('button',{name:'编辑 站点标题',exact:true}).click()
 await expect(page.getByRole('dialog').locator('#content')).toBeDisabled()
 await page.goto('environments')
 mock.overrides.set('config/detail',route=>ok(route,{config:{...config,content:'new response'}}))
 await page.goto('config?env=qa&pipeline=web');await page.getByRole('button',{name:'编辑 站点标题',exact:true}).click()
 await expect(page.getByRole('dialog').locator('#content')).toHaveValue('new response')
 release()
 await expect(page.getByRole('dialog').locator('#content')).toHaveValue('new response')
})
